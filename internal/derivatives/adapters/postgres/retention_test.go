package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/derivatives/adapters/postgres"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func order(user string, status domain.Status, freeze domain.FreezeState, released bool, filled, consumed string, at time.Time) domain.Order {
	id := uuid.Must(uuid.NewV7()).String()
	return domain.Order{
		ID: id, ClientOrderID: id, UserID: user, Symbol: "BTC-USDT-PERP", Side: domain.Buy, PositionSide: domain.SideBoth,
		Type: domain.Limit, TimeInForce: domain.GTC, Price: d("60000"), Qty: d("1"), Kind: domain.KindUser, Leverage: 10,
		MarginMode: domain.Cross, MakerFee: d("0"), TakerFee: d("0"), LotSize: d("0.001"), MarginPerLot: d("0"), FeePerLot: d("0"),
		Consumed: d(consumed), Released: released, Status: status, FreezeState: freeze, Filled: d(filled), FilledQuote: d("0"),
		Fee: d("0"), RealizedPnL: d("0"), CreatedAt: at, UpdatedAt: at,
	}
}

func fill(o domain.Order, side domain.Side, settled bool, at time.Time) domain.Fill {
	return domain.Fill{
		TradeID: uuid.Must(uuid.NewV7()).String(), OrderID: o.ID, UserID: o.UserID, Symbol: o.Symbol, Side: side,
		PositionSide: domain.SideBoth, Price: d("60000"), Qty: d("1"), ClosedQty: d("0"), Fee: d("0"), FeeWaived: d("0"),
		RealizedPnL: d("0"), Insurance: d("0"), Seq: 1, ExecutedAt: at, Settled: settled,
	}
}

