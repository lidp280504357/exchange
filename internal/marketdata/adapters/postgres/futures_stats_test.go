package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/adapters/postgres"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func TestFuturesStats(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, db, migrations.Market(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewFuturesStats(db)
	t0 := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	oi := func(at time.Time, v string) ports.FuturesStat {
		return ports.FuturesStat{
			Symbol: "BTC-USDT-PERP", Metric: ports.MetricOpenInterest, Period: "5m", At: at,
			Values: map[string]decimal.Decimal{"open_interest": d(v), "open_interest_value": d(v).Mul(d("86000"))},
		}
	}
	if last, err := repo.Last(ctx, "BTC-USDT-PERP", ports.MetricOpenInterest, "5m"); err != nil || !last.IsZero() {
		t.Fatalf("an empty series' last point = %v, %v", last, err)
	}
	err := repo.Upsert(ctx, []ports.FuturesStat{
		oi(t0, "94000.5"), oi(t0.Add(5*time.Minute), "94100"), oi(t0.Add(10*time.Minute), "94200"),
		// Another period and another contract are other series.
		{Symbol: "BTC-USDT-PERP", Metric: ports.MetricOpenInterest, Period: "1h", At: t0, Values: map[string]decimal.Decimal{"open_interest": d("1")}},
		{Symbol: "ETH-USDT-PERP", Metric: ports.MetricOpenInterest, Period: "5m", At: t0.Add(time.Hour), Values: map[string]decimal.Decimal{"open_interest": d("2")}},
		{Symbol: "BTC-USDT-PERP", Metric: ports.MetricFunding, At: t0.Add(-2 * time.Hour), Values: map[string]decimal.Decimal{"funding_rate": d("-0.00001592")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The same point again replaces it.
	if err := repo.Upsert(ctx, []ports.FuturesStat{oi(t0.Add(10*time.Minute), "94250")}); err != nil {
		t.Fatal(err)
	}
	last, err := repo.Last(ctx, "BTC-USDT-PERP", ports.MetricOpenInterest, "5m")
	if err != nil || !last.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("last 5m point = %v, %v", last, err)
	}
	got, err := repo.Recent(ctx, "BTC-USDT-PERP", ports.MetricOpenInterest, "5m", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].At.Equal(t0.Add(5*time.Minute)) || !got[1].At.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("the latest two, oldest first: %+v", got)
	}
	if v := got[1].Values["open_interest"]; !v.Equal(d("94250")) || got[1].Values["open_interest_value"].String() != "8105500000" {
		t.Fatalf("the replaced point's values: %v", got[1].Values)
	}
	funding, err := repo.Recent(ctx, "BTC-USDT-PERP", ports.MetricFunding, "", 10)
	if err != nil || len(funding) != 1 || funding[0].Values["funding_rate"].String() != "-0.00001592" {
		t.Fatalf("funding = %+v, %v", funding, err)
	}
	// A funding rate has no period, every other metric one.
	if err := repo.Upsert(ctx, []ports.FuturesStat{{
		Symbol: "BTC-USDT-PERP", Metric: ports.MetricFunding, Period: "8h", At: t0,
		Values: map[string]decimal.Decimal{"funding_rate": d("0")},
	}}); err == nil {
		t.Fatal("a funding rate with a period was stored")
	}
	if err := repo.Upsert(ctx, []ports.FuturesStat{{
		Symbol: "BTC-USDT-PERP", Metric: ports.MetricBasis, At: t0,
		Values: map[string]decimal.Decimal{"basis": d("1")},
	}}); err == nil {
		t.Fatal("a basis point without a period was stored")
	}
	// Purging a period leaves the others.
	n, err := repo.Purge(ctx, "5m", t0.Add(6*time.Minute))
	if err != nil || n != 2 {
		t.Fatalf("purged %d, %v; want the two 5m points before 10:06", n, err)
	}
	if got, _ := repo.Recent(ctx, "BTC-USDT-PERP", ports.MetricOpenInterest, "1h", 10); len(got) != 1 {
		t.Fatalf("the 1h point went with the 5m ones: %+v", got)
	}
	if n, err := repo.Purge(ctx, "", t0); err != nil || n != 1 {
		t.Fatalf("purged %d funding rates, %v", n, err)
	}
}
