package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	instrumentpg "github.com/lidp280504357/exchange/internal/instrument/adapters/postgres"
	instrumentapp "github.com/lidp280504357/exchange/internal/instrument/application"
	"github.com/lidp280504357/exchange/internal/ledger/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/ledger/application"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/migrations"
)

// ledgerDBs are the schemas the ledger commands read: the ledger itself,
// instrument (asset precision) and config (feature flags).
type ledgerDBs struct {
	ledger, instrument, config *pg.DB
}

func ledgerCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	var dbs ledgerDBs
	for schema, dst := range map[string]**pg.DB{"ledger": &dbs.ledger, "instrument": &dbs.instrument, "config": &dbs.config} {
		db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, schema)
		if err != nil {
			return err
		}
		defer db.Close()
		*dst = db
	}
	return ledgerWith(ctx, dbs, args, out)
}

// instrumentAssets reads asset precision straight from the instrument
// schema, as the operator CLI cannot rely on reaching instrument-service.
type instrumentAssets struct{ svc *instrumentapp.Service }

func (a instrumentAssets) Decimals(ctx context.Context, asset string) (int32, error) {
	v, err := a.svc.Asset(ctx, asset)
	return v.Decimals, err
}

type staticFlags map[string]flags.Flag

func (f staticFlags) Enabled(key string, s flags.Subject) bool {
	fl, ok := f[key]
	return ok && fl.Allows(s)
}

func ledgerWith(ctx context.Context, dbs ledgerDBs, args []string, out io.Writer) error {
	for _, db := range []*pg.DB{dbs.ledger, dbs.config, dbs.instrument} {
		if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
			return err
		}
	}
	if err := migrate.Up(ctx, dbs.instrument, migrations.Instrument(), quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, dbs.ledger, migrations.Ledger(), quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, dbs.config, migrations.Config(), quiet); err != nil {
		return err
	}
	current, err := flags.Load(ctx, dbs.config)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	store := postgres.NewStore(dbs.ledger, event.NewFactory("exchangectl", host))
	svc := &application.Service{
		Store:  store,
		Assets: instrumentAssets{svc: &instrumentapp.Service{Store: instrumentpg.NewStore(dbs.instrument, nil)}},
		Flags:  staticFlags(current),
		Now:    time.Now,
	}
	switch args[0] {
	case "adjust":
		return ledgerAdjust(ctx, svc, args[1:], out)
	case "balances":
		if len(args) != 2 {
			return errors.New("ledger balances needs a user ID")
		}
		return ledgerBalances(ctx, svc, args[1], out)
	case "reconcile":
		return ledgerReconcile(ctx, store, out)
	case "trades":
		return ledgerTrades(ctx, svc, args[1:], out)
	case "retry-trades":
		return ledgerRetryTrades(ctx, svc, args[1:], out)
	case "system":
		asset := "USDT"
		if len(args) > 1 {
			asset = strings.ToUpper(args[1])
		}
		return ledgerSystem(ctx, svc, asset, out)
	case "insurance-fund":
		return ledgerInsuranceFund(ctx, svc, args[1:], out)
	default:
		return fmt.Errorf("unknown ledger command %q", args[0])
	}
}

func ledgerAdjust(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger adjust", flag.ContinueOnError)
	fs.SetOutput(out)
	user := fs.String("user", "", "user ID")
	house := fs.Bool("house", false, "credit HOUSE's inventory (the MARKET_MAKER system account, ADR-0013) instead of a user")
	asset := fs.String("asset", "", "asset code, e.g. USDT")
	amount := fs.String("amount", "", "decimal amount; negative debits")
	reason := fs.String("reason", "", "why (required, goes to the audit log)")
	key := fs.String("key", "", "idempotency key; repeat it to retry safely (default: a new one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*user == "") == !*house || *asset == "" || *amount == "" || *reason == "" {
		fs.Usage()
		return errors.New("--user or --house, --asset, --amount and --reason are required")
	}
	if !svc.Flags.Enabled(flags.KeyManualAdjustment, flags.Subject{UserID: *user}) {
		return fmt.Errorf("manual adjustments are off; turn on %s with exchangectl flags set", flags.KeyManualAdjustment)
	}
	d, err := decimal.NewFromString(*amount)
	if err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	if *key == "" {
		*key = uuid.NewString()
	}
	var res application.Result
	if *house {
		res, err = svc.AdjustHouse(ctx, *key, *asset, d, actor(), *reason)
	} else {
		res, err = svc.Adjust(ctx, *key, *user, *asset, d, actor(), *reason)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "journal %s (key %s, replayed %v)\n", res.JournalID, *key, res.Replayed)
	return nil
}

