package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/marketdata/adapters/postgres"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/retention"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// The history policy (M1): the platform's candles of less than a day go
// once closed before the window's cutoff (by their close: the 12h candle
// open across the cutoff stays), those of a day and longer stay; the
// settled funding periods, the reference candles (a price event's too),
// the trades, futures statistics and liquidations go once older; the
// symbols' last sequences and the halts stay.
func TestRetention(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, db, migrations.Market(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 6, 30, 0, 0, time.UTC)
	cut := now.AddDate(0, 0, -15) // 2026-10-15T06:30Z
	old, recent := now.AddDate(0, 0, -20), now.Add(-time.Hour)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	candle := func(interval string, open time.Time) {
		t.Helper()
		exec(`INSERT INTO candles (symbol, interval, open_time, open, high, low, close, volume, quote_volume, trade_count)
			VALUES ('ASTRA-USDT', $1, $2, 1, 1, 1, 1, 0, 0, 0)`, interval, open)
	}
	candle("1m", old)                                             // closed long ago: goes
	candle("1m", cut.Add(-time.Minute))                           // closes at the cutoff: goes
	candle("1m", cut)                                             // closes after it: stays
	candle("12h", time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)) // closed 10-15 00:00, before the cutoff: goes
	candle("12h", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC))  // open across the cutoff: stays
	candle("1d", old)                                             // a day and longer stay
	candle("1w", old)
	candle("1M", old)
	exec(`INSERT INTO funding_periods (symbol, funding_time, premium_sum, samples) VALUES ('BTC-USDT-PERP', $1, 0, 0)`, old.Add(8*time.Hour))
	for _, at := range []time.Time{old, recent} {
		exec(`INSERT INTO funding_periods (symbol, funding_time, premium_sum, samples, funding_rate, premium, interest_rate, mark_price,
			index_price, settled_at) VALUES ('BTC-USDT-PERP', $1, 0, 1, 0.0001, 0, 0.0001, 60000, 60000, $1)`, at)
		exec(`INSERT INTO reference_candles (source, symbol, open_time, open, high, low, close, volume, quote_volume, trade_count, overlay)
			VALUES ('BINANCE', 'BTC-USDT', $1, 1, 1, 1, 1, 0, 0, 0, false), ('BINANCE', 'ETH-USDT', $1, 1, 1, 1, 1, 0, 0, 0, true)`, at)
		exec(`INSERT INTO trades (symbol, sequence, trade_id, trade_number, price, quantity, quote_quantity, taker_side, executed_at)
			VALUES ('ASTRA-USDT', $1, $2, $1, 1, 1, 1, 'BUY', $3)`, at.Unix(), uuid.NewString(), at)
		exec(`INSERT INTO futures_stats (symbol, metric, period, ts, data) VALUES ('BTC-USDT-PERP', 'basis', '1d', $1, '{"basis": "1"}')`, at)
		exec(`INSERT INTO futures_liquidations (symbol, traded_at, position_side, price, average_price, quantity, value_usd)
			VALUES ('BTC-USDT-PERP', $1, 'LONG', 1, 1, 1, 1)`, at)
	}
	exec(`INSERT INTO symbols (symbol, last_sequence, last_price, last_trade_at) VALUES ('ASTRA-USDT', 9, 1, $1)`, old)

	want := map[string]int64{
		"candles": 3, "funding_periods": 1, "reference_candles": 2, "trades": 1, "futures_stats": 1, "futures_liquidations": 1,
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
		for _, table := range []string{"candles", "symbols", "feed_halts", "sim_halts", "sim_heartbeats"} {
			if !seen[table] {
				t.Errorf("%s not reported", table)
			}
		}
	}
	w := retention.Window{Now: now, Days: 15, KeyDays: 90, Batch: 1, DryRun: true}
	check(postgres.Retention{}.Run(ctx, db, w))
	w.DryRun = false
	check(postgres.Retention{}.Run(ctx, db, w))
	rows, err := db.Query(ctx, `SELECT interval, open_time FROM candles ORDER BY interval COLLATE "C", open_time`)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for rows.Next() {
		var interval string
		var open time.Time
		if err := rows.Scan(&interval, &open); err != nil {
			t.Fatal(err)
		}
		left = append(left, interval+" "+open.UTC().Format(time.RFC3339))
	}
	rows.Close()
	wantLeft := []string{
		"12h 2026-10-15T00:00:00Z", "1M " + old.Format(time.RFC3339), "1d " + old.Format(time.RFC3339), "1m 2026-10-15T06:30:00Z",
		"1w " + old.Format(time.RFC3339),
	}
	if len(left) != len(wantLeft) {
		t.Fatalf("candles left %v, want %v", left, wantLeft)
	}
	for i := range left {
		if left[i] != wantLeft[i] {
			t.Fatalf("candles left %v, want %v", left, wantLeft)
		}
	}
	for table, n := range map[string]int64{
		"funding_periods": 2, "reference_candles": 2, "trades": 1, "futures_stats": 1, "futures_liquidations": 1, "symbols": 1,
	} {
		var got int64
		if err := db.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != n {
			t.Errorf("%s: %d left, want %d (%v)", table, got, n, err)
		}
	}
	var unsettled int64
	if err := db.QueryRow(ctx, `SELECT count(*) FROM funding_periods WHERE settled_at IS NULL`).Scan(&unsettled); err != nil || unsettled != 1 {
		t.Errorf("the period under way: %d %v", unsettled, err)
	}
	want = map[string]int64{}
	check(postgres.Retention{}.Run(ctx, db, w))
}
