package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/skill/exchange/internal/marketdata/adapters/postgres"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// A reference minute a price event touched is stored as shown: the
// reference market's own update of it does not replace it, the purge keeps
// it, and it is read back by symbol and time (review GD ③).
func TestTouchedReferenceMinutes(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, db, migrations.Market(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	refs := postgres.NewStore(db).Read().References()
	t0 := time.Date(2026, 10, 7, 4, 0, 0, 0, time.UTC)
	minute := func(at time.Time, high string) domain.Candle {
		return domain.Candle{
			Symbol: "BTC-USDT", Interval: domain.Minute1, OpenTime: at, Open: d("84000"), High: d(high), Low: d("83990"), Close: d(high),
			Volume: d("1"), QuoteVolume: d("84000"), Trades: 3,
		}
	}
	if err := refs.Upsert(ctx, "binance", []domain.Candle{minute(t0, "84050"), minute(t0.Add(time.Minute), "84060")}); err != nil {
		t.Fatal(err)
	}
	if err := refs.UpsertOverlaid(ctx, "binance", []domain.Candle{minute(t0.Add(time.Minute), "97440")}); err != nil {
		t.Fatal(err)
	}
	if err := refs.Upsert(ctx, "binance", []domain.Candle{minute(t0.Add(time.Minute), "84070")}); err != nil {
		t.Fatal(err)
	}
	got, err := refs.Overlaid(ctx, "binance", "BTC-USDT", t0, t0.Add(5*time.Minute))
	if err != nil || len(got) != 1 || !got[0].High.Equal(d("97440")) || !got[0].OpenTime.Equal(t0.Add(time.Minute)) || got[0].Trades != 3 {
		t.Fatalf("touched %+v %v", got, err)
	}
	if none, err := refs.Overlaid(ctx, "binance", "ETH-USDT", t0, t0.Add(5*time.Minute)); err != nil || len(none) != 0 {
		t.Fatalf("another pair %+v %v", none, err)
	}
	if n, err := refs.Purge(ctx, t0.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("purged %d %v, want the untouched minute only", n, err)
	}
	if kept, _ := refs.Overlaid(ctx, "binance", "BTC-USDT", t0, t0.Add(5*time.Minute)); len(kept) != 1 {
		t.Fatalf("after the purge %+v", kept)
	}
}
