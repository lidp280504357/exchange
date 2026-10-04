package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shopspring/decimal"

	instrumentpg "github.com/skill/exchange/internal/instrument/adapters/postgres"
	instrumentapp "github.com/skill/exchange/internal/instrument/application"
	instrumentdomain "github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/wallet/adapters/postgres"
	"github.com/skill/exchange/internal/wallet/application"
	"github.com/skill/exchange/internal/wallet/domain"
	"github.com/skill/exchange/internal/wallet/ports"
	"github.com/skill/exchange/migrations"
)

// walletCmd queues wallet operations for the network's processor in
// wallet-service (the instance holding the scanner lease), which alone
// talks to the chain and the signer, and shows their results.
func walletCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "wallet")
	if err != nil {
		return err
	}
	defer db.Close()
	// The custodian's networks and the assets' decimals, for its fees.
	idb, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 1}, "instrument")
	if err != nil {
		return err
	}
	defer idb.Close()
	return walletWith(ctx, db, idb, args, out)
}

func walletWith(ctx context.Context, db, idb *pg.DB, args []string, out io.Writer) error {
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, db, migrations.Wallet(), quiet); err != nil {
		return err
	}
	if err := migrate.UpPlatform(ctx, idb, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, idb, migrations.Instrument(), quiet); err != nil {
		return err
	}
	host, _ := os.Hostname()
	store := postgres.NewStore(db, event.NewFactory("exchangectl", host))
	instruments := &instrumentapp.Service{Store: instrumentpg.NewStore(idb, nil)}
	fs := flag.NewFlagSet("wallet "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	network := fs.String("network", "ETH-SEPOLIA", "network; UDUN for the custodian's reconcile and checks")
	queue := func(kind string, a map[string]string) error {
		c, err := application.Queue(ctx, store, *network, kind, a, actor(), time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "queued %s %s; the processor runs it within a scan interval (see: exchangectl wallet commands)\n", c.Kind, c.ID)
		return nil
	}
	switch args[0] {
	case "sweep":
		least := fs.String("min", "", "sweep addresses holding at least this much (default: the network's minimum deposit)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		a := map[string]string{}
		if *least != "" {
			a["min"] = *least
		}
		return queue(domain.CommandSweep, a)
	case "fund":
		tx := fs.String("tx", "", "the platform's transfer into the hot wallet")
		account := fs.String("account", "GAS_SUPPLY", "system account to credit: GAS_SUPPLY, INSURANCE_FUND or MARKET_MAKER")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *tx == "" {
			return fmt.Errorf("--tx is required")
		}
		return queue(domain.CommandFund, map[string]string{"tx": *tx, "account": *account})
	case "reconcile":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := queue(domain.CommandReconcile, nil); err != nil {
			return err
		}
		return printChecks(ctx, store, *network, out)
	case "checks":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return printChecks(ctx, store, *network, out)
	case "withdrawals":
		status := fs.String("status", domain.WithdrawalReview, "one status, or ALL")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		statuses := []string{*status}
		if *status == "ALL" {
			statuses = []string{
				domain.WithdrawalRequested, domain.WithdrawalReview, domain.WithdrawalApproved, domain.WithdrawalSigning,
				domain.WithdrawalBroadcast, domain.WithdrawalConfirming, domain.WithdrawalSubmitted, domain.WithdrawalConfirmed, domain.WithdrawalRejected,
				domain.WithdrawalCanceled, domain.WithdrawalFailed,
			}
		}
		list, err := store.Read().Withdrawals().ByStatus(ctx, *network, statuses...)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tUSER\tAMOUNT\tTO\tSTATUS\tRISK\tAPPROVALS\tTX")
		for _, x := range list {
			fmt.Fprintf(w, "%s\t%s\t%s %s\t%s\t%s\t%d %v\t%d/%d %v\t%s\n", x.ID, x.UserID, x.Amount, x.Asset, x.Address, x.Status,
				x.RiskScore, x.RiskReasons, len(x.Approvals), x.ApprovalsRequired, x.Approvals, x.TxHash)
		}
		return w.Flush()
	case "approve", "reject":
		if len(args) < 2 {
			return fmt.Errorf("usage: wallet %s <withdrawal_id> --reviewer NAME --reason TEXT", args[0])
		}
		reviewer := fs.String("reviewer", "", "who decides (each approval needs another reviewer)")
		reason := fs.String("reason", "", "why")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		wd, err := application.ReviewWithdrawal(ctx, store, application.Review{
			ID: args[1], Reviewer: *reviewer, Reason: *reason, Approve: args[0] == "approve",
		}, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s (%d/%d approvals)\n", wd.ID, wd.Status, len(wd.Approvals), wd.ApprovalsRequired)
		return nil
	case "custody-resolve":
		if len(args) < 2 {
			return errors.New("usage: wallet custody-resolve <withdrawal_id> (--sent --tx HASH | --failed) --reason TEXT")
		}
		sent := fs.Bool("sent", false, "the custodian sent it (give --tx)")
		failed := fs.Bool("failed", false, "the custodian did not send it: its funds are released")
		tx := fs.String("tx", "", "the transaction that sent it")
		reason := fs.String("reason", "", "how it was found out (required)")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *sent == *failed {
			return errors.New("give one of --sent and --failed")
		}
		wd, err := application.ResolveCustodyWithdrawal(ctx, store, args[1], *sent, *tx, actor(), *reason, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s (%s); the processor settles or releases it within a round\n", wd.ID, wd.Status, wd.ProviderStatus)
		return nil
	case "custody-fees":
		return printCustodyFees(ctx, store, out)
	case "custody-fee":
		if len(args) < 2 {
			return errors.New("usage: wallet custody-fee <withdrawal_id> (--book [--asset A] [--amount X] | --write-off) --reason TEXT")
		}
		book := fs.Bool("book", false, "book it from GAS_SUPPLY: as reported, or in --asset and --amount as found charged")
		writeOff := fs.Bool("write-off", false, "never book it (not taken from the coin balances, reported in another unit, or waiting for GAS_SUPPLY with nothing to fund it)")
		asset := fs.String("asset", "", "the asset the custodian took it in, when not the reported one")
		amount := fs.String("amount", "", "what the custodian took, when not the reported amount")
		reason := fs.String("reason", "", "how it was found out (required)")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *book == *writeOff {
			return errors.New("give one of --book and --write-off")
		}
		d := application.FeeResolution{WithdrawalID: args[1], Book: *book, Asset: *asset, Actor: actor(), Reason: *reason}
		if *amount != "" {
			v, err := decimal.NewFromString(*amount)
			if err != nil {
				return fmt.Errorf("amount: %w", err)
			}
			d.Amount = v
		}
		f, err := application.ResolveCustodyFee(ctx, store, custodied(instruments), d, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s %s %s; a booked fee is booked within a round\n", f.TxHash, f.Status, f.Amount, f.Asset)
		return nil
	case "custody-fee-unit":
		provider := fs.String("provider", domain.ProviderUdun, "the custodian: UDUN, or the stand-in's UDUNMOCK (ADR-0017)")
		asset := fs.String("asset", "", "the network's asset, e.g. USDT")
		unit := fs.String("unit", "", "SELF (in the asset, as documented), MAIN (in the chain's own coin) or OUTSIDE (not in the coin balances)")
		reason := fs.String("reason", "", "how it was confirmed, e.g. against the first real withdrawal on the block explorer (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *unit == "" {
			return printFeeUnits(ctx, store, out)
		}
		u := domain.FeeUnit{
			Provider: strings.ToUpper(*provider), Asset: strings.ToUpper(*asset), Network: strings.ToUpper(*network), Unit: *unit,
			ConfirmedBy: actor(), Reason: *reason, ConfirmedAt: time.Now(),
		}
		if ok, err := servedBy(ctx, instruments, u.Provider, u.Asset, u.Network); err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("%s does not serve %s on %s", u.Provider, u.Asset, u.Network)
			}
			return err
		}
		if err := application.SetCustodyFeeUnit(ctx, store, u); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s counts its fee on %s %s as %s; fees held before stay held (wallet custody-fees)\n", u.Provider, u.Asset, u.Network,
			strings.ToUpper(u.Unit))
		return nil
	case "withdrawals-suspended":
		return printSuspensions(ctx, store, out)
	case "retire-addresses", "restore-addresses":
		provider := fs.String("provider", domain.ProviderUdun, "the custodian whose deposit addresses go out of use (or come back)")
		reason := fs.String("reason", "", "why (required, goes to the audit log)")
		yes := fs.Bool("yes", false, "do it; without, only say what would be done")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		// Only the stand-in's addresses are made up: once the real gateway
		// serves the custodian its addresses are real, and retiring them
		// would leave real deposits to them unmatched (review AH). The
		// custodian's gateway is in <PROVIDER>_GATEWAY_URL.
		gateway := strings.ToUpper(*provider) + "_GATEWAY_URL"
		if gw := os.Getenv(gateway); !standIn(gw) {
			return fmt.Errorf("%s's gateway here (%s) is not the stand-in udun-mock: its deposit addresses are real, they stay "+
				"(run this in wallet-service while it still talks to the stand-in)", strings.ToUpper(*provider), gateway)
		}
		inUse, retired, err := store.Read().Addresses().OfProvider(ctx, strings.ToUpper(*provider))
		if err != nil {
			return err
		}
		if !*yes {
			if args[0] == "retire-addresses" {
				fmt.Fprintf(out, "would retire the %d deposit addresses of %s in use (%d retired before); add --yes to do it\n", inUse,
					strings.ToUpper(*provider), retired)
			} else {
				fmt.Fprintf(out, "would put back the %d retired deposit addresses of %s (%d in use now); add --yes to do it\n", retired,
					strings.ToUpper(*provider), inUse)
			}
			return nil
		}
		if args[0] == "retire-addresses" {
			n, err := application.RetireDepositAddresses(ctx, store, *provider, actor(), *reason, time.Now())
			if err != nil {
				return err
			}
			total := 0
			for network, k := range n {
				fmt.Fprintf(out, "%s: %d retired\n", network, k)
				total += k
			}
			fmt.Fprintf(out, "%d deposit addresses of %s retired: users get new ones on their next request, withdrawals to these are refused\n",
				total, strings.ToUpper(*provider))
			return nil
		}
		restored, left, err := application.RestoreDepositAddresses(ctx, store, *provider, actor(), *reason)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%d deposit addresses of %s restored; %d left retired (their user has another on the network now)\n", restored,
			strings.ToUpper(*provider), left)
		return nil
	case "withdrawals-suspend", "withdrawals-resume":
		asset := fs.String("asset", "", "the asset, e.g. USDT")
		reason := fs.String("reason", "", "why (required, goes to the audit log)")
		accept := fs.String("accept", "0", "resume: a difference you accept, which the custody checks do not count as missing for --for")
		acceptFor := fs.Duration("for", 24*time.Hour, "resume: how long the accepted difference holds (at most 168h)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if args[0] == "withdrawals-suspend" {
			x, err := application.SuspendWithdrawals(ctx, store, withdrawable(instruments), *asset, actor(), *reason, time.Now())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "withdrawals of %s suspended: new requests are refused, approved ones wait\n", x.Asset)
			return nil
		}
		amount, err := decimal.NewFromString(*accept)
		if err != nil {
			return fmt.Errorf("--accept: %w", err)
		}
		forSet := false
		fs.Visit(func(f *flag.Flag) { forSet = forSet || f.Name == "for" })
		if forSet && !amount.IsPositive() {
			return errors.New("--for is how long an accepted difference holds: give --accept too")
		}
		now := time.Now()
		x, err := application.ResumeWithdrawals(ctx, store, application.Resume{
			Asset: *asset, Actor: actor(), Reason: *reason, Accept: amount, AcceptFor: *acceptFor,
		}, now)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "withdrawals of %s resumed (suspended by %s at %s); approved ones go out within a round\n", x.Asset, x.SuspendedBy,
			x.SuspendedAt.UTC().Format(time.RFC3339))
		if amount.IsPositive() {
			fmt.Fprintf(out, "the custody checks do not count %s %s as missing until %s\n", amount, x.Asset, now.Add(*acceptFor).UTC().Format(time.RFC3339))
		}
		return nil
	case "commands":
		limit := fs.Int("limit", 20, "how many")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		list, err := store.Read().Commands().Recent(ctx, *limit)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "CREATED\tKIND\tSTATUS\tBY\tRESULT")
		for _, c := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.CreatedAt.UTC().Format(time.RFC3339), c.Kind, c.Status, c.RequestedBy, c.Result)
		}
		return w.Flush()
	default:
		fmt.Fprint(out, usage)
		return fmt.Errorf("unknown wallet command %q", args[0])
	}
}

