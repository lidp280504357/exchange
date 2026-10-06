package application

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

type fakeSource struct {
	backfills atomic.Int32
	tickers   atomic.Int32
	streams   atomic.Int32
	mu        sync.Mutex
	followed  [][]string // the symbols of each stream
	markets   []string   // and its market
}

func (s *fakeSource) Name() string { return "fake" }

func (s *fakeSource) Backfill(_ context.Context, ref ports.Reference, _, to time.Time) ([]domain.Candle, error) {
	s.backfills.Add(1)
	return []domain.Candle{
		{Symbol: ref.Symbol, Interval: domain.Minute1, OpenTime: to.Add(-2 * time.Minute), Close: d("83900")},
		{Symbol: ref.Symbol, Interval: domain.Minute1, OpenTime: to.Add(-time.Minute), Close: d("83910")},
	}, nil
}

func (s *fakeSource) Tickers(_ context.Context, refs []ports.Reference) ([]domain.Ticker, error) {
	s.tickers.Add(1)
	out := make([]domain.Ticker, 0, len(refs))
	for _, r := range refs {
		out = append(out, domain.Ticker{Symbol: r.Symbol, Last: d("83000"), Open: d("82000"), At: time.Now().Add(-time.Second)})
	}
	return out, nil
}

// Stream sends a candle and a ticker, then fails the first connection and
// holds the others until ctx ends.
func (s *fakeSource) Stream(ctx context.Context, refs []ports.Reference, on ports.StreamHandlers) error {
	n := s.streams.Add(1)
	var symbols []string
	for _, r := range refs {
		symbols = append(symbols, r.Symbol)
	}
	s.mu.Lock()
	s.followed, s.markets = append(s.followed, symbols), append(s.markets, refs[0].Market)
	s.mu.Unlock()
	on.Candle(domain.Candle{Symbol: refs[0].Symbol, Interval: domain.Minute1, OpenTime: domain.Minute1.Start(time.Now()), Close: d("83920")})
	on.Ticker(domain.Ticker{Symbol: refs[0].Symbol, Last: d("83921"), Open: d("82000"), At: time.Now()})
	if n == 1 {
		return errors.New("connection reset")
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *fakeSource) streamed(i int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.followed) {
		return nil
	}
	return s.followed[i]
}

type switchFlags struct{ on, halt atomic.Bool }

