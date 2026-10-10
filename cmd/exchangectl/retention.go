package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"

	adminpg "github.com/skill/exchange/internal/admin/adapters/postgres"
	authpg "github.com/skill/exchange/internal/auth/adapters/postgres"
	derivativespg "github.com/skill/exchange/internal/derivatives/adapters/postgres"
	instrumentpg "github.com/skill/exchange/internal/instrument/adapters/postgres"
	ledgerpg "github.com/skill/exchange/internal/ledger/adapters/postgres"
	marginpg "github.com/skill/exchange/internal/margin/adapters/postgres"
	marketpg "github.com/skill/exchange/internal/marketdata/adapters/postgres"
	makerpg "github.com/skill/exchange/internal/marketmaker/adapters/postgres"
	simpg "github.com/skill/exchange/internal/marketsim/adapters/postgres"
	notifypg "github.com/skill/exchange/internal/notification/adapters/postgres"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
	riskpg "github.com/skill/exchange/internal/risk/adapters/postgres"
	signerpg "github.com/skill/exchange/internal/signer/adapters/postgres"
	tradingpg "github.com/skill/exchange/internal/trading/adapters/postgres"
	userpg "github.com/skill/exchange/internal/user/adapters/postgres"
	walletpg "github.com/skill/exchange/internal/wallet/adapters/postgres"
)

// retentionPolicies are every schema's (M1), in the order they run.
func retentionPolicies() []retention.Policy {
	return []retention.Policy{
		ledgerpg.Retention{},
		tradingpg.Retention{},
		authpg.Retention{},
		userpg.Retention{},
		notifypg.Retention{},
		walletpg.Retention{},
		adminpg.Retention{},
		riskpg.Retention{},
		instrumentpg.Retention{},
		flags.Retention{},
		simpg.Retention{},
		makerpg.Retention{},
		signerpg.Retention{},
		derivativespg.Retention{},
		marginpg.Retention{},
		marketpg.Retention{},
	}
}

const retentionUsage = `usage: exchangectl retention run [--dry-run] [--days 15] [--key-days 90] [--batch 5000] [--pause 100ms] [--only SCHEMA,...]`

func retentionCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "run" {
		return fmt.Errorf("%s", retentionUsage)
	}
	fs := flag.NewFlagSet("retention run", flag.ContinueOnError)
	fs.SetOutput(out)
	dry := fs.Bool("dry-run", false, "count what would be deleted and delete nothing")
	days := fs.Int("days", retention.DefaultDays, "days of history kept")
	keyDays := fs.Int("key-days", retention.DefaultKeyDays, "days idempotency keys are kept (at least --days)")
	batch := fs.Int("batch", retention.DefaultBatch, "rows deleted per statement")
	pause := fs.Duration("pause", 100*time.Millisecond, "pause between batches")
	only := fs.String("only", "", "only these schemas (comma-separated)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	w := retention.Window{Now: time.Now().UTC(), Days: *days, KeyDays: *keyDays, DryRun: *dry, Batch: *batch, Pause: *pause}
	policies, err := pickPolicies(retentionPolicies(), *only)
	if err != nil {
		return err
	}
	open := func(schema string) (*pg.DB, func(), error) {
		db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, schema)
		if err != nil {
			return nil, nil, err
		}
		return db, db.Close, nil
	}
	return runRetention(ctx, policies, open, w, out)
}

// pickPolicies keeps the policies of the schemas named, all without names.
func pickPolicies(all []retention.Policy, only string) ([]retention.Policy, error) {
	if strings.TrimSpace(only) == "" {
		return all, nil
	}
	known := make([]string, 0, len(all))
	for _, p := range all {
		known = append(known, p.Schema())
	}
	var out []retention.Policy
	for _, name := range strings.Split(only, ",") {
		name = strings.TrimSpace(name)
		i := slices.Index(known, name)
		if i < 0 {
			return nil, fmt.Errorf("no retention policy for schema %q (known: %s)", name, strings.Join(known, ", "))
		}
		out = append(out, all[i])
	}
	return out, nil
}

// runRetention runs the policies within w, one schema at a time, and
// prints each rule's rows and the share of its table they take; after a
// run that deleted rows the tables are analyzed again.
func runRetention(ctx context.Context, policies []retention.Policy, open func(schema string) (*pg.DB, func(), error),
	w retention.Window, out io.Writer,
) error {
	if err := w.Validate(); err != nil {
		return err
	}
	what := "deleting"
	if w.DryRun {
		what = "dry run, nothing deleted"
	}
	fmt.Fprintf(out, "retention (%s): history before %s (%d days), keys before %s (%d days)\n", what,
		w.History().UTC().Format(time.RFC3339), w.Days, w.Keys().UTC().Format(time.RFC3339), w.KeyDays)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TABLE\tROWS\t~SIZE\tTABLE SIZE\tRULE")
	var rows, bytes int64
	for _, p := range policies {
		results, err := runPolicy(ctx, p, open, w)
		for _, r := range results {
			fmt.Fprintf(tw, "%s.%s\t%d\t%s\t%s\t%s\n", p.Schema(), r.Table, r.Rows, humanBytes(r.EstimatedBytes()), humanBytes(r.Bytes), r.Rule)
			rows += r.Rows
			bytes += r.EstimatedBytes()
		}
		if err != nil {
			_ = tw.Flush()
			return fmt.Errorf("%s: %w", p.Schema(), err)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	verb := "deleted"
	if w.DryRun {
		verb = "would delete"
	}
	// The size is the rows' share of each table's, an estimate; the space
	// goes back to the tables (autovacuum reuses it), to the disk only with
	// VACUUM FULL.
	fmt.Fprintf(out, "%s %d rows, about %s of the tables' size (an estimate; reused by the tables, returned to the disk only by VACUUM FULL)\n",
		verb, rows, humanBytes(bytes))
	return nil
}

func runPolicy(ctx context.Context, p retention.Policy, open func(schema string) (*pg.DB, func(), error), w retention.Window) ([]retention.Result, error) {
	db, done, err := open(p.Schema())
	if err != nil {
		return nil, err
	}
	defer done()
	results, err := p.Run(ctx, db, w)
	if err != nil || w.DryRun {
		return results, err
	}
	// The planner's statistics after a large delete.
	for _, r := range results {
		if r.Rows == 0 {
			continue
		}
		if _, err := db.Exec(ctx, "ANALYZE "+pgx.Identifier{r.Table}.Sanitize()); err != nil {
			return results, fmt.Errorf("analyze %s: %w", r.Table, err)
		}
	}
	return results, nil
}

// humanBytes prints a size in B, KB, MB or GB (1024-based).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 2; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMG"[exp])
}