// custodied finds the decimals of an asset the custodian holds for the
// platform on a network in the instrument schema.
func custodied(svc *instrumentapp.Service) application.Custodied {
	return func(ctx context.Context, provider, asset, network string) (int32, bool, error) {
		ok, err := servedBy(ctx, svc, provider, asset, network)
		if err != nil || !ok {
			return 0, false, err
		}
		v, err := svc.Asset(ctx, asset)
		return v.Decimals, err == nil, err
	}
}

// withdrawable reports whether an asset has a network to withdraw it on.
func withdrawable(svc *instrumentapp.Service) application.Withdrawable {
	return func(ctx context.Context, asset string) (bool, error) {
		v, err := svc.Asset(ctx, asset)
		if errors.Is(err, instrumentdomain.ErrNotFound) {
			return false, nil
		}
		return err == nil && len(v.Networks) > 0, err
	}
}

// standIn reports whether a custodian's gateway address is the test
// environment's stand-in (the udun-mock container).
func standIn(gateway string) bool {
	u, err := url.Parse(strings.TrimSpace(gateway))
	return err == nil && u.Hostname() == "udun-mock"
}

// printSuspensions lists the suspended assets, then what the custody
// checks keep: a suspicion (funds missing at a check, suspended on the
// next if still missing) or a difference a person accepted.
func printSuspensions(ctx context.Context, store *postgres.Store, out io.Writer) error {
	list, err := store.Read().Suspensions().List(ctx)
	if err != nil {
		return err
	}
	watches, err := store.Read().Suspensions().Watches(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if len(list) == 0 {
		fmt.Fprintln(w, "no asset's withdrawals are suspended")
	} else {
		fmt.Fprintln(w, "ASSET\tSINCE\tBY\tMISSING\tWHY")
		for _, x := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", x.Asset, x.SuspendedAt.UTC().Format(time.RFC3339), x.SuspendedBy, x.Shortfall, x.Reason)
		}
	}
	if len(watches) > 0 {
		fmt.Fprintln(w, "\nWATCHED\tMISSING SINCE\tACCEPTED\tUNTIL\tBY")
		stamp := func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.UTC().Format(time.RFC3339)
		}
		for _, x := range watches {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", x.Asset, stamp(x.SuspectSince), x.Accepted, stamp(x.AcceptedUntil), x.AcceptedBy)
		}
	}
	return w.Flush()
}

