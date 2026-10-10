package retention_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/platform/retention"
	"github.com/skill/exchange/internal/platform/testenv"
)

func TestWindow(t *testing.T) {
	now := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	w := retention.Window{Now: now, Days: 15, KeyDays: 90, Batch: 5000}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := w.History(); !got.Equal(time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("history cutoff %s", got)
	}
	if got := w.Keys(); !got.Equal(time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("keys cutoff %s", got)
	}
	for _, bad := range []retention.Window{
		{Days: 15, KeyDays: 90, Batch: 1},
		{Now: now, Days: 0, KeyDays: 90, Batch: 1},
		{Now: now, Days: 15, KeyDays: 7, Batch: 1},
		{Now: now, Days: 15, KeyDays: 90},
	} {
		if bad.Validate() == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

func TestEstimatedBytes(t *testing.T) {
	for _, c := range []struct {
		r    retention.Result
		want int64
	}{
		{retention.Result{Rows: 25, Total: 100, Bytes: 4000}, 1000},
		{retention.Result{Rows: 0, Total: 100, Bytes: 4000}, 0},
		{retention.Result{Rows: 120, Total: 100, Bytes: 4000}, 4000},
		{retention.Result{Rows: 5, Total: 0, Bytes: 4000}, 0},
	} {
		if got := c.r.EstimatedBytes(); got != c.want {
			t.Fatalf("%+v: %d, want %d", c.r, got, c.want)
		}
	}
}

// Apply refuses a window or a rule it cannot run before it reads anything
// (B197): no batch (a LIMIT 0 that never ends), no cutoff, no statements.
func TestApplyRefuses(t *testing.T) {
	ctx := context.Background()
	w := retention.Window{Now: time.Now(), Days: 15, KeyDays: 90, Batch: 10}
	rule := retention.Rule{Table: "notes", Name: "old", Cutoff: w.History(), Where: "created_at < $1"}
	noBatch := w
	noBatch.Batch = 0
	noCutoff := rule
	noCutoff.Cutoff = time.Time{}
	for name, c := range map[string]struct {
		w retention.Window
		r retention.Rule
	}{
		"no batch":      {noBatch, rule},
		"no cutoff":     {w, noCutoff},
		"no statements": {w, retention.Rule{Table: "notes", Name: "nothing", Cutoff: w.History()}},
	} {
		if _, err := retention.Apply(ctx, nil, c.w, c.r); err == nil {
			t.Fatalf("%s: run", name)
		}
	}
}

// Apply counts with DryRun and deletes nothing; without it, deletes in
// batches until one comes back short, leaving the newer rows.
func TestApply(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE notes (id bigint PRIMARY KEY, created_at timestamptz NOT NULL);
		INSERT INTO notes SELECT i, now() - make_interval(days => CASE WHEN i <= 23 THEN 20 ELSE 1 END)
		FROM generate_series(1, 30) i`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	w := retention.Window{Now: now, Days: 15, KeyDays: 90, Batch: 5, DryRun: true}
	rule := retention.Rule{
		Table: "notes", Name: "older than 15 days", Cutoff: w.History(),
		Count:  `SELECT count(*) FROM notes WHERE created_at < $1`,
		Delete: `DELETE FROM notes WHERE id IN (SELECT id FROM notes WHERE created_at < $1 LIMIT $2)`,
	}
	res, err := retention.Apply(ctx, db, w, rule)
	if err != nil || res.Rows != 23 || res.Total != 30 || res.Bytes <= 0 {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	var left int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&left); err != nil || left != 30 {
		t.Fatalf("the dry run deleted rows: %d left (%v)", left, err)
	}
	w.DryRun = false
	if res, err = retention.Apply(ctx, db, w, rule); err != nil || res.Rows != 23 {
		t.Fatalf("run: %+v %v", res, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&left); err != nil || left != 7 {
		t.Fatalf("%d rows left, want the 7 newer ones (%v)", left, err)
	}
	if res, err = retention.Apply(ctx, db, w, rule); err != nil || res.Rows != 0 {
		t.Fatalf("again: %+v %v", res, err)
	}

	// A rule by its condition alone: the statements are made from it.
	if _, err := db.Exec(ctx, `INSERT INTO notes SELECT i, now() - interval '30 days' FROM generate_series(101, 112) i`); err != nil {
		t.Fatal(err)
	}
	byWhere := []retention.Rule{
		{Table: "notes", Name: "older than 15 days, even ids", Cutoff: w.History(), Where: "created_at < $1 AND id % 2 = 0"},
		{Table: "notes", Name: "older than 15 days", Cutoff: w.History(), Where: "created_at < $1"},
	}
	w.DryRun = true
	all, err := retention.ApplyAll(ctx, db, w, byWhere)
	if err != nil || len(all) != 2 || all[0].Rows != 6 || all[1].Rows != 12 {
		t.Fatalf("dry run by condition: %+v %v", all, err)
	}
	w.DryRun = false
	if all, err = retention.ApplyAll(ctx, db, w, byWhere); err != nil || all[0].Rows != 6 || all[1].Rows != 6 {
		t.Fatalf("run by condition: %+v %v", all, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&left); err != nil || left != 7 {
		t.Fatalf("%d rows left, want 7 (%v)", left, err)
	}
	if _, err := retention.Apply(ctx, db, w, retention.Rule{Table: "notes", Name: "nothing", Cutoff: w.History()}); err == nil {
		t.Fatal("a rule with neither a condition nor statements was run")
	}

	// A run cut short between batches says where it stopped (B197).
	if _, err := db.Exec(ctx, `INSERT INTO notes SELECT i, now() - interval '30 days' FROM generate_series(201, 203) i`); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	w.Batch, w.Pause = 1, time.Hour
	res, err = retention.Apply(short, db, w, rule)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "notes (older than 15 days) stopped after 1 rows") || res.Rows != 1 {
		t.Fatalf("cut short: %+v %v", res, err)
	}
	// A statement that fails names its table and rule.
	bad := retention.Rule{Table: "notes", Name: "broken", Cutoff: w.History(), Where: "no_such_column < $1"}
	w.Pause = 0
	if _, err := retention.Apply(ctx, db, w, bad); err == nil || !strings.Contains(err.Error(), "notes (broken)") {
		t.Fatalf("a failing statement: %v", err)
	}
}
