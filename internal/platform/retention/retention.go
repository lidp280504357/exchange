// Package retention is the shared shape of the data retention run (M1,
// the user 2026-10-10: Postgres keeps the last 15 days of history). Each
// service's postgres adapter exports a Policy for its own schema;
// exchangectl retention run opens every schema, runs the policies and
// prints what they deleted, or would delete with DryRun.
//
// Only history is deleted, never current state; idempotency keys outlive
// the rows they guarded (KeyDays), so a replay after the history is gone
// is still told apart from a new request.
//
// A rule deletes by ctid, a batch at a time, the rows another transaction
// holds skipped (SKIP LOCKED) rather than waited for: they go in the next
// run. Each batch scans for its rows, so a large table wants an index its
// condition can use, or rows laid down in the order they age (the table's
// insert order), which a scan meets first.
package retention

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/skill/exchange/internal/platform/pg"
)

// Defaults of a run.
const (
	DefaultDays    = 15
	DefaultKeyDays = 90
	DefaultBatch   = 5000
)

// Window is what a run keeps and how it deletes.
type Window struct {
	// Now, in UTC.
	Now time.Time
	// Days of history kept: rows older than Now - Days are deleted.
	Days int
	// Days idempotency keys are kept, longer than the history.
	KeyDays int
	// DryRun counts what would be deleted and deletes nothing.
	DryRun bool
	// Rows deleted per statement, each in a short transaction of its own,
	// so a large table is never locked for long.
	Batch int
	// Pause between batches, to leave the database to the services.
	Pause time.Duration
}

// History is the cutoff of history: rows from before it are deleted.
func (w Window) History() time.Time { return w.Now.AddDate(0, 0, -w.Days) }

// Keys is the cutoff of idempotency keys.
func (w Window) Keys() time.Time { return w.Now.AddDate(0, 0, -w.KeyDays) }

// Validate checks the window's numbers.
func (w Window) Validate() error {
	switch {
	case w.Now.IsZero():
		return fmt.Errorf("retention: the window has no time")
	case w.Days < 1:
		return fmt.Errorf("retention: days must be at least 1, got %d", w.Days)
	case w.KeyDays < w.Days:
		return fmt.Errorf("retention: keys (%d days) must outlive the history (%d days)", w.KeyDays, w.Days)
	case w.Batch < 1:
		return fmt.Errorf("retention: batch must be at least 1, got %d", w.Batch)
	}
	return nil
}

// Result is what one rule of a policy deleted, or would delete.
type Result struct {
	// Table, as the schema names it.
	Table string
	// Rule says which rows: "older than 15 days and ended", "keys older than 90 days".
	Rule string
	// Rows deleted, or that would be deleted with DryRun.
	Rows int64
	// Total is the rows of the table before the run (an estimate for large ones).
	Total int64
	// Bytes is the table's size before the run, indexes and TOAST included.
	Bytes int64
}

// EstimatedBytes is the share of the table's size that Rows take.
func (r Result) EstimatedBytes() int64 {
	if r.Total <= 0 || r.Rows <= 0 {
		return 0
	}
	if r.Rows >= r.Total {
		return r.Bytes
	}
	return int64(float64(r.Bytes) * float64(r.Rows) / float64(r.Total))
}

// Policy deletes the history of one service's schema.
type Policy interface {
	// Schema is the schema the policy's pool must be bound to.
	Schema() string
	// Run applies the policy within w and reports each rule's rows.
	Run(ctx context.Context, db *pg.DB, w Window) ([]Result, error)
}

// Rule is one deletion of rows of Table, the cutoff bound as $1.
//
// Where is the condition of the rows (e.g. "created_at < $1 AND status =
// 'DONE'"): Apply counts them for a dry run and deletes them by ctid, a
// batch at a time. Count and Delete replace the statements made from it
// (Delete must be given when there is no Where): Delete gets the batch
// size as $2 and reports the rows it deleted as its command tag - a
// DELETE, or the INSERT of a WITH d AS (DELETE ... RETURNING ...) that
// moves the rows' keys into a key table.
type Rule struct {
	Table  string
	Name   string
	Cutoff time.Time
	Where  string
	Count  string
	Delete string
}

// statements returns the rule's count and batch statements.
func (r Rule) statements() (count, del string) {
	count, del = r.Count, r.Delete
	if r.Where != "" {
		t := pgx.Identifier{r.Table}.Sanitize()
		if count == "" {
			count = "SELECT count(*) FROM " + t + " WHERE " + r.Where
		}
		if del == "" {
			del = "DELETE FROM " + t + " WHERE ctid IN (SELECT ctid FROM " + t + " WHERE " + r.Where + " LIMIT $2 FOR UPDATE SKIP LOCKED)"
		}
	}
	return count, del
}

// ApplyAll runs the rules in order, as Apply does, and stops at the first
// that fails (its partial result last).
func ApplyAll(ctx context.Context, db *pg.DB, w Window, rules []Rule) ([]Result, error) {
	out := make([]Result, 0, len(rules))
	for _, r := range rules {
		res, err := Apply(ctx, db, w, r)
		out = append(out, res)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Apply runs one rule: counts with DryRun, otherwise deletes in batches
// until a batch comes back short.
func Apply(ctx context.Context, db *pg.DB, w Window, r Rule) (Result, error) {
	res := Result{Table: r.Table, Rule: r.Name}
	if err := w.Validate(); err != nil {
		return res, err
	}
	count, del := r.statements()
	switch {
	case count == "" || del == "":
		return res, fmt.Errorf("retention: the rule %q of %s has neither a condition nor statements", r.Name, r.Table)
	case r.Cutoff.IsZero():
		return res, fmt.Errorf("retention: the rule %q of %s has no cutoff", r.Name, r.Table)
	}
	var err error
	if res.Total, res.Bytes, err = Stats(ctx, db, r.Table); err != nil {
		return res, err
	}
	if w.DryRun {
		if err := db.QueryRow(ctx, count, r.Cutoff).Scan(&res.Rows); err != nil {
			return res, fmt.Errorf("retention: count %s (%s): %w", r.Table, r.Name, err)
		}
		return res, nil
	}
	for {
		tag, err := db.Exec(ctx, del, r.Cutoff, w.Batch)
		if err != nil {
			return res, fmt.Errorf("retention: delete from %s (%s) after %d rows: %w", r.Table, r.Name, res.Rows, err)
		}
		res.Rows += tag.RowsAffected()
		if tag.RowsAffected() < int64(w.Batch) {
			return res, nil
		}
		if err := Wait(ctx, w.Pause); err != nil {
			return res, fmt.Errorf("retention: %s (%s) stopped after %d rows: %w", r.Table, r.Name, res.Rows, err)
		}
	}
}

// Wait sleeps between batches, or returns the context's error once it is
// done.
func Wait(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Stats returns a table's rows (the planner's estimate, counted when it has
// none yet) and its size with indexes and TOAST, in the pool's schema.
func Stats(ctx context.Context, db *pg.DB, table string) (rows, bytes int64, err error) {
	err = db.QueryRow(ctx, `SELECT c.reltuples::bigint, pg_total_relation_size(c.oid) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = current_schema() AND c.relname = $1`, table).Scan(&rows, &bytes)
	if err != nil {
		return 0, 0, fmt.Errorf("retention: size of %s: %w", table, err)
	}
	if rows < 0 {
		if err := db.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&rows); err != nil {
			return 0, 0, fmt.Errorf("retention: count %s: %w", table, err)
		}
	}
	return rows, bytes, nil
}