// count counts a table's rows.
func count(t *testing.T, db *pg.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The history policy (M1): history older than the window goes, current
// state stays whatever its age, and a deleted fill's key stays 90 days so
// a trade coming again is still told apart. A dry run counts what the run
// deletes and deletes nothing; batches of two take a table in several
// statements.
func TestRetention(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Derivatives(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("derivatives-service", "test"))
	now := time.Now().UTC().Truncate(time.Second)
	old, recent := now.AddDate(0, 0, -20), now.Add(-time.Hour)
	user := uuid.NewString()

	gone := []domain.Order{
		order(user, domain.StatusFilled, domain.FreezeDone, true, "1", "1", old),
		order(user, domain.StatusCanceled, domain.FreezeDone, true, "0", "0", old),
		order(user, domain.StatusRejected, domain.FreezePending, false, "0", "0", old), // never frozen
		order(user, domain.StatusExpired, domain.FreezeNone, false, "0", "0", old),
	}
	stays := []domain.Order{
		order(user, domain.StatusOpen, domain.FreezeDone, false, "0", "0", old),     // still working
		order(user, domain.StatusCanceled, domain.FreezeDone, false, "0", "0", old), // not released yet
		order(user, domain.StatusFilled, domain.FreezeDone, true, "1", "0.5", old),  // fills not all applied
		order(user, domain.StatusFilled, domain.FreezeDone, true, "1", "1", recent), // recent
		order(user, domain.StatusFilled, domain.FreezeDone, true, "1", "1", old),    // its fill waits on the ledger
	}
	oldFill, unsettled, recentFill := fill(gone[0], domain.Buy, true, old), fill(gone[0], domain.Sell, false, old), fill(stays[3], domain.Buy, true, recent)
	parkedFill := fill(stays[4], domain.Buy, true, old) // settled, but a pending settlement names it
	err := store.Tx(ctx, func(r ports.Repos) error {
		for _, o := range append(append([]domain.Order{}, gone...), stays...) {
			if err := r.Orders().Insert(ctx, o); err != nil {
				return err
			}
		}
		for _, f := range []domain.Fill{oldFill, unsettled, recentFill, parkedFill} {
			if err := r.Fills().Insert(ctx, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO pending_settlements (idem_key, user_id, request, trade_id, side, last_error) VALUES ($1, $2, '{}', $3, $4, 'refused')`,
		"fill:"+parkedFill.TradeID+":BUY", user, parkedFill.TradeID, "BUY")
	ancientKey := uuid.NewString()
	exec(`INSERT INTO fill_keys (trade_id, side, executed_at) VALUES ($1, 'BUY', $2), ($3, 'SELL', $4)`,
		ancientKey, now.AddDate(0, 0, -100), uuid.NewString(), now.AddDate(0, 0, -30))
	position := uuid.NewString()
	for _, r := range []struct {
		at     time.Time
		status string
	}{{old, "SETTLED"}, {old.Add(8 * time.Hour), "SKIPPED"}, {old.Add(16 * time.Hour), "SNAPSHOT"}, {recent, "SETTLED"}} {
		exec(`INSERT INTO funding_rounds (symbol, funding_time, status, positions) VALUES ('BTC-USDT-PERP', $1, $2, 1)`, r.at, r.status)
		exec(`INSERT INTO funding_payments (symbol, funding_time, position_id, user_id, position_side, quantity, margin_mode)
			VALUES ('BTC-USDT-PERP', $1, $2, $3, 'BOTH', 1, 'CROSS')`, r.at, position, user)
	}
	for _, c := range []struct {
		status string
		at     time.Time
	}{{"CANCELED", old}, {"TRIGGERED", old}, {"ACTIVE", old}, {"FAILED", recent}} {
		exec(`INSERT INTO conditional_orders (conditional_id, user_id, symbol, position_side, side, kind, trigger_price, trigger_by,
			order_type, status, created_at, updated_at) VALUES ($1, $2, 'BTC-USDT-PERP', 'BOTH', 'SELL', 'TAKE_PROFIT', 70000, 'MARK',
			'MARKET', $3, $4, $4)`, uuid.NewString(), user, c.status, c.at)
	}
	exec(`INSERT INTO cross_liquidations (liquidation_id, user_id, asset, started_at, equity, balance, status, fee, done_at)
		VALUES ($1, $2, 'USDT', $3, 1, 1, 'DONE', 0, $3), ($4, $2, 'BTC', $3, 1, 1, 'OPEN', NULL, NULL)`,
		uuid.NewString(), user, old, uuid.NewString())
	exec(`INSERT INTO reconciliation_runs (started_at, check_name, mismatches) VALUES ($1, 'X', 0), ($2, 'X', 0)`, old, recent)
	exec(`INSERT INTO positions (position_id, user_id, symbol, position_side, quantity, entry_cost, margin, margin_mode, leverage,
		updated_at) VALUES ($1, $2, 'BTC-USDT-PERP', 'BOTH', 0, 0, 0, 'CROSS', 10, $3)`, position, user, old)

	want := map[string]int64{
		"orders": 4, "fills": 1, "fill_keys": 1, "funding_payments": 2, "funding_rounds": 2, "conditional_orders": 2,
		"cross_liquidations": 1, "reconciliation_runs": 1,
	}
	w := retention.Window{Now: now, Days: 15, KeyDays: 90, Batch: 2, DryRun: true}
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
			if r.Bytes <= 0 {
				t.Errorf("%s: no size", r.Table)
			}
		}
		for _, table := range []string{"orders", "fills", "positions", "pending_settlements", "settings"} {
			if !seen[table] {
				t.Errorf("%s not reported", table)
			}
		}
	}
	check(postgres.Retention{}.Run(ctx, db, w))
	if n := count(t, db, "orders"); n != 9 {
		t.Fatalf("the dry run deleted orders: %d left", n)
	}
	w.DryRun = false
	check(postgres.Retention{}.Run(ctx, db, w))

	for _, o := range gone {
		if _, err := store.Read().Orders().Get(ctx, o.ID); err == nil {
			t.Errorf("order %s (%s) kept", o.ID, o.Status)
		}
	}
	for _, o := range stays {
		if _, err := store.Read().Orders().Get(ctx, o.ID); err != nil {
			t.Errorf("order %s (%s): %v", o.ID, o.Status, err)
		}
	}
	// The deleted fill's key answers for it; the others are still fills.
	for _, f := range []domain.Fill{oldFill, unsettled, recentFill, parkedFill} {
		if ok, err := store.Read().Fills().Has(ctx, f.TradeID, f.Side); err != nil || !ok {
			t.Errorf("fill %s %s: %v %v", f.TradeID, f.Side, ok, err)
		}
	}
	if ok, err := store.Read().Fills().Has(ctx, ancientKey, domain.Buy); err != nil || ok {
		t.Errorf("a key past 90 days: %v %v", ok, err)
	}
	for table, left := range map[string]int64{
		"fills": 3, "fill_keys": 2, "funding_rounds": 2, "funding_payments": 2, "conditional_orders": 2, "cross_liquidations": 1,
		"reconciliation_runs": 1, "positions": 1, "pending_settlements": 1,
	} {
		if n := count(t, db, table); n != left {
			t.Errorf("%s: %d left, want %d", table, n, left)
		}
	}
	var snapshot string
	if err := db.QueryRow(ctx, `SELECT status FROM funding_rounds WHERE funding_time < $1`, now.AddDate(0, 0, -15)).Scan(&snapshot); err != nil ||
		snapshot != "SNAPSHOT" {
		t.Errorf("the old round left %q %v", snapshot, err)
	}
	// Again: nothing more.
	want = map[string]int64{}
	check(postgres.Retention{}.Run(ctx, db, w))
}
