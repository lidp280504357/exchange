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
// instrument (asset precision) and config (feature flags); open opens
// another for the command that needs it (release-hold reads trading and
// wallet).
type ledgerDBs struct {
	ledger, instrument, config *pg.DB
	open                       func(schema string) (*pg.DB, error)
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
	var extra []*pg.DB
	defer func() {
		for _, db := range extra {
			db.Close()
		}
	}()
	dbs.open = func(schema string) (*pg.DB, error) {
		db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 1}, schema)
		if err == nil {
			extra = append(extra, db)
		}
		return db, err
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
	case "house-margin":
		return ledgerHouseMargin(ctx, svc, args[1:], out)
	case "gas-supply":
		return ledgerGasSupply(ctx, svc, args[1:], out)
	case "release-hold":
		return ledgerReleaseHold(ctx, svc, dbs, args[1:], out)
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

// ledgerReleaseHold releases a console hold that the console cannot: part
// of its frozen amount was released elsewhere, so the full release fails
// with LEDGER_INSUFFICIENT_BALANCE. The account's frozen balance is shared
// with the user's other holds, spot orders and withdrawals, so only what
// is left once theirs are counted is the hold's: that much at most is
// released (all of it unless --amount says less), audited as forced
// (C5.5 ⑧, ⑯).
func ledgerReleaseHold(ctx context.Context, svc *application.Service, dbs ledgerDBs, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger release-hold", flag.ContinueOnError)
	fs.SetOutput(out)
	id := fs.String("id", "", "the hold's ID (the console's user page lists them)")
	amount := fs.String("amount", "", "how much to release (default: what of the frozen balance is the hold's)")
	reason := fs.String("reason", "", "why (required, goes to the audit log)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *reason == "" {
		fs.Usage()
		return errors.New("--id and --reason are required")
	}
	h, acc, err := svc.Hold(ctx, *id)
	if err != nil {
		return err
	}
	if h == nil {
		return errors.New("no such hold")
	}
	holds, orders, withdrawals, err := otherFreezes(ctx, dbs, *h)
	if err != nil {
		return err
	}
	most := decimal.Min(h.Amount, decimal.Max(acc.Frozen.Sub(holds).Sub(orders).Sub(withdrawals), decimal.Zero))
	fmt.Fprintf(out, "%s %s frozen: %s; other holds %s, spot orders %s, withdrawals %s; the hold's (%s) at most %s\n",
		h.AccountType, h.Asset, acc.Frozen, holds, orders, withdrawals, h.Amount, most)
	release := most
	if *amount != "" {
		if release, err = decimal.NewFromString(*amount); err != nil {
			return fmt.Errorf("amount: %w", err)
		}
		if release.IsNegative() || release.GreaterThan(most) {
			return fmt.Errorf("--amount %s: at most %s of the frozen balance is the hold's", release, most)
		}
	}
	released, err := svc.ForceReleaseHold(ctx, *id, actor(), *reason, release)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "hold %s released: %s of %s %s returned to available (journal %q)\n", released.ID, release, h.Amount, h.Asset,
		released.ReleaseJournalID)
	return nil
}

// otherFreezes sums what else the user's frozen balance of the hold's
// asset holds: the other active holds, the spot orders' unused freezes
// (domain.Order.Unused of spot-trading: a sell its base, a market buy its
// quote, a limit buy its limit price per unit filled) and the withdrawals
// frozen and not yet settled or released.
func otherFreezes(ctx context.Context, dbs ledgerDBs, h domain.Hold) (holds, orders, withdrawals decimal.Decimal, err error) {
	if err = dbs.ledger.QueryRow(ctx, `SELECT coalesce(sum(amount), 0) FROM holds
		WHERE user_id = $1 AND account_type = $2 AND asset = $3 AND released_at IS NULL AND id <> $4`,
		h.UserID, h.AccountType, h.Asset, h.ID).Scan(&holds); err != nil {
		return holds, orders, withdrawals, fmt.Errorf("other holds: %w", err)
	}
	if h.AccountType != domain.AccountSpot {
		return holds, orders, withdrawals, nil
	}
	trading, err := dbs.open("trading")
	if err != nil {
		return holds, orders, withdrawals, err
	}
	if err = trading.QueryRow(ctx, `SELECT coalesce(sum(greatest(frozen_amount - CASE WHEN side = 'SELL' THEN filled_quantity
			WHEN type = 'MARKET' THEN filled_quote ELSE price * filled_quantity END, 0)), 0)
		FROM orders WHERE user_id = $1 AND frozen_asset = $2 AND freeze_state = 'FROZEN' AND released = false`,
		h.UserID, h.Asset).Scan(&orders); err != nil {
		return holds, orders, withdrawals, fmt.Errorf("spot orders: %w", err)
	}
	wallet, err := dbs.open("wallet")
	if err != nil {
		return holds, orders, withdrawals, err
	}
	if err = wallet.QueryRow(ctx, `SELECT coalesce(sum(amount + fee), 0) FROM withdrawals
		WHERE user_id = $1 AND asset = $2 AND freeze_journal_id IS NOT NULL AND settle_journal_id IS NULL AND unfreeze_journal_id IS NULL`,
		h.UserID, h.Asset).Scan(&withdrawals); err != nil {
		return holds, orders, withdrawals, fmt.Errorf("withdrawals: %w", err)
	}
	return holds, orders, withdrawals, nil
}

