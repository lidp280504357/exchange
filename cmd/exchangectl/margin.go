package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/adapters/postgres"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/migrations"
)

// marginCmd seeds and reads margin-service's state (margin design
// 2026-10-06): the terms from deploy/instruments/margin.json, the loans,
// and invariant 7 against the ledger.
func marginCmd(ctx context.Context, cfg settings, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "margin")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, db, migrations.Margin(), quiet); err != nil {
		return err
	}
	store := postgres.NewStore(db, event.NewFactory("exchangectl", "cli"))
	switch args[0] {
	case "apply":
		return marginApply(ctx, store, args[1:], in, out)
	case "terms":
		return marginTerms(ctx, store, out)
	case "loans":
		return marginLoans(ctx, store, out)
	case "reconcile":
		return marginReconcile(ctx, cfg, store, out)
	case "liquidate":
		return marginLiquidate(ctx, args[1:], out)
	case "liquidations":
		return marginLiquidations(ctx, store, args[1:], out)
	default:
		return fmt.Errorf("unknown margin command %q", args[0])
	}
}

func marginApply(ctx context.Context, store ports.Store, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("margin apply", flag.ContinueOnError)
	fs.SetOutput(out)
	file := fs.String("file", "", `the terms as in deploy/instruments/margin.json ("-" reads stdin)`)
	force := fs.Bool("force", false, "also change the terms the admin console changed last (kept otherwise)")
	dry := fs.Bool("dry-run", false, "list the changes without making them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		fs.Usage()
		return errors.New("--file is required")
	}
	src := in
	if *file != "-" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	tf, err := application.ReadTermsFile(src)
	if err != nil {
		return err
	}
	seed, err := tf.Terms()
	if err != nil {
		return err
	}
	res, err := application.ApplyTerms(ctx, store, seed, application.ApplyOptions{DryRun: *dry, Force: *force})
	if err != nil {
		return err
	}
	verb := "changed"
	if *dry {
		verb = "would change"
	}
	for _, c := range res.Changed {
		fmt.Fprintln(out, verb, c)
	}
	for _, k := range res.Kept {
		fmt.Fprintln(out, "kept", k)
	}
	fmt.Fprintf(out, "%d %s, %d kept (the console's), %d unchanged\n", len(res.Changed), verb, len(res.Kept), res.Unchanged)
	return nil
}

func marginTerms(ctx context.Context, store ports.Store, out io.Writer) error {
	r := store.Read().Terms()
	cross, ok, err := r.Cross(ctx)
	if err != nil {
		return err
	}
	if !ok {
		cross = domain.DefaultTerms(domain.AccountCross, 3)
	}
	fmt.Fprintf(out, "cross: %dx, warn %s, liquidate %s, fee %s\n\n", cross.Leverage, cross.WarnLevel, cross.LiquidationLevel,
		cross.LiquidationFee)
	assets, err := r.Assets(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ASSET\tBORROW\tCOLLATERAL\tHAIRCUT\tPOOL\tUSER CAP\tMODEL\tFIXED/H")
	for _, a := range assets {
		fmt.Fprintf(w, "%s\t%v\t%v\t%s\t%s\t%s\t%s\t%s\n", a.Asset, a.Borrowable, a.Collateral, a.Haircut, a.PoolCap, a.UserCap, a.Model, a.FixedRate)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	pairs, err := r.Pairs(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PAIR\tISOLATED\tLEVERAGE\tWARN\tLIQUIDATE\tFEE")
	for _, p := range pairs {
		fmt.Fprintf(w, "%s\t%v\t%dx\t%s\t%s\t%s\n", p.Symbol, p.Isolated, p.Terms.Leverage, p.Terms.WarnLevel, p.Terms.LiquidationLevel,
			p.Terms.LiquidationFee)
	}
	return w.Flush()
}

// marginLiquidate asks the running margin-service (in its container) to
// liquidate an account now, as an approved request of the administrators
// does: the approval defaults to a new ID, and the same one again returns
// the liquidation it started.
func marginLiquidate(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("margin liquidate", flag.ContinueOnError)
	fs.SetOutput(out)
	user := fs.String("user", "", "the user's ID")
	account := fs.String("account", "", "MARGIN_CROSS, or MARGIN_ISOLATED:<symbol>")
	approval := fs.String("approval", "", "the approval's ID (default: a new one)")
	base := fs.String("url", "http://localhost:8099", "margin-service's HTTP address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" || *account == "" {
		fs.Usage()
		return errors.New("--user and --account are required")
	}
	if *approval == "" {
		*approval = uuid.Must(uuid.NewV7()).String()
	}
	body, _ := json.Marshal(map[string]string{"approval_id": *approval})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/internal/margin/accounts/%s/%s/liquidate", strings.TrimSuffix(*base, "/"), *user, *account), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Id", "cli:exchangectl")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	fmt.Fprintln(out, strings.TrimSpace(string(raw)))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("liquidate: HTTP %d", resp.StatusCode)
	}
	return nil
}

// marginLiquidations lists a user's liquidations, or those under way.
func marginLiquidations(ctx context.Context, store ports.Store, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("margin liquidations", flag.ContinueOnError)
	fs.SetOutput(out)
	user := fs.String("user", "", "the user's ID (default: every liquidation under way)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var list []ports.Liquidation
	var err error
	if *user == "" {
		list, err = store.Read().Liquidations().Running(ctx)
	} else {
		list, err = store.Read().Liquidations().OfUser(ctx, *user, nil, "", 50)
	}
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "LIQUIDATION\tUSER\tACCOUNT\tTRIGGER\tSTATUS\tSTEP\tTRADED\tFEE_USDT\tCOVERED_USDT\tSTARTED\tNOTE")
	for _, l := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s %s\t%s\t%s\t%s\t%s\n", l.ID, l.UserID, l.Account, l.Trigger, l.Status, l.Step, l.Traded,
			l.QuoteAsset, l.FeeUSDT, l.InsuranceCovered, l.StartedAt.UTC().Format(time.RFC3339), l.Note)
	}
	return w.Flush()
}

