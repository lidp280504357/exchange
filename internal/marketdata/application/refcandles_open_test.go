package application

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
)

// spikeHistory is the source's candles around a price event's minute
// (04:01, not in them: the source knows the reference market only) for the
// charted intervals.
func spikeHistory() *history {
	open := func(t, o, h, l, c, v string) domain.Candle {
		return domain.Candle{OpenTime: at(t), Open: d(o), High: d(h), Low: d(l), Close: d(c), Volume: d(v), QuoteVolume: d(v).Mul(d(c)), Trades: 3}
	}
	return &history{klines: map[domain.Interval][]domain.Candle{
		domain.Minute1: {
			minute("2026-10-07T04:00:00Z", "84000", "84050", "83990", "84030", "1"),
			minute("2026-10-07T04:01:00Z", "84030", "84060", "84000", "84040", "1"),
			minute("2026-10-07T04:02:00Z", "84040", "84060", "84020", "84050", "2"),
		},
		domain.Minute15: {open("2026-10-07T04:00:00Z", "84000", "84100", "83900", "84050", "30")},
		domain.Hour1:    {open("2026-10-07T04:00:00Z", "84000", "84100", "83900", "84050", "60")},
		domain.Hour4:    {open("2026-10-07T04:00:00Z", "84000", "84100", "83900", "84050", "240")},
		domain.Day1:     {open("2026-10-07T00:00:00Z", "83000", "84500", "82500", "84045", "900")},
	}}
}

// spikeShown checks that every charted interval's open candle has the
// spike, in the charts' answer and in the push.
func spikeShown(t *testing.T, rc *ReferenceCandles, when string) {
	t.Helper()
	ctx := context.Background()
	for _, i := range []domain.Interval{domain.Minute15, domain.Hour1, domain.Hour4, domain.Day1} {
		list, err := rc.Candles(ctx, "BTC-USDT", ref("BTC-USDT", "BTCUSDT"), string(i), time.Time{}, time.Time{}, 10)
		if err != nil || len(list) == 0 || !list[len(list)-1].High.Equal(d("97440")) {
			t.Fatalf("%s: the %s chart's open candle %+v %v", when, i, list, err)
		}
	}
	ones, err := rc.Candles(ctx, "BTC-USDT", ref("BTC-USDT", "BTCUSDT"), "1m", time.Time{}, time.Time{}, 10)
	if err != nil || len(ones) != 3 || !ones[1].High.Equal(d("97440")) || !ones[1].Close.Equal(d("97000")) {
		t.Fatalf("%s: the 1m chart %+v %v", when, ones, err)
	}
	pushed := map[string]string{}
	for _, u := range rc.Push(ctx, nil) {
		if m, ok := u.Message.(*marketv1.CandleUpdated); ok && u.Symbol == "BTC-USDT" {
			pushed[m.GetCandle().GetInterval()] = m.GetCandle().GetHigh()
		}
	}
	for _, i := range []string{"15m", "1h", "4h", "1d"} {
		if pushed[i] != "97440" {
			t.Fatalf("%s: the %s push %v", when, i, pushed)
		}
	}
}

// Every interval's open candle has a price event's spike, in the charts and
// in the pushes, after it and after a restart, whichever way the candle
// started (review C64: on 2026-10-07 09:37 the 1h chart had the spike at
// once and the 4h one only later - the initializer had read the 4h open
// candle from the source after the spike, and the source does not know
// it).
func TestEveryIntervalsOpenCandleHasTheSpike(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	refs := store.Read().References()
	rc := newReferenceRig(spikeHistory())
	defer rc.wg.Wait()
	rc.WithOverlaid(store, "binance")
	// 04:00 opens 15m, 1h and 4h with the minute; 1d is read from the
	// source.
	rc.Observe(minute("2026-10-07T04:00:00Z", "84000", "84050", "83990", "84030", "1"))
	rc.wg.Wait()
	spike := minute("2026-10-07T04:01:00Z", "84030", "97440", "84000", "97000", "1") // as shown and stored
	if err := refs.UpsertOverlaid(ctx, "binance", []domain.Candle{spike}); err != nil {
		t.Fatal(err)
	}
	rc.Observe(spike)
	rc.Observe(minute("2026-10-07T04:02:00Z", "84040", "84050", "84030", "84040", "1")) // the event over
	spikeShown(t, rc, "live")

	// A restart: the next minute's update finds every longer interval
	// midway and reads it from the source, which does not have the spike.
	again := newReferenceRig(spikeHistory())
	defer again.wg.Wait()
	again.WithOverlaid(store, "binance")
	again.Observe(minute("2026-10-07T04:02:00Z", "84040", "84060", "84020", "84050", "2"))
	again.wg.Wait()
	for _, i := range []domain.Interval{domain.Minute15, domain.Hour1, domain.Hour4, domain.Day1} {
		if o := again.open["BTC-USDT"][i]; o == nil || !o.c.High.Equal(d("97440")) || !o.c.Close.Equal(d("84050")) {
			t.Fatalf("after a restart the %s open candle %+v", i, o)
		}
	}
	spikeShown(t, again, "after a restart")
}

// spikeSource streams one 1m candle and holds the connection.
type spikeSource struct {
	fakeSource
	c domain.Candle
}

func (s *spikeSource) Stream(ctx context.Context, _ []ports.Reference, on ports.StreamHandlers) error {
	on.Candle(s.c)
	<-ctx.Done()
	return ctx.Err()
}

// The charts' open candles fold the feed's 1m candle as shown and stored,
// a price event's factor applied (review C64 (a)).
func TestTheChartsFoldTheCandleAsShown(t *testing.T) {
	store := newMemStore()
	start := domain.Minute1.Start(time.Now())
	src := &spikeSource{c: domain.Candle{
		Symbol: "BTC-USDT", Interval: domain.Minute1, OpenTime: start, Open: d("84000"), High: d("84010"), Low: d("83990"),
		Close: d("84000"), Volume: d("1"),
	}}
	fl := &switchFlags{}
	fl.on.Store(true)
	f := NewReferenceFeed(src, store, fl, testListing(), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	o, _, _ := newOverlayRig()
	o.now = time.Now
	if err := o.Set("BTC-USDT", OverlayPush{
		Factor: d("1.1"), Until: time.Now().Add(10 * time.Second), EventID: "e1", Seq: 1,
		Since: start.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	f.WithOverlay(o)
	var mu sync.Mutex
	var seen []domain.Candle
	f.Observe(func(c domain.Candle) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, c)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = f.Run(ctx); close(done) }()
	eventually(t, "the candle observed", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) > 0
	})
	cancel()
	<-done
	got := seen[0]
	if !got.Open.Equal(d("92400")) || !got.High.Equal(d("92411")) || !got.Low.Equal(d("92389")) || !got.Close.Equal(d("92400")) {
		t.Fatalf("observed %+v", got)
	}
	stored, err := store.Read().References().Overlaid(context.Background(), "fake", "BTC-USDT", start, start.Add(time.Minute))
	if err != nil || len(stored) != 1 || !stored[0].High.Equal(got.High) || !stored[0].Close.Equal(got.Close) {
		t.Fatalf("stored %+v %v", stored, err)
	}
}