// houseOnly lets the operator move HOUSE's funds between its accounts:
// HOUSE is a system user (HOUSE_USER_ID) that cannot sign in.
type houseOnly string

func (h houseOnly) Check(_ context.Context, userID, _ string) (bool, string, error) {
	if userID != string(h) {
		return false, "OPERATOR_TRANSFER_HOUSE_ONLY", nil
	}
	return true, "", nil
}

// ledgerHouseMargin adds USDT to HOUSE's futures account (ADR-0015): on the
// contracts HOUSE is the user HOUSE_USER_ID, whose margin and losses come
// out of its FUTURES account. An audited adjustment credits its SPOT
// account, then the ledger's own transfer moves the amount to FUTURES;
// both are idempotent under the key. Run it in an app container (it reads
// HOUSE_USER_ID from apps.env).
func ledgerHouseMargin(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger house-margin", flag.ContinueOnError)
	fs.SetOutput(out)
	amount := fs.String("amount", "", "USDT to add to HOUSE's futures account")
	reason := fs.String("reason", "", "why (required, goes to the audit log)")
	key := fs.String("key", "", "idempotency key; repeat it to retry safely (default: a new one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	house := strings.TrimSpace(os.Getenv("HOUSE_USER_ID"))
	if house == "" {
		return errors.New("HOUSE_USER_ID is not set; run this in an app container")
	}
	if *amount == "" || *reason == "" {
		fs.Usage()
		return errors.New("--amount and --reason are required")
	}
	if !svc.Flags.Enabled(flags.KeyManualAdjustment, flags.Subject{UserID: house}) {
		return fmt.Errorf("manual adjustments are off; turn on %s with exchangectl flags set", flags.KeyManualAdjustment)
	}
	d, err := decimal.NewFromString(*amount)
	if err != nil || !d.IsPositive() {
		return errors.New("--amount must be a positive decimal")
	}
	if *key == "" {
		*key = uuid.NewString()
	}
	res, err := svc.Adjust(ctx, *key+":credit", house, "USDT", d, actor(), *reason)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "credited HOUSE's SPOT: journal %s (replayed %v)\n", res.JournalID, res.Replayed)
	svc.Eligibility = houseOnly(house)
	t, err := svc.Transfer(ctx, application.TransferInput{
		UserID: house, IdemKey: *key + ":move", Asset: "USDT", Amount: d, From: domain.AccountSpot, To: domain.AccountFutures,
	})
	if err != nil {
		return err
	}
	if t.Status != domain.TransferCompleted {
		return fmt.Errorf("the move to FUTURES did not complete: %s %s", t.Status, t.FailureReason)
	}
	fmt.Fprintf(out, "moved to HOUSE's FUTURES: transfer %s\n", t.ID)
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
// ledgerGasSupply sets fee revenue aside in GAS_SUPPLY, which the
// custodian's fees are booked from (ADR-0011): with a custodian there is
// no transfer of the platform's own to book (wallet fund), what users paid
// in fees is already where the custodian takes its fees from.
func ledgerGasSupply(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger gas-supply", flag.ContinueOnError)
	fs.SetOutput(out)
	asset := fs.String("asset", "USDT", "asset code")
	amount := fs.String("amount", "", "decimal amount to move from FEE_REVENUE")
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
	res, err := svc.FundGasSupply(ctx, *key, strings.ToUpper(*asset), d, actor(), *reason)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "journal %s (key %s, replayed %v)\n", res.JournalID, *key, res.Replayed)
	return nil
}

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
