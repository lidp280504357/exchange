package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/derivatives/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/migrations"
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
	case "reconcile":
		return derivativesReconcile(ctx, cfg, db, out)
	default:
		return fmt.Errorf("unknown derivatives command %q", args[0])
	}
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
