package postgres_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/margin/adapters/postgres"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/retention"
	"github.com/skill/exchange/internal/platform/testenv"
)

// The history policy (M1): the interest charged, the hourly rates and
// runs, the reconciliation runs and the completed liquidations go once
// older than the window; borrows, repayments, transfers and order
// reservations are the records of their keys and stay 90 days; the
// accounts, loans and pools, what waits for the ledger, a liquidation
// under way and the latest interest run done stay whatever their age.
func TestRetention(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, os.DirFS("../../../../migrations/margin"), log); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	old, recent, ancient := now.AddDate(0, 0, -20), now.Add(-time.Hour), now.AddDate(0, 0, -100)
	user := uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO accounts (user_id, account_type) VALUES ($1, 'MARGIN_CROSS')`, user)
	exec(`INSERT INTO loans (user_id, account_type, asset, principal, interest, updated_at) VALUES ($1, 'MARGIN_CROSS', 'USDT', 10, 0, $2)`, user, ancient)
	exec(`INSERT INTO pools (asset, lent) VALUES ('USDT', 10)`)
	for _, c := range []struct {
		status string
		at     time.Time
	}{{"DONE", old}, {"PENDING", old.Add(time.Hour)}, {"DONE", recent}} {
		exec(`INSERT INTO interest_charges (interest_id, user_id, account_type, asset, hour, principal, interest_model, hourly_rate,
			interest, status, created_at) VALUES ($1, $2, 'MARGIN_CROSS', 'USDT', $3, 10, 'FIXED', 0.00001, 0.0001, $4, $3)`,
			uuid.NewString(), user, c.at, c.status)
	}
	exec(`INSERT INTO hourly_rates (asset, hour, interest_model, rate, lent, pool_cap) VALUES ('USDT', $1, 'FIXED', 0.00001, 0, 1),
		('USDT', $2, 'FIXED', 0.00001, 0, 1)`, old, recent)
	// The latest run done is old (an outage): it stays, the next starts
	// from it; a run under way stays too.
	exec(`INSERT INTO interest_runs (hour, status) VALUES ($1, 'DONE'), ($2, 'DONE'), ($3, 'RUNNING')`,
		old.Add(-time.Hour), old, old.Add(time.Hour))
	exec(`INSERT INTO reconciliation_runs (started_at, check_name, mismatches) VALUES ($1, 'X', 0), ($2, 'X', 0)`, old, recent)
	done, running := uuid.NewString(), uuid.NewString()
	for _, l := range []struct {
		id, status string
		completed  any
	}{{done, "COMPLETED", old}, {running, "STARTED", nil}} {
		exec(`INSERT INTO liquidations (liquidation_id, user_id, account_type, trigger, status, step, prior_status, total_asset,
			total_liability, fee_rate, quote_asset, started_at, step_at, completed_at) VALUES ($1, $2, 'MARGIN_CROSS', 'AUTO', $3, 'DONE',
			'NORMAL', 1, 1, 0.02, 'USDT', $4, $4, $5)`, l.id, user, l.status, old, l.completed)
		exec(`INSERT INTO liquidation_orders (liquidation_id, symbol, side, status, created_at, updated_at)
			VALUES ($1, 'BTC-USDT', 'SELL', 'DONE', $2, $2)`, l.id, old)
	}
	for _, at := range []time.Time{ancient, old} {
		exec(`INSERT INTO borrows (borrow_id, user_id, account_type, asset, amount, interest_model, hourly_rate, first_interest, idem_key,
			request_hash, status, created_at) VALUES ($1, $2, 'MARGIN_CROSS', 'USDT', 10, 'FIXED', 0.00001, 0, $4, '\x00', 'DONE', $3)`,
			uuid.NewString(), user, at, uuid.NewString())
		exec(`INSERT INTO repays (repay_id, user_id, account_type, asset, interest_repaid, principal_repaid, reason, idem_key, request_hash,
			status, created_at) VALUES ($1, $2, 'MARGIN_CROSS', 'USDT', 0, 1, 'USER', $4, '\x00', 'DONE', $3)`,
			uuid.NewString(), user, at, uuid.NewString())
		exec(`INSERT INTO transfers (transfer_id, user_id, direction, account_type, asset, amount, idem_key, request_hash, status, created_at)
			VALUES ($1, $2, 'IN', 'MARGIN_CROSS', 'USDT', 1, $4, '\x00', 'DONE', $3)`, uuid.NewString(), user, at, uuid.NewString())
		exec(`INSERT INTO order_reservations (order_id, user_id, account_type, symbol, side_effect, created_at)
			VALUES ($1, $2, 'MARGIN_CROSS', 'BTC-USDT', 'NONE', $3)`, uuid.NewString(), user, at)
	}
	// A borrow still waiting on the ledger stays whatever its age.
	exec(`INSERT INTO borrows (borrow_id, user_id, account_type, asset, amount, interest_model, hourly_rate, first_interest, idem_key,
		request_hash, status, created_at) VALUES ($1, $2, 'MARGIN_CROSS', 'USDT', 10, 'FIXED', 0.00001, 0, $4, '\x00', 'PENDING', $3)`,
		uuid.NewString(), user, ancient, uuid.NewString())

	want := map[string]int64{
		"interest_charges": 1, "hourly_rates": 1, "interest_runs": 1, "reconciliation_runs": 1, "liquidation_orders": 1,
		"liquidations": 1, "borrows": 1, "repays": 1, "transfers": 1, "order_reservations": 1,
	}
	check := func(res []retention.Result, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, r := range res {
			seen[r.Table] = true
			if r.Rows != want[r.Table] {
				t.Errorf("%s (%s): %d rows, want %d", r.Table, r.Rule, r.Rows, want[r.Table])
			}
		}
		for _, table := range []string{"interest_charges", "borrows", "accounts", "loans", "pools"} {
			if !seen[table] {
				t.Errorf("%s not reported", table)
			}
		}
	}
	w := retention.Window{Now: now, Days: 15, KeyDays: 90, Batch: 1, DryRun: true}
	check(postgres.Retention{}.Run(ctx, db, w))
	w.DryRun = false
	check(postgres.Retention{}.Run(ctx, db, w))
	for table, left := range map[string]int64{
		"interest_charges": 2, "hourly_rates": 1, "interest_runs": 2, "reconciliation_runs": 1, "liquidations": 1, "liquidation_orders": 1,
		"borrows": 2, "repays": 1, "transfers": 1, "order_reservations": 1, "accounts": 1, "loans": 1, "pools": 1,
	} {
		var n int64
		if err := db.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != left {
			t.Errorf("%s: %d left, want %d (%v)", table, n, left, err)
		}
	}
	var latest time.Time
	if err := db.QueryRow(ctx, `SELECT max(hour) FROM interest_runs WHERE status = 'DONE'`).Scan(&latest); err != nil || !latest.Equal(old) {
		t.Errorf("the latest run done %v %v, want %v", latest, err, old)
	}
	var status string
	if err := db.QueryRow(ctx, `SELECT status FROM liquidations`).Scan(&status); err != nil || status != "STARTED" {
		t.Errorf("the liquidation left %q %v", status, err)
	}
	want = map[string]int64{}
	check(postgres.Retention{}.Run(ctx, db, w))
}
