package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	instrumentpg "github.com/skill/exchange/internal/instrument/adapters/postgres"
	instrumentapp "github.com/skill/exchange/internal/instrument/application"
	"github.com/skill/exchange/internal/ledger/adapters/postgres"
	"github.com/skill/exchange/internal/ledger/application"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	walletpg "github.com/skill/exchange/internal/wallet/adapters/postgres"
	walletapp "github.com/skill/exchange/internal/wallet/application"
	walletdomain "github.com/skill/exchange/internal/wallet/domain"
	"github.com/skill/exchange/migrations"
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
	case "custody-reset":
		return ledgerCustodyReset(ctx, svc, dbs, args[1:], out)
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
	force := fs.Bool("force", false, "release while the user has orders or withdrawals in flight (their freezes counted whole)")
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
	o, err := otherFreezes(ctx, dbs, *h)
	if err != nil {
		return err
	}
	most := decimal.Min(h.Amount, decimal.Max(acc.Frozen.Sub(o.holds).Sub(o.orders).Sub(o.withdrawals).Sub(o.unsettled), decimal.Zero))
	fmt.Fprintf(out, "%s %s frozen: %s; other holds %s, spot orders %s (%d), withdrawals %s (%d), trades not settled %s (%d); "+
		"the hold's (%s) at most %s\n", h.AccountType, h.Asset, acc.Frozen, o.holds, o.orders, o.inOrders, o.withdrawals, o.inWithdrawals,
		o.unsettled, o.inTrades, h.Amount, most)
	// Orders, withdrawals and trades in flight move the frozen balance
	// meanwhile (a freeze the ledger made and trading has not marked, a
	// fill the ledger has not settled): counted whole they only lower the
	// cap, and an operator says so with --force (C5.5 ⑱, ⑳).
	if (o.inOrders > 0 || o.inWithdrawals > 0 || o.inTrades > 0) && !*force {
		return fmt.Errorf("the user has %d orders, %d withdrawals and %d trades not settled in flight on %s: cancel them, let the ledger "+
			"settle the trades (exchangectl ledger trades --failed, exchangectl dlq), or --force", o.inOrders, o.inWithdrawals, o.inTrades, h.Asset)
	}
	release := most
	if *amount != "" {
		if release, err = decimal.NewFromString(*amount); err != nil {
			return fmt.Errorf("amount: %w", err)
		}
		if release.IsNegative() || release.GreaterThan(most) {
			return fmt.Errorf("--amount %s: at most %s of the frozen balance is the hold's", release, most)
		}
	}
	released, err := svc.ForceReleaseHold(ctx, *id, actor(), *reason, release, map[string]string{
		"frozen": acc.Frozen.String(), "other_holds": o.holds.String(), "orders": o.orders.String(), "withdrawals": o.withdrawals.String(),
		"trades_not_settled": o.unsettled.String(), "most": most.String(), "orders_in_flight": strconv.Itoa(o.inOrders),
		"withdrawals_in_flight": strconv.Itoa(o.inWithdrawals), "trades_in_flight": strconv.Itoa(o.inTrades),
		"forced_in_flight": strconv.FormatBool(o.inOrders > 0 || o.inWithdrawals > 0 || o.inTrades > 0),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "hold %s released: %s of %s %s returned to available (journal %q)\n", released.ID, release, h.Amount, h.Asset,
		released.ReleaseJournalID)
	return nil
}

// freezes is what else a hold's frozen balance holds: the user's other
// active holds, the spot orders' freezes and the withdrawals frozen and
// not yet settled or released, with how many orders and withdrawals.
type freezes struct {
	holds, orders, withdrawals, unsettled decimal.Decimal
	inOrders, inWithdrawals, inTrades     int
}

// settleWindow is how far back release-hold looks for the user's trades
// the ledger has not settled.
const settleWindow = 24 * time.Hour

