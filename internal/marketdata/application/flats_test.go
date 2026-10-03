package application

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// flatFlags has market.flat_minutes on for the symbols it holds.
type flatFlags map[string]bool

func (f flatFlags) Enabled(key string, s flags.Subject) bool {
	return key == flags.KeyFlatMinutes && f[s.Symbol]
}

// newFlats has s store the flat minutes of the symbols of unreferenced
// that on has the switch on for.
func newFlats(t *testing.T, s *Service, unreferenced *[]string, on flatFlags) *FlatMinutes {
	t.Helper()
	f := NewFlatMinutes(func(context.Context) []string { return *unreferenced }, on, event.NewFactory("market-data-service", "test"),
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	f.Decide(context.Background())
	s.StoreFlats(f)
	return f
}

// flatEvents returns the flat minutes queued since the last call, as
// "symbol hh:mm close".
func flatEvents(t *testing.T, store *memStore) []string {
	t.Helper()
	var out []string
	for _, m := range store.takeOutbox(t) {
		c := m.(*marketv1.CandleClosed).GetCandle()
		if c.GetTradeCount() != 0 || c.GetInterval() != "1m" || !c.GetClosed() {
			t.Fatalf("not a flat minute %v", c)
		}
		out = append(out, c.GetSymbol()+" "+c.GetOpenTime().AsTime().Format("15:04")+" "+c.GetClose())
	}
	return out
}

// made returns how many flat minutes f counted.
func made(t *testing.T, f *FlatMinutes) float64 {
	t.Helper()
	var m dto.Metric
	if err := f.made.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func symbolTrade(symbol string, seq uint64, price, when string) domain.Trade {
	t := trade(seq, price, "0.1", when)
	t.Symbol = symbol
	return t
}

// The minutes without a trade of a symbol charting the platform's trades
// are stored flat when its next trade is applied (coordinator 2026-10-04,
// review AU): at the previous close, no volume, no trade, their events
// queued with them; each longer interval takes them in as a rollup of the
// minutes would (one that opens on a flat minute opens at that price, and
// keeps it when a trade comes); they push nothing of their own.
func TestQuietMinutesAreStoredWithTheNextTrade(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	now := at("2026-09-30T10:01:30Z")
	s := newService(t, store, &now)
	unreferenced := []string{"BTC-USDT"}
	f := newFlats(t, s, &unreferenced, flatFlags{"BTC-USDT": true})
	apply := func(trades ...domain.Trade) {
		t.Helper()
		if _, err := s.OnTrades(ctx, trades); err != nil {
			t.Fatal(err)
		}
	}
	apply(trade(1, "70000", "0.1", "2026-09-30T10:01:10Z"), trade(2, "70500", "0.1", "2026-09-30T10:01:20Z"))
	if got := flatEvents(t, store); len(got) != 0 {
		t.Fatalf("flats without a quiet minute %v", got)
	}
	// 10:02 and 10:03 quiet: stored with the trade of 10:04, whenever it
	// comes.
	now = at("2026-09-30T10:09:00Z")
	apply(trade(3, "71000", "0.1", "2026-09-30T10:04:10Z"))
	if got := flatEvents(t, store); !slices.Equal(got, []string{"BTC-USDT 10:02 70500", "BTC-USDT 10:03 70500"}) {
		t.Fatalf("events %v", got)
	}
	for _, m := range []string{"10:02", "10:03"} {
		flat := store.candles["BTC-USDT|1m|2026-09-30T"+m+":00Z"]
		if flat.Trades != 0 || !flat.Volume.IsZero() || !flat.Open.Equal(d("70500")) || !flat.High.Equal(d("70500")) ||
			!flat.Low.Equal(d("70500")) || !flat.Close.Equal(d("70500")) {
			t.Fatalf("the flat minute %s %+v", m, flat)
		}
	}
	if c := store.candles["BTC-USDT|1m|2026-09-30T10:04:00Z"]; c.Trades != 1 || !c.Open.Equal(d("71000")) {
		t.Fatalf("10:04 %+v", c)
	}
	if five := store.candles["BTC-USDT|5m|2026-09-30T10:00:00Z"]; five.Trades != 3 || !five.Open.Equal(d("70000")) ||
		!five.High.Equal(d("71000")) || !five.Low.Equal(d("70000")) || !five.Close.Equal(d("71000")) {
		t.Fatalf("5m %+v", five)
	}
	if got := made(t, f); got != 2 {
		t.Fatalf("counted %v", got)
	}

	// 10:05–10:10 quiet, then a trade at 10:11: the 5m candle from 10:05
	// is flat at 71000; the one from 10:10 opened flat and keeps that open,
	// its high and low counting both.
	apply(trade(4, "69000", "0.1", "2026-09-30T10:11:10Z"))
	if got := flatEvents(t, store); len(got) != 6 || got[0] != "BTC-USDT 10:05 71000" || got[5] != "BTC-USDT 10:10 71000" {
		t.Fatalf("events %v", got)
	}
	if five := store.candles["BTC-USDT|5m|2026-09-30T10:05:00Z"]; five.Trades != 0 || !five.Open.Equal(d("71000")) ||
		!five.High.Equal(d("71000")) || !five.Low.Equal(d("71000")) || !five.Close.Equal(d("71000")) {
		t.Fatalf("5m from 10:05 %+v", five)
	}
	if five := store.candles["BTC-USDT|5m|2026-09-30T10:10:00Z"]; !five.Open.Equal(d("71000")) || !five.High.Equal(d("71000")) ||
		!five.Low.Equal(d("69000")) || !five.Close.Equal(d("69000")) || five.Trades != 1 {
		t.Fatalf("5m from 10:10, opened flat %+v", five)
	}
	// Pushed: the trade's candle of each interval and the ticker, nothing
	// for the flats.
	now = at("2026-09-30T10:11:30Z")
	if got := kinds(s.Updates(now)); len(got) != len(domain.Intervals)+1 || got["updated:1m"] != 1 || got["ticker"] != 1 {
		t.Fatalf("pushed %v", got)
	}

	// After three hours away, the last hour before the trade (12:20 to
	// 13:19); the minutes before it stay gaps.
	apply(trade(5, "69500", "0.1", "2026-09-30T13:20:10Z"))
	got := flatEvents(t, store)
	if len(got) != int(FlatCatchUp/time.Minute) || got[0] != "BTC-USDT 12:20 69000" || got[len(got)-1] != "BTC-USDT 13:19 69000" {
		t.Fatalf("after three hours: %d flats, %v … %v", len(got), got[0], got[len(got)-1])
	}
	if _, ok := store.candles["BTC-USDT|1m|2026-09-30T12:19:00Z"]; ok {
		t.Fatal("a flat older than the catch-up")
	}
}

// A trade consumer behind by minutes (a deploy, a crash, a Redpanda
// outage) stores what one on time does (review AU): a minute is flat only
// once a later trade showed it quiet, so trades that come late go into
// their own minutes, never into a flat.
func TestLateTradesMakeTheSameFlats(t *testing.T) {
	ctx := context.Background()
	trades := []domain.Trade{
		trade(1, "70000", "0.1", "2026-09-30T10:01:10Z"), trade(2, "70200", "0.1", "2026-09-30T10:02:30Z"),
		trade(3, "70400", "0.1", "2026-09-30T10:03:30Z"), trade(4, "70600", "0.1", "2026-09-30T10:05:10Z"),
	}
	stored := func(late bool) (map[string]domain.Candle, []string) {
		store := newMemStore()
		now := at("2026-09-30T10:01:20Z")
		s := newService(t, store, &now)
		unreferenced := []string{"BTC-USDT"}
		newFlats(t, s, &unreferenced, flatFlags{"BTC-USDT": true})
		batches := [][]domain.Trade{trades[:1], trades[1:2], trades[2:3], trades[3:]}
		if late {
			batches = [][]domain.Trade{trades[:1], trades[1:]}
		}
		for _, b := range batches {
			now = b[0].At.Add(time.Second)
			if late {
				now = at("2026-09-30T10:07:00Z")
			}
			if _, err := s.OnTrades(ctx, b); err != nil {
				t.Fatal(err)
			}
		}
		return store.candles, flatEvents(t, store)
	}
	onTime, onTimeFlats := stored(false)
	late, lateFlats := stored(true)
	same := func(a, b domain.Candle) bool { return proto.Equal(candleProto(a, false), candleProto(b, false)) }
	if !maps.EqualFunc(onTime, late, same) {
		t.Fatalf("on time %v\nlate %v", onTime, late)
	}
	if !slices.Equal(onTimeFlats, lateFlats) || !slices.Equal(lateFlats, []string{"BTC-USDT 10:04 70400"}) {
		t.Fatalf("flats on time %v, late %v", onTimeFlats, lateFlats)
	}
	if c := late["BTC-USDT|1m|2026-09-30T10:02:00Z"]; c.Trades != 1 || !c.Open.Equal(d("70200")) {
		t.Fatalf("the late trade's minute %+v", c)
	}
}

// Only the symbols decided get flat minutes: those no reference market
// follows with market.flat_minutes on, as last decided; a transaction
// that failed stores and counts them once, when the trades come again.
func TestFlatMinutesOnlyWhereDecided(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	now := at("2026-09-30T10:01:30Z")
	s := newService(t, store, &now)
	unreferenced := []string{"BTC-USDT"} // ETH-USDT follows a reference market
	on := flatFlags{"BTC-USDT": true, "ETH-USDT": true}
	f := newFlats(t, s, &unreferenced, on)
	if !f.Own("BTC-USDT") || f.Own("ETH-USDT") {
		t.Fatal("decided wrong")
	}
	first := []domain.Trade{symbolTrade("BTC-USDT", 1, "70000", "2026-09-30T10:01:10Z"), symbolTrade("ETH-USDT", 1, "2500", "2026-09-30T10:01:10Z")}
	if _, err := s.OnTrades(ctx, first); err != nil {
		t.Fatal(err)
	}
	quiet := []domain.Trade{symbolTrade("BTC-USDT", 2, "70100", "2026-09-30T10:03:10Z"), symbolTrade("ETH-USDT", 2, "2510", "2026-09-30T10:03:10Z")}
	store.down = true
	if _, err := s.OnTrades(ctx, quiet); err == nil {
		t.Fatal("stored with the database down")
	}
	store.down = false
	if _, err := s.OnTrades(ctx, quiet); err != nil {
		t.Fatal(err)
	}
	if got := flatEvents(t, store); !slices.Equal(got, []string{"BTC-USDT 10:02 70000"}) || made(t, f) != 1 {
		t.Fatalf("events %v, counted %v", got, made(t, f))
	}
	if _, ok := store.candles["ETH-USDT|1m|2026-09-30T10:02:00Z"]; ok {
		t.Fatal("a flat minute of a pair on the reference market")
	}

	// The switch off for BTC-USDT, decided again: none.
	delete(on, "BTC-USDT")
	f.Decide(ctx)
	if _, err := s.OnTrades(ctx, []domain.Trade{symbolTrade("BTC-USDT", 3, "70200", "2026-09-30T10:05:10Z")}); err != nil {
		t.Fatal(err)
	}
	if got := flatEvents(t, store); len(got) != 0 {
		t.Fatalf("with the switch off %v", got)
	}
}