func ledgerBalances(ctx context.Context, svc *application.Service, userID string, out io.Writer) error {
	list, err := svc.Balances(ctx, userID, "")
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ACCOUNT\tASSET\tAVAILABLE\tFROZEN\tVERSION")
	for _, a := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", a.Key.Type, a.Key.Asset, a.Available, a.Frozen, a.Version)
	}
	return w.Flush()
}

func ledgerReconcile(ctx context.Context, store *postgres.Store, out io.Writer) error {
	results, err := store.Reconcile(ctx, time.Now)
	if err != nil {
		return err
	}
	broken := 0
	for _, r := range results {
		fmt.Fprintf(out, "%-28s %d mismatches\n", r.Check, len(r.Mismatches))
		for _, m := range r.Mismatches {
			fmt.Fprintf(out, "  %s: %s\n", m.Key, m.Detail)
		}
		broken += len(r.Mismatches)
	}
	if broken > 0 {
		return fmt.Errorf("%d ledger mismatches", broken)
	}
	return nil
}

func ledgerTrades(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger trades", flag.ContinueOnError)
	fs.SetOutput(out)
	failed := fs.Bool("failed", false, "only the trades parked as FAILED")
	limit := fs.Int("limit", 20, "how many, newest first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	status := ""
	if *failed {
		status = domain.TradeFailed
	}
	list, err := svc.Trades(ctx, status, *limit)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TRADE\tSYMBOL\tNO\tPRICE\tQUANTITY\tSTATUS\tATTEMPTS\tERROR")
	for _, t := range list {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%d\t%s\n", t.ID, t.Symbol, t.Number, t.Price, t.Quantity, t.Status, t.Attempts,
			strings.TrimSpace(t.ErrorCode+" "+t.Error))
	}
	return w.Flush()
}

func ledgerRetryTrades(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger retry-trades", flag.ContinueOnError)
	fs.SetOutput(out)
	limit := fs.Int("limit", 100, "how many FAILED trades, oldest first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	res, err := svc.RetryFailed(ctx, *limit)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "settled %d, still failed %d\n", res.Settled, res.Failed)
	for _, t := range res.Refused {
		fmt.Fprintf(out, "  %s: %s %s\n", t.ID, t.ErrorCode, t.Error)
	}
	return nil
}

func ledgerSystem(ctx context.Context, svc *application.Service, asset string, out io.Writer) error {
	list, err := svc.SystemBalances(ctx, asset)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ACCOUNT\tASSET\tAVAILABLE\tFROZEN")
	for _, a := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Key.Type, a.Key.Asset, a.Available, a.Frozen)
	}
	return w.Flush()
}

// ledgerInsuranceFund seeds the insurance fund with simulated funds.
func ledgerInsuranceFund(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger insurance-fund", flag.ContinueOnError)
	fs.SetOutput(out)
	asset := fs.String("asset", "USDT", "asset code")
	amount := fs.String("amount", "", "decimal amount to add")
	reason := fs.String("reason", "", "why (required, goes to the audit log)")
	key := fs.String("key", "", "idempotency key; repeat it to retry safely (default: a new one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *amount == "" || *reason == "" {
		fs.Usage()
		return errors.New("--amount and --reason are required")
	}
	d, err := decimal.NewFromString(*amount)
	if err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	if *key == "" {
		*key = uuid.NewString()
	}
	res, err := svc.FundInsurance(ctx, *key, strings.ToUpper(*asset), d, actor(), *reason)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "journal %s (key %s, replayed %v)\n", res.JournalID, *key, res.Replayed)
	return nil
}
