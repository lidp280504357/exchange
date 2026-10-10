package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/chx"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/retention"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// v7At is a UUIDv7 made at t, as the trading service makes order IDs:
// orders_state is keyed by the time in it.
func v7At(t time.Time) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	ms := uint64(t.UnixMilli()) //nolint:gosec // after 1970
	for i := range 6 {
		id[i] = byte(ms >> (40 - 8*i)) //nolint:gosec // one byte of it, meant
	}
	return id
}

// The read models' cleanup (B199, B201): every row of an order that ended
// and last changed before the window goes from orders_state; one made
// before it and canceled lately stays, as do one still open and a new
// one. A dry run counts the rows and deletes nothing.
func TestRetentionOrdersState(t *testing.T) {
	ctx := context.Background()
	cfg := testenv.ClickHouse(t)
	ch, err := chx.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	sqlDB := chx.OpenDB(cfg)
	defer func() { _ = sqlDB.Close() }()
	if err := migrate.UpClickHouse(ctx, sqlDB, migrations.ClickHouse(), quiet); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old, recent := now.AddDate(0, 0, -20), now.AddDate(0, 0, -1)
	// order makes an order at made with its updates, the last one at last.
	order := func(made, last time.Time, statuses ...string) string {
		t.Helper()
		id, user := v7At(made), uuid.New()
		if err := ch.Exec(ctx, `INSERT INTO orders (order_id, client_order_id, user_id, symbol, side, type, time_in_force, price, quantity,
			quote_amount, frozen_asset, frozen_amount, accepted_at)
			VALUES (?, 'c', ?, 'BTC-USDT', 'BUY', 'LIMIT', 'GTC', 100, 1, NULL, 'USDT', 100, ?)`, id, user, made); err != nil {
			t.Fatal(err)
		}
		for i, status := range statuses {
			at := made
			if i == len(statuses)-1 {
				at = last
			}
			if err := ch.Exec(ctx, `INSERT INTO order_updates (order_id, user_id, symbol, sequence, status, filled_quantity, filled_quote,
				trade_id, reason, event_id, occurred_at) VALUES (?, ?, 'BTC-USDT', ?, ?, 0, 0, '', '', ?, ?)`,
				id, user, int64(i), status, uuid.New(), at); err != nil {
				t.Fatal(err)
			}
		}
		return id.String()
	}
	gone := order(old, old, "NEW", "FILLED")
	late := order(old, recent, "NEW", "CANCELED")
	open := order(old, old, "NEW")
	fresh := order(recent, recent, "NEW", "FILLED")
	// One row per order, so the dry run's count does not hang on merges.
	if err := ch.Exec(ctx, `OPTIMIZE TABLE orders_state FINAL`); err != nil {
		t.Fatal(err)
	}

	w := retention.Window{Now: now, Days: 15, KeyDays: 90, DryRun: true, Batch: 100}
	res, err := ordersState(ctx, ch, w)
	if err != nil || res.Rows != 1 || res.Total != 4 {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	w.DryRun = false
	if res, err = ordersState(ctx, ch, w); err != nil || res.Rows != 1 {
		t.Fatalf("run: %+v %v", res, err)
	}
	rows, err := ch.Query(ctx, `SELECT toString(order_id) FROM orders_current ORDER BY order_id`)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		left = append(left, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(left, " ")
	if len(left) != 3 || strings.Contains(got, gone) || !strings.Contains(got, late) || !strings.Contains(got, open) || !strings.Contains(got, fresh) {
		t.Fatalf("orders left %v; gone %s", left, gone)
	}
	if res, err = ordersState(ctx, ch, w); err != nil || res.Rows != 0 {
		t.Fatalf("a second run: %+v %v", res, err)
	}
}
