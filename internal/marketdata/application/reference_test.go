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

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

type fakeSource struct {
	backfills atomic.Int32
	tickers   atomic.Int32
	streams   atomic.Int32
	mu        sync.Mutex
	followed  [][]string // the symbols of each stream
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
	s.followed = append(s.followed, symbols)
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
