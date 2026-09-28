package application

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

type fakeSource struct {
	backfills atomic.Int32
	streams   atomic.Int32
}

func (s *fakeSource) Name() string { return "fake" }

func (s *fakeSource) Backfill(_ context.Context, symbol string, _ time.Time) ([]domain.Candle, error) {
	s.backfills.Add(1)
	now := domain.Minute1.Start(time.Now())
	return []domain.Candle{
		{Symbol: symbol, Interval: domain.Minute1, OpenTime: now.Add(-time.Minute), Close: d("83900")},
		{Symbol: symbol, Interval: domain.Minute1, OpenTime: now, Close: d("83910")},
	}, nil
}

// Stream sends one update, then fails the first connection and holds the
// second until ctx ends.
func (s *fakeSource) Stream(ctx context.Context, symbols []string, on func(domain.Candle)) error {
	n := s.streams.Add(1)
	on(domain.Candle{Symbol: symbols[0], Interval: domain.Minute1, OpenTime: domain.Minute1.Start(time.Now()), Close: d("83920")})
	if n == 1 {
		return errors.New("connection reset")
	}
	<-ctx.Done()
	return ctx.Err()
}

type switchFlags struct{ on atomic.Bool }

func (f *switchFlags) Enabled(key string, _ flags.Subject) bool {
	return key == flags.KeyReferenceFeed && f.on.Load()
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 200 {
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
	f := NewReferenceFeed(src, store, fl, []string{"BTC-USDT"}, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	f.recheck = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = f.Run(ctx); close(done) }()

	time.Sleep(50 * time.Millisecond)
	if src.backfills.Load() != 0 {
		t.Fatal("the feed must stay off while its flag is off")
	}
	fl.on.Store(true)
	// The first connection fails; the feed backfills again and reconnects.
	eventually(t, "a second stream", func() bool { return src.streams.Load() == 2 })
	if src.backfills.Load() != 2 {
		t.Fatalf("%d backfills, want one per connection", src.backfills.Load())
	}
	ref, fresh := f.Latest("BTC-USDT")
	if !fresh || !ref.Price.Equal(d("83920")) || ref.Source != "fake" {
		t.Fatalf("latest %+v fresh %v", ref, fresh)
	}
	if last, _ := store.Read().References().Latest(ctx, "fake", "BTC-USDT"); last == nil || !last.Close.Equal(d("83920")) {
		t.Fatalf("stored %+v", last)
	}
	if _, fresh := f.Latest("ETH-USDT"); fresh {
		t.Fatal("a symbol the feed does not follow has no reference")
	}
	// Off again: the stream stops and the price is dropped.
	fl.on.Store(false)
	eventually(t, "the price dropped", func() bool { _, ok := f.get("BTC-USDT"); return !ok })
	cancel()
	<-done
}