func (f *switchFlags) Enabled(key string, _ flags.Subject) bool {
	switch key {
	case flags.KeyReferenceFeed:
		return f.on.Load()
	case flags.KeyHaltOnFeedLoss:
		return f.halt.Load()
	}
	return false
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 300 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

func TestReferenceFeed(t *testing.T) {
	store := newMemStore()
	src := &fakeSource{}
	fl := &switchFlags{}
	list := testListing()
	list.contracts = []ports.Contract{{Symbol: "BTC-USDT-PERP", IndexSymbol: "BTC-USDT"}} // without a perpetual to follow
	f := NewReferenceFeed(src, store, fl, list, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	f.recheck, f.remap = 20*time.Millisecond, 50*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = f.Run(ctx); close(done) }()

	time.Sleep(50 * time.Millisecond)
	if src.streams.Load() != 0 {
		t.Fatal("the feed must stay off while its flag is off")
	}
	fl.on.Store(true)
	// The first connection fails; the feed reconnects and loads the
	// tickers alongside the stream.
	eventually(t, "a second stream", func() bool { return src.streams.Load() == 2 })
	eventually(t, "the tickers loaded", func() bool { return src.tickers.Load() >= 1 })
	if got := src.streamed(0); len(got) != 1 || got[0] != "BTC-USDT" {
		t.Fatalf("followed %v: only the pairs with a reference market", got)
	}
	// Each connection sends its candle (83920) before its ticker (83921):
	// the second one's candle may just have come in (review 2026-10-03:
	// asserting at once saw 83920 now and then).
	eventually(t, "the stream's last price", func() bool {
		latest, fresh := f.Latest("BTC-USDT")
		return fresh && latest.Price.Equal(d("83921")) && latest.Source == "fake"
	})
	eventually(t, "the stream's ticker", func() bool {
		tk, ok := f.Ticker("BTC-USDT")
		return ok && tk.Last.Equal(d("83921"))
	})
	if last, _ := store.Read().References().Latest(ctx, "fake", "BTC-USDT"); last == nil || !last.Close.Equal(d("83920")) {
		t.Fatalf("stored %+v", last)
	}
	if f.Received().IsZero() {
		t.Fatal("the stream's messages are noted")
	}
	if _, fresh := f.Latest("ETH-BTC"); fresh {
		t.Fatal("a pair without a reference market has no reference")
	}
	// A pair gets a reference market: the feed reconnects to follow it.
	list.mu.Lock()
	list.pairs[1].Reference = ref("ETH-BTC", "ETHBTC")
	list.mu.Unlock()
	eventually(t, "a stream with both pairs", func() bool { return len(src.streamed(2)) == 2 })
	// Off again: the stream stops and the data is dropped.
	fl.on.Store(false)
	eventually(t, "the price dropped", func() bool { _, ok := f.get("BTC-USDT"); return !ok })
	if _, ok := f.Ticker("BTC-USDT"); ok || !f.Received().IsZero() {
		t.Fatal("the tickers are dropped with the prices")
	}
	cancel()
	<-done
}

func TestReferenceBackfillStopsWhereTheStreamStarted(t *testing.T) {
	store := newMemStore()
	src := &fakeSource{}
	f := NewReferenceFeed(src, store, &switchFlags{}, testListing(), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	ctx := context.Background()
	streaming := domain.Minute1.Start(time.Now())
	btc := ref("BTC-USDT", "BTCUSDT")
	if err := f.backfill(ctx, btc, streaming); err != nil || src.backfills.Load() != 1 {
		t.Fatalf("backfill: %v, %d calls", err, src.backfills.Load())
	}
	last, _ := store.Read().References().Latest(ctx, "fake", "BTC-USDT")
	if last == nil || !last.OpenTime.Equal(streaming.Add(-time.Minute)) {
		t.Fatalf("latest stored %+v", last)
	}
	// Nothing missing up to the stream's first minute: no request.
	if err := f.backfill(ctx, btc, streaming.Add(-time.Minute)); err != nil || src.backfills.Load() != 1 {
		t.Fatalf("second backfill: %v, %d calls", err, src.backfills.Load())
	}
}

// A contract with a reference market follows its own perpetual on a
// connection of its own (coin-M design §3.2): its candles and tickers are
// kept under the contract, and its messages are not the spot feed's (the
// pairs' halts look at the spot stream alone).
func TestReferenceFeedFollowsTheContractsOnTheirMarket(t *testing.T) {
	store := newMemStore()
	src := &fakeSource{}
	fl := &switchFlags{}
	list := testListing()
	list.pairs = list.pairs[:1] // BTC-USDT only
	list.contracts = append(list.contracts, ports.Contract{
		Symbol: "BTC-USD-PERP", IndexSymbol: "BTC-USDT", MarginType: "COIN", ContractSize: d("100"), ReferenceSymbol: "BTCUSD_PERP",
	})
	f := NewReferenceFeed(src, store, fl, list, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	f.recheck, f.remap = 20*time.Millisecond, time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = f.Run(ctx); close(done) }()
	fl.on.Store(true)
	// One connection a market; the first of them fails and comes back.
	eventually(t, "four streams", func() bool { return src.streams.Load() == 4 })
	markets := map[string][]string{}
	src.mu.Lock()
	for i, m := range src.markets {
		markets[m] = src.followed[i]
	}
	src.mu.Unlock()
	if got := markets[ports.MarketSpot]; len(got) != 1 || got[0] != "BTC-USDT" {
		t.Fatalf("spot %v", got)
	}
	if got := markets[ports.MarketUSDM]; len(got) != 1 || got[0] != "BTC-USDT-PERP" {
		t.Fatalf("USDⓈ-M %v", got)
	}
	if got := markets[ports.MarketCoinM]; len(got) != 1 || got[0] != "BTC-USD-PERP" {
		t.Fatalf("COIN-M %v", got)
	}
	eventually(t, "the perpetual's ticker", func() bool {
		tk, ok := f.Ticker("BTC-USD-PERP")
		return ok && tk.Last.Equal(d("83921"))
	})
	if last, _ := store.Read().References().Latest(ctx, "fake", "BTC-USDT-PERP"); last == nil || !last.Close.Equal(d("83920")) {
		t.Fatalf("the perpetual's candles under the contract: %+v", last)
	}
	f.mu.Lock()
	spot, usdm := f.received[ports.MarketSpot], f.received[ports.MarketUSDM]
	f.mu.Unlock()
	if spot.IsZero() || usdm.IsZero() || !f.Received().Equal(spot) {
		t.Fatalf("received: spot %s, USDⓈ-M %s, Received %s", spot, usdm, f.Received())
	}
	cancel()
	<-done
}