// otherFreezes sums what else the user's frozen balance of the hold's
// asset holds. A spot order counts whole while not released, pending
// (the ledger froze it, trading has not marked it) or frozen, fills
// included: the ledger frees a fill's part only once it settles (C5.5 ⑱).
func otherFreezes(ctx context.Context, dbs ledgerDBs, h domain.Hold) (freezes, error) {
	var o freezes
	if err := dbs.ledger.QueryRow(ctx, `SELECT coalesce(sum(amount), 0) FROM holds
		WHERE user_id = $1 AND account_type = $2 AND asset = $3 AND released_at IS NULL AND id <> $4`,
		h.UserID, h.AccountType, h.Asset, h.ID).Scan(&o.holds); err != nil {
		return o, fmt.Errorf("other holds: %w", err)
	}
	if h.AccountType != domain.AccountSpot {
		return o, nil
	}
	trading, err := dbs.open("trading")
	if err != nil {
		return o, err
	}
	if err := trading.QueryRow(ctx, `SELECT coalesce(sum(frozen_amount), 0), count(*) FROM orders
		WHERE user_id = $1 AND frozen_asset = $2 AND freeze_state IN ('PENDING', 'FROZEN') AND released = false`,
		h.UserID, h.Asset).Scan(&o.orders, &o.inOrders); err != nil {
		return o, fmt.Errorf("spot orders: %w", err)
	}
	wallet, err := dbs.open("wallet")
	if err != nil {
		return o, err
	}
	if err := wallet.QueryRow(ctx, `SELECT coalesce(sum(amount + fee), 0), count(*) FROM withdrawals
		WHERE user_id = $1 AND asset = $2 AND freeze_journal_id IS NOT NULL AND settle_journal_id IS NULL AND unfreeze_journal_id IS NULL`,
		h.UserID, h.Asset).Scan(&o.withdrawals, &o.inWithdrawals); err != nil {
		return o, fmt.Errorf("withdrawals: %w", err)
	}
	return o, unsettledTrades(ctx, dbs, trading, h, &o)
}