// servedBy reports whether the custodian serves an asset's network.
func servedBy(ctx context.Context, svc *instrumentapp.Service, provider, asset, network string) (bool, error) {
	v, err := svc.Asset(ctx, asset)
	if errors.Is(err, instrumentdomain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, n := range v.Networks {
		if n.Network == network && n.Provider == provider {
			return true, nil
		}
	}
	return false, nil
}

// printCustodyFees lists the custodian's fees held for a person (review ④).
func printCustodyFees(ctx context.Context, store *postgres.Store, out io.Writer) error {
	list, err := store.Read().ChainFees().Held(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "no custodian fee waits for a person")
		return nil
	}
	fmt.Fprintf(out, "%d custodian fees wait for a person (wallet custody-fee <withdrawal_id> --book|--write-off):\n", len(list))
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RECEIVED\tWITHDRAWAL\tNETWORK\tFEE\tWHY")
	for _, f := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s %s\t%s\n", f.CreatedAt.UTC().Format(time.RFC3339), f.Reference, f.Network, f.Amount, f.Asset, f.HoldReason)
	}
	return w.Flush()
}

// printFeeUnits lists how the custodian counts its fees, as confirmed.
func printFeeUnits(ctx context.Context, store *postgres.Store, out io.Writer) error {
	list, err := store.Read().ChainFees().Units(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "no fee unit confirmed: a chain's own coin is taken as SELF, a token's fees are held for a person")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CUSTODIAN\tASSET\tNETWORK\tUNIT\tCONFIRMED\tBY\tHOW")
	for _, u := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", u.Provider, u.Asset, u.Network, u.Unit, u.ConfirmedAt.UTC().Format(time.RFC3339),
			u.ConfirmedBy, u.Reason)
	}
	return w.Flush()
}