func marginLoans(ctx context.Context, store ports.Store, out io.Writer) error {
	loans, err := store.Read().Loans().Open(ctx, "")
	if err != nil {
		return err
	}
	lent, err := store.Read().Pools().Lent(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "USER\tACCOUNT\tASSET\tPRINCIPAL\tINTEREST")
	for _, l := range loans {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", l.UserID, l.Account, l.Asset, l.Principal, l.Interest)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out)
	for asset, v := range lent {
		fmt.Fprintf(out, "pool %s: lent %s\n", asset, v)
	}
	return nil
}

// marginReconcile checks invariant 7 now: every margin account's debt and
// interest rows in the ledger against margin-service's loans.
func marginReconcile(ctx context.Context, cfg settings, store ports.Store, out io.Writer) error {
	ledgerDB, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 1}, "ledger")
	if err != nil {
		return err
	}
	defer ledgerDB.Close()
	rows, err := ledgerDB.Query(ctx, `SELECT owner_id, account_type, scope, asset, -available FROM accounts
		WHERE account_type IN ('MARGIN_CROSS_DEBT', 'MARGIN_CROSS_INTEREST', 'MARGIN_ISOLATED_DEBT', 'MARGIN_ISOLATED_INTEREST')
			AND available <> 0`)
	if err != nil {
		return err
	}
	type owed struct{ principal, interest decimal.Decimal }
	ledger := map[ports.LoanKey]owed{}
	for rows.Next() {
		var user, accountType, scope, asset string
		var amount decimal.Decimal
		if err := rows.Scan(&user, &accountType, &scope, &asset, &amount); err != nil {
			rows.Close()
			return err
		}
		base, interest := strings.CutSuffix(accountType, "_INTEREST")
		base = strings.TrimSuffix(base, "_DEBT")
		k := ports.LoanKey{UserID: user, Account: domain.Account{Type: domain.AccountType(base), Symbol: scope}, Asset: asset}
		o := ledger[k]
		if interest {
			o.interest = amount
		} else {
			o.principal = amount
		}
		ledger[k] = o
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	loans, err := store.Read().Loans().Open(ctx, "")
	if err != nil {
		return err
	}
	broken := 0
	seen := map[ports.LoanKey]bool{}
	for _, l := range loans {
		k := ports.LoanKey{UserID: l.UserID, Account: l.Account, Asset: l.Asset}
		seen[k] = true
		o := ledger[k]
		if !l.Principal.Equal(o.principal) || !l.Interest.Equal(o.interest) {
			broken++
			fmt.Fprintf(out, "MISMATCH %s %s %s: loan %s + %s, ledger %s + %s\n", l.UserID, l.Account, l.Asset, l.Principal, l.Interest,
				o.principal, o.interest)
		}
	}
	for k, o := range ledger {
		if !seen[k] {
			broken++
			fmt.Fprintf(out, "MISMATCH %s %s %s: no loan, ledger %s + %s\n", k.UserID, k.Account, k.Asset, o.principal, o.interest)
		}
	}
	fmt.Fprintf(out, "%d loans, %d debts in the ledger, %d mismatches\n", len(loans), len(ledger), broken)
	if broken > 0 {
		return errors.New("invariant 7 broken (a write in flight shows for a moment: run it again)")
	}
	return nil
}
