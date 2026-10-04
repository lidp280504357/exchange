package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/derivatives/adapters/postgres"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/migrations"
)

// derivativesCmd reads and repairs derivatives-service's state: the
// contracts under reduce-only, and invariant 6 against the ledger.
func derivativesCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "derivatives")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, db, migrations.Derivatives(), quiet); err != nil {
		return err
	}
	store := postgres.NewStore(db, event.NewFactory("exchangectl", "cli"))
	switch args[0] {
	case "states":
		list, err := store.Read().Contracts().All(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SYMBOL\tREDUCE_ONLY\tREASON\tSINCE\tLIFTED_BY")
		for _, s := range list {
			fmt.Fprintf(w, "%s\t%v\t%s\t%s\t%s\n", s.Symbol, s.ReduceOnly, s.Reason, s.Since.UTC().Format(time.RFC3339), s.LiftedBy)
		}
		return w.Flush()
	case "resume":
		if len(args) != 2 {
			return errors.New("derivatives resume needs a contract, e.g. BTC-USDT-PERP")
		}
		symbol := strings.ToUpper(args[1])
		changed, err := store.Read().Contracts().Lift(ctx, symbol, actor(), time.Now())
		if err != nil {
			return err
		}
		if !changed {
			fmt.Fprintf(out, "%s is not under reduce-only\n", symbol)
			return nil
		}
		fmt.Fprintf(out, "%s: reduce-only lifted\n", symbol)
		return nil
	case "funding":
		return derivativesFunding(ctx, db, args[1:], out)
	case "reconcile":
		return derivativesReconcile(ctx, cfg, db, out)
	default:
		return fmt.Errorf("unknown derivatives command %q", args[0])
	}
}

// derivativesFunding lists the newest funding rounds with what their
// positions paid and received; it fails when a round waits for its rate
// past the two hours after which it is skipped, or when the receivers got
// more than the payers and the insurance fund paid.
func derivativesFunding(ctx context.Context, db *pg.DB, args []string, out io.Writer) error {
	symbol, limit := "", 12
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--symbol" && i+1 < len(args):
			i++
			symbol = strings.ToUpper(args[i])
		case args[i] == "--limit" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return errors.New("--limit needs a positive number")
			}
			limit = n
		default:
			return fmt.Errorf("unknown derivatives funding argument %q", args[i])
		}
	}
	rows, err := db.Query(ctx, `SELECT r.symbol, r.funding_time, r.status, coalesce(r.funding_rate::text, ''),
			coalesce(r.mark_price::text, ''), r.positions, coalesce(sum(p.amount) FILTER (WHERE p.amount < 0), 0),
			coalesce(sum(p.amount) FILTER (WHERE p.amount > 0), 0), coalesce(sum(p.insurance), 0),
			count(p.settled_at)
		FROM funding_rounds r LEFT JOIN funding_payments p USING (symbol, funding_time)
		WHERE $1 = '' OR r.symbol = $1
		GROUP BY r.symbol, r.funding_time ORDER BY r.funding_time DESC, r.symbol LIMIT $2`, symbol, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SYMBOL\tFUNDING_TIME\tSTATUS\tRATE\tMARK\tPOSITIONS\tSETTLED\tPAID\tRECEIVED\tINSURANCE")
	var problems []string
	for rows.Next() {
		var sym, status, rate, mark string
		var at time.Time
		var positions, settled int
		var paid, received, insurance decimal.Decimal
		if err := rows.Scan(&sym, &at, &status, &rate, &mark, &positions, &paid, &received, &insurance, &settled); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n", sym, at.UTC().Format(time.RFC3339), status, rate, mark, positions,
			settled, paid.Neg(), received, insurance)
		if status == "SNAPSHOT" && time.Since(at) > 2*time.Hour+10*time.Minute {
			problems = append(problems, fmt.Sprintf("%s %s still waits for its rate", sym, at.UTC().Format(time.RFC3339)))
		}
		if received.GreaterThan(paid.Neg().Add(insurance)) {
			problems = append(problems, fmt.Sprintf("%s %s: received %s > paid %s + insurance %s", sym, at.UTC().Format(time.RFC3339),
				received, paid.Neg(), insurance))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// derivativesReconcile checks invariant 6 from both schemas: per contract
// the long and short quantities match, and PNL_CLEARING + long cost −
// short cost = 0. Fills in flight show as a passing difference.
func derivativesReconcile(ctx context.Context, cfg settings, db *pg.DB, out io.Writer) error {
	ledgerDB, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 1}, "ledger")
	if err != nil {
		return err
	}
	defer ledgerDB.Close()
	broken := 0
	rows, err := db.Query(ctx, `SELECT symbol, sum(quantity), sum(CASE WHEN quantity > 0 THEN entry_cost ELSE -entry_cost END)
		FROM positions GROUP BY symbol ORDER BY symbol`)
	if err != nil {
		return err
	}
	net := decimal.Zero
	for rows.Next() {
		var symbol string
		var qty, cost decimal.Decimal
		if err := rows.Scan(&symbol, &qty, &cost); err != nil {
			rows.Close()
			return err
		}
		fmt.Fprintf(out, "%-16s long − short %s, long cost − short cost %s\n", symbol, qty, cost)
		if !qty.IsZero() {
			broken++
		}
		net = net.Add(cost)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	clearing := decimal.Zero
	err = ledgerDB.QueryRow(ctx, `SELECT coalesce(sum(available), 0) FROM accounts WHERE account_type = 'PNL_CLEARING' AND asset = 'USDT'`).
		Scan(&clearing)
	if err != nil {
		return err
	}
	sum := clearing.Add(net)
	fmt.Fprintf(out, "PNL_CLEARING %s + long cost − short cost %s = %s (invariant 6: 0)\n", clearing, net, sum)
	var pending int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pending_settlements`).Scan(&pending); err != nil {
		return err
	}
	fmt.Fprintf(out, "settlements waiting on the ledger: %d\n", pending)
	if !sum.IsZero() {
		broken++
	}
	if broken > 0 || pending > 0 {
		return fmt.Errorf("%d derivatives mismatches, %d parked settlements", broken, pending)
	}
	return nil
}