func printChecks(ctx context.Context, store *postgres.Store, network string, out io.Writer) error {
	list, err := store.Read().Checks().Latest(ctx, network)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "no chain check yet")
	} else {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "CHECKED\tASSET\tHELD\tELSEWHERE\tIN FLIGHT\tEXPECTED\tUNBOOKED FEES\tSHORTFALL\tADDRESSES\tSIMULATED")
		baseline := false
		for _, c := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n", c.CheckedAt.UTC().Format(time.RFC3339), c.Asset, c.Chain, c.Elsewhere,
				c.InFlight, c.Ledger, c.Unbooked, c.Shortfall, c.Addresses, c.Baseline)
			baseline = baseline || !c.Baseline.IsZero()
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if baseline {
			fmt.Fprintln(out, "SIMULATED: deposits the custodian's stand-in reported, taken out of EXPECTED when the real gateway replaced it "+
				"(ledger custody-reset): at no custodian")
		}
	}
	if network == domain.ProviderUdun || network == domain.ProviderUdunMock {
		return printBackfills(ctx, store, out)
	}
	return nil
}

// printBackfills lists the deposits administrators backfilled that no
// custodian callback has matched yet (design 2026-10-02 §4.3): the
// custodian's balance holds no such deposit if one was entered wrongly.
func printBackfills(ctx context.Context, store *postgres.Store, out io.Writer) error {
	list, err := store.Read().Deposits().Page(ctx, ports.DepositFilter{ManualPending: true, Limit: 200})
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "\nno backfilled deposit waits for its callback")
		return nil
	}
	fmt.Fprintf(out, "\n%d backfilled deposits without a custodian callback yet (each should be in the custodian's balance):\n", len(list))
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DETECTED\tID\tUSER\tAMOUNT\tNETWORK\tTRADE\tTX\tSTATUS\tBY")
	for _, d := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s %s\t%s\t%s\t%s\t%s\t%s\n", d.DetectedAt.UTC().Format(time.RFC3339), d.ID, d.UserID, d.Amount, d.Asset,
			d.Network, d.ProviderTxID, d.TxHash, d.Status, d.EnteredBy)
	}
	return w.Flush()
}
