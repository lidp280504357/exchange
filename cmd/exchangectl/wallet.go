package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/wallet/application"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/migrations"
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
	return walletWith(ctx, db, args, out)
}

func walletWith(ctx context.Context, db *pg.DB, args []string, out io.Writer) error {
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, db, migrations.Wallet(), quiet); err != nil {
		return err
	}
	host, _ := os.Hostname()
	store := postgres.NewStore(db, event.NewFactory("exchangectl", host))
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

func printChecks(ctx context.Context, store *postgres.Store, network string, out io.Writer) error {
	list, err := store.Read().Checks().Latest(ctx, network)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "no chain check yet")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CHECKED\tASSET\tHELD\tELSEWHERE\tIN FLIGHT\tEXPECTED\tUNBOOKED FEES\tSHORTFALL\tADDRESSES")
	for _, c := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n", c.CheckedAt.UTC().Format(time.RFC3339), c.Asset, c.Chain, c.Elsewhere,
			c.InFlight, c.Ledger, c.Unbooked, c.Shortfall, c.Addresses)
	}
	return w.Flush()
}