// unsettledTrades counts the user's trades of the last settleWindow that
// spend the hold's asset (a buy's quote at its limit, a sell's base) and
// that the ledger has not settled yet: trading released their orders, the
// ledger still holds their part frozen until the trade.events consumer
// settles them (C5.5 ⑳). A trade stuck longer (in a DLQ) is not counted.
func unsettledTrades(ctx context.Context, dbs ledgerDBs, trading *pg.DB, h domain.Hold, o *freezes) error {
	// A limit buy's fill holds its limit times the quantity frozen until
	// the ledger settles it: the trade's amount, then the improvement
	// released (trade-release:<id>) when the price was better (C5.5 ㉒).
	rows, err := trading.Query(ctx, `SELECT f.trade_id::text, f.side, f.symbol, f.quantity,
		CASE WHEN f.side = 'BUY' THEN greatest(f.quote_quantity, coalesce(o.price * f.quantity, 0)) ELSE f.quote_quantity END
		FROM fills f LEFT JOIN orders o ON o.id = f.order_id
		WHERE f.user_id = $1 AND f.executed_at > $2`, h.UserID, time.Now().Add(-settleWindow))
	if err != nil {
		return fmt.Errorf("trades: %w", err)
	}
	spent := map[string]decimal.Decimal{}
	for rows.Next() {
		var id, side, symbol string
		var qty, quote decimal.Decimal
		if err := rows.Scan(&id, &side, &symbol, &qty, &quote); err != nil {
			rows.Close()
			return fmt.Errorf("trades: %w", err)
		}
		base, quoted, _ := strings.Cut(symbol, "-")
		switch {
		case side == "BUY" && quoted == h.Asset:
			spent["trade:"+id] = spent["trade:"+id].Add(quote)
		case side == "SELL" && base == h.Asset:
			spent["trade:"+id] = spent["trade:"+id].Add(qty)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("trades: %w", err)
	}
	if len(spent) == 0 {
		return nil
	}
	keys := make([]string, 0, len(spent))
	for k := range spent {
		keys = append(keys, k)
	}
	settled, err := dbs.ledger.Query(ctx, `SELECT idem_key FROM journals WHERE idem_key = ANY($1)`, keys)
	if err != nil {
		return fmt.Errorf("settled trades: %w", err)
	}
	for settled.Next() {
		var k string
		if err := settled.Scan(&k); err != nil {
			settled.Close()
			return fmt.Errorf("settled trades: %w", err)
		}
		delete(spent, k)
	}
	settled.Close()
	if err := settled.Err(); err != nil {
		return fmt.Errorf("settled trades: %w", err)
	}
	for _, v := range spent {
		o.unsettled, o.inTrades = o.unsettled.Add(v), o.inTrades+1
	}
	return nil
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

// ledgerCustodyReset takes the simulated deposits a custodian's stand-in
// reported out of what the ledger expects the custodians to hold, when the
// real gateway replaces it (decision B1 of the real gateway's
// integration): DEPOSIT_PENDING up, ADJUSTMENT down, audited; --reverse
// puts them back. The wallet keeps each journal for its custody check,
// which shows their sum on a line of its own. Repeat the --key to retry
// safely, the wallet's record too.
func ledgerCustodyReset(ctx context.Context, svc *application.Service, dbs ledgerDBs, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger custody-reset", flag.ContinueOnError)
	fs.SetOutput(out)
	provider := fs.String("provider", walletdomain.ProviderUdun, "the custodian")
	asset := fs.String("asset", "", "asset code, e.g. USDT")
	amount := fs.String("amount", "", "decimal amount: the custodian's expected holding when it was replaced")
	reverse := fs.Bool("reverse", false, "put a reset back (a rollback)")
	force := fs.Bool("force", false, "reset more than the custodian's last check found it holding (or with no check yet)")
	reason := fs.String("reason", "", "why (required, goes to the audit log)")
	key := fs.String("key", "", "idempotency key; repeat it to retry safely (default: a new one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *asset == "" || *amount == "" || *reason == "" {
		fs.Usage()
		return errors.New("--asset, --amount and --reason are required")
	}
	d, err := decimal.NewFromString(*amount)
	if err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	if *key == "" {
		*key = uuid.NewString()
	}
	code, custodian := strings.ToUpper(*asset), strings.ToUpper(*provider)
	wdb, err := dbs.open("wallet")
	if err != nil {
		return err
	}
	if err := migrate.UpPlatform(ctx, wdb, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, wdb, migrations.Wallet(), quiet); err != nil {
		return err
	}
	host, _ := os.Hostname()
	wallet := walletpg.NewStore(wdb, event.NewFactory("exchangectl", host))
	before, err := wallet.Read().Checks().Baselines(ctx, custodian)
	if err != nil {
		return err
	}
	if *reverse && d.GreaterThan(before[code]) {
		return fmt.Errorf("only %s %s was reset for %s", before[code], code, custodian)
	}
	// More than the custodian holds would quietly lower the expectation
	// below what it should hold, and the checks would miss funds missing
	// (review AH): the stand-in's holding at its last check is the amount.
	if !*reverse && !*force {
		checks, err := wallet.Read().Checks().Latest(ctx, custodian)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(checks, func(c walletdomain.ChainCheck) bool { return c.Asset == code })
		switch {
		case i < 0:
			return fmt.Errorf("no check of %s at %s yet: run exchangectl wallet reconcile --network %s first, or --force", code, custodian,
				custodian)
		case d.GreaterThan(checks[i].Chain):
			return fmt.Errorf("the check of %s found %s holding %s %s, less than %s: a reset of more hides funds missing (--force if meant)",
				checks[i].CheckedAt.UTC().Format(time.RFC3339), custodian, checks[i].Chain, code, d)
		}
	}
	res, err := svc.ResetCustody(ctx, *key, code, d, *reverse, actor(), *reason)
	if err != nil {
		return err
	}
	signed := d
	if *reverse {
		signed = d.Neg()
	}
	if err := walletapp.RecordCustodyBaseline(ctx, wallet, walletdomain.CustodyBaseline{
		JournalID: res.JournalID, Provider: custodian, Asset: code, Amount: signed, Actor: actor(), Reason: *reason, CreatedAt: time.Now(),
	}); err != nil {
		return fmt.Errorf("journal %s posted (key %s), not recorded for the custody check: run again with --key %s: %w", res.JournalID, *key,
			*key, err)
	}
	after, err := wallet.Read().Checks().Baselines(ctx, custodian)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "journal %s (key %s, replayed %v); simulated %s at no custodian: %s\n", res.JournalID, *key, res.Replayed, code,
		after[code])
	return nil
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
