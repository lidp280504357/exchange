package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketdata/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestCandlesTradesAndSymbols(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, db, migrations.Market(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db)
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	candle := func(i domain.Interval, open time.Time, last string) domain.Candle {
		return domain.Candle{
			Symbol: "BTC-USDT", Interval: i, OpenTime: open, Open: d("1"), High: d(last), Low: d("1"), Close: d(last),
			Volume: d("0.5"), QuoteVolume: d("35000"), Trades: 2,
		}
	}
	err := store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Candles().Upsert(ctx, []domain.Candle{
			candle(domain.Minute1, t0, "70000"), candle(domain.Minute1, t0.Add(2*time.Minute), "70100"),
			candle(domain.Hour1, t0, "70100"),
		}); err != nil {
			return err
		}
		// The same interval again replaces it.
		if err := r.Candles().Upsert(ctx, []domain.Candle{candle(domain.Minute1, t0.Add(2*time.Minute), "70200")}); err != nil {
			return err
		}
		trades := []domain.Trade{
			{Symbol: "BTC-USDT", Sequence: 4, ID: uuid.NewString(), Number: 1, Price: d("70000"), Quantity: d("0.1"), Quote: d("7000"), TakerSide: "BUY", At: t0},
			{Symbol: "BTC-USDT", Sequence: 9, ID: uuid.NewString(), Number: 2, Price: d("70200"), Quantity: d("0.1"), Quote: d("7020"), TakerSide: "SELL", At: t0.Add(2 * time.Minute)},
		}
		if err := r.Trades().Insert(ctx, trades); err != nil {
			return err
		}
		if err := r.Trades().Insert(ctx, trades[:1]); err != nil { // repeated: ignored
			return err
		}
		return r.Symbols().Save(ctx, ports.SymbolState{Symbol: "BTC-USDT", Sequence: 9, LastPrice: d("70200"), LastAt: t0.Add(2 * time.Minute)})
	})
	if err != nil {
		t.Fatal(err)
	}
	r := store.Read()
	latest, err := r.Candles().Latest(ctx, "BTC-USDT")
	if err != nil || len(latest) != 2 {
		t.Fatalf("latest %+v, %v", latest, err)
	}
	for _, c := range latest {
		if c.Interval == domain.Minute1 && (!c.OpenTime.Equal(t0.Add(2*time.Minute)) || !c.Close.Equal(d("70200")) || c.Trades != 2) {
			t.Fatalf("latest 1m %+v", c)
		}
	}
	got, err := r.Candles().Range(ctx, "BTC-USDT", domain.Minute1, t0, t0.Add(2*time.Minute))
	if err != nil || len(got) != 1 || !got[0].OpenTime.Equal(t0) {
		t.Fatalf("range %+v, %v", got, err)
	}
	before, err := r.Candles().Before(ctx, "BTC-USDT", domain.Minute1, t0.Add(2*time.Minute))
	if err != nil || before == nil || !before.Close.Equal(d("70000")) {
		t.Fatalf("before %+v, %v", before, err)
	}
	if none, err := r.Candles().Before(ctx, "BTC-USDT", domain.Minute1, t0); err != nil || none != nil {
		t.Fatalf("nothing before the first: %+v, %v", none, err)
	}
	recent, err := r.Trades().Recent(ctx, "BTC-USDT", 10)
	if err != nil || len(recent) != 2 || recent[0].Sequence != 9 || recent[0].Number != 2 || recent[0].TakerSide != "SELL" {
		t.Fatalf("recent %+v, %v", recent, err)
	}
	states, err := r.Symbols().All(ctx)
	if err != nil || len(states) != 1 || states[0].Sequence != 9 || !states[0].LastPrice.Equal(d("70200")) {
		t.Fatalf("symbols %+v, %v", states, err)
	}
	if n, err := r.Trades().Purge(ctx, t0.Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("purge: %d, %v", n, err)
	}
}
