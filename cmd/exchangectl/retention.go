package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
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
	"github.com/skill/exchange/internal/platform/chx"
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

// minDays is the shortest window a run takes without --force (B199): a
// slip of the finger should not empty the history.
const minDays = 7

const retentionUsage = `usage: exchangectl retention run [--dry-run] [--days 15] [--key-days 90] [--batch 5000] [--pause 100ms] [--only SCHEMA,...] [--force]`

func retentionCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "run" {
		return fmt.Errorf("%s", retentionUsage)
	}
	fs := flag.NewFlagSet("retention run", flag.ContinueOnError)
	fs.SetOutput(out)
	dry := fs.Bool("dry-run", false, "count what would be deleted and delete nothing")
	days := fs.Int("days", retention.DefaultDays, fmt.Sprintf("days of history kept (at least %d without --force)", minDays))
	keyDays := fs.Int("key-days", retention.DefaultKeyDays, "days idempotency keys are kept (at least --days)")
	batch := fs.Int("batch", retention.DefaultBatch, "rows deleted per statement")
	pause := fs.Duration("pause", 100*time.Millisecond, "pause between batches")
	only := fs.String("only", "", "only these schemas (comma-separated; clickhouse for the read models)")
	force := fs.Bool("force", false, fmt.Sprintf("take fewer than %d days", minDays))
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *days < minDays && !*force {
		return fmt.Errorf("--days %d keeps less than %d days of history: add --force if that is meant", *days, minDays)
	}
	w := retention.Window{Now: time.Now().UTC(), Days: *days, KeyDays: *keyDays, DryRun: *dry, Batch: *batch, Pause: *pause}
	policies, readModels, err := pickPolicies(retentionPolicies(), *only)
	if err != nil {
		return err
	}
	open := func(schema string) (*pg.DB, func(), error) {
		db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2, AppName: "exchangectl-retention"}, schema)
		if err != nil {
			return nil, nil, err
		}
		return db, db.Close, nil
	}
	var ch driver.Conn
	if readModels && cfg.ClickHouse.Addr != "" {
		if ch, err = chx.Open(ctx, cfg.ClickHouse); err != nil {
			return fmt.Errorf("clickhouse: %w", err)
		}
		defer func() { _ = ch.Close() }()
	}
	return runRetention(ctx, policies, open, ch, w, out)
}

// pickPolicies keeps the policies of the schemas named, all without names;
// readModels says whether the ClickHouse read models are among them
// ("clickhouse", or no names).
func pickPolicies(all []retention.Policy, only string) ([]retention.Policy, bool, error) {
	if strings.TrimSpace(only) == "" {
		return all, true, nil
	}
	known := make([]string, 0, len(all))
	for _, p := range all {
		known = append(known, p.Schema())
	}
	var out []retention.Policy
	readModels := false
	for _, name := range strings.Split(only, ",") {
		name = strings.TrimSpace(name)
		if name == "clickhouse" {
			readModels = true
			continue
		}
		i := slices.Index(known, name)
		if i < 0 {
			return nil, false, fmt.Errorf("no retention policy for schema %q (known: %s, clickhouse)", name, strings.Join(known, ", "))
		}
		out = append(out, all[i])
	}
	return out, readModels, nil
}

// runRetention runs the policies within w, one schema at a time, then the
// read models' cleanup when ch is set, and prints each rule's rows and the
// share of its table they take; after a run that deleted rows the tables
// are analyzed again. A schema that fails does not stop the others: the
// failures are listed at the end and make the run fail (B199).
func runRetention(ctx context.Context, policies []retention.Policy, open func(schema string) (*pg.DB, func(), error),
	ch driver.Conn, w retention.Window, out io.Writer,
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
	var failed []error
	show := func(schema string, results []retention.Result) {
		for _, r := range results {
			fmt.Fprintf(tw, "%s.%s\t%d\t%s\t%s\t%s\n", schema, r.Table, r.Rows, humanBytes(r.EstimatedBytes()), humanBytes(r.Bytes), r.Rule)
			rows += r.Rows
			bytes += r.EstimatedBytes()
		}
	}
	for _, p := range policies {
		results, err := runPolicy(ctx, p, open, w)
		show(p.Schema(), results)
		if err != nil {
			fmt.Fprintf(tw, "%s\tFAILED\t\t\t%v\n", p.Schema(), err)
			failed = append(failed, fmt.Errorf("%s: %w", p.Schema(), err))
		}
	}
	if ch != nil {
		res, err := ordersState(ctx, ch, w)
		show("clickhouse", []retention.Result{res})
		if err != nil {
			fmt.Fprintf(tw, "clickhouse\tFAILED\t\t\t%v\n", err)
			failed = append(failed, fmt.Errorf("clickhouse: %w", err))
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
	if len(failed) > 0 {
		return fmt.Errorf("retention: %d of the schemas failed: %w", len(failed), errors.Join(failed...))
	}
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

// finishedOrders are the orders whose state the read models no longer keep:
// ended before the window, by the time of their ID (created_key).
const finishedOrders = `SELECT order_id FROM orders_current
	WHERE created_key < toDateTime64(?, 3, 'UTC') AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED')`

// ordersState deletes from ClickHouse's orders_state every row of the
// orders that ended before the window (B199): the table holds each order's
// current state, an open one's too, so it has no TTL - one by status would
// expire an order's newest row and bring an older one back - and the
// bots' orders made it grow without end. A lightweight DELETE of all of an
// order's rows at once; a dry run counts the orders.
func ordersState(ctx context.Context, ch driver.Conn, w retention.Window) (retention.Result, error) {
	res := retention.Result{Table: "orders_state", Rule: "every row of the orders ended before the window"}
	cutoff := w.History().UTC().Format("2006-01-02 15:04:05.000")
	var total, bytes uint64
	if err := ch.QueryRow(ctx, `SELECT sum(rows), sum(bytes_on_disk) FROM system.parts
		WHERE database = currentDatabase() AND table = 'orders_state' AND active`).Scan(&total, &bytes); err != nil {
		return res, fmt.Errorf("size of orders_state: %w", err)
	}
	res.Total, res.Bytes = int64(total), int64(bytes) //nolint:gosec // table sizes fit
	var n uint64
	if err := ch.QueryRow(ctx, `SELECT count() FROM (`+finishedOrders+`)`, cutoff).Scan(&n); err != nil {
		return res, fmt.Errorf("count the finished orders: %w", err)
	}
	res.Rows = int64(n) //nolint:gosec // row counts fit
	if w.DryRun || n == 0 {
		return res, nil
	}
	if err := ch.Exec(ctx, `DELETE FROM orders_state WHERE created_key < toDateTime64(?, 3, 'UTC') AND order_id IN (`+finishedOrders+`)`,
		cutoff, cutoff); err != nil {
		return res, fmt.Errorf("delete the finished orders' state: %w", err)
	}
	return res, nil
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
