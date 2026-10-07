package application

import (
	"context"
	"testing"
	"time"

	"github.com/skill/exchange/internal/marketdata/domain"
)

// The charts lay the minutes a price event touched, as stored, over the
// reference market's history (review GD ③): the spike stays in a closed
// 1m candle and in the 5m candle around it; the stored minute is not
// replaced by the reference market's own afterwards.
func TestTheChartsKeepThePriceEventsMinutes(t *testing.T) {
	h := &history{klines: map[domain.Interval][]domain.Candle{
		domain.Minute1: {
			minute("2026-10-07T04:01:00Z", "84000", "84050", "83990", "84030", "1"),
			minute("2026-10-07T04:02:00Z", "84030", "84060", "84000", "84040", "1"),
		},
		domain.Minute5: {minute("2026-10-07T04:00:00Z", "83900", "84060", "83890", "84040", "5")},
	}}
	rc := newReferenceRig(h)
	defer rc.wg.Wait()
	store := newMemStore()
	rc.WithOverlaid(store, "binance")
	ctx := context.Background()
	refs := store.Read().References()
	spike := minute("2026-10-07T04:01:00Z", "84000", "97440", "83990", "97000", "1")
	if err := refs.UpsertOverlaid(ctx, "binance", []domain.Candle{spike}); err != nil {
		t.Fatal(err)
	}
	if err := refs.Upsert(ctx, "binance", []domain.Candle{minute("2026-10-07T04:01:00Z", "84000", "84050", "83990", "84030", "1")}); err != nil {
		t.Fatal(err)
	}
	if kept, _ := refs.Overlaid(ctx, "binance", "BTC-USDT", at("2026-10-07T04:00:00Z"), at("2026-10-07T04:05:00Z")); len(kept) != 1 ||
		!kept[0].High.Equal(d("97440")) {
		t.Fatalf("the stored minute %+v", kept)
	}
	ref := ref("BTC-USDT", "BTCUSDT")
	ones, err := rc.Candles(ctx, "BTC-USDT", ref, "1m", at("2026-10-07T04:00:00Z"), at("2026-10-07T04:03:00Z"), 10)
	if err != nil || len(ones) != 2 || !ones[0].High.Equal(d("97440")) || !ones[0].Close.Equal(d("97000")) || !ones[1].High.Equal(d("84060")) {
		t.Fatalf("1m %+v %v", ones, err)
	}
	fives, err := rc.Candles(ctx, "BTC-USDT", ref, "5m", at("2026-10-07T04:00:00Z"), at("2026-10-07T04:05:00Z"), 10)
	if err != nil || len(fives) != 1 || !fives[0].High.Equal(d("97440")) || !fives[0].Close.Equal(d("84040")) {
		t.Fatalf("5m %+v %v", fives, err)
	}
}

// A chart request whose read of the touched minutes fails (a client gone,
// the store down) answers an error and leaves the feed's updates free: on
// 2026-10-07 04:28 it returned holding the lock, the feed stalled behind it
// and every followed pair halted.
func TestAFailedReadOfTheTouchedMinutesDoesNotStallTheFeed(t *testing.T) {
	h := &history{klines: map[domain.Interval][]domain.Candle{
		domain.Minute1: {minute("2026-10-07T04:01:00Z", "84000", "84050", "83990", "84030", "1")},
	}}
	rc := newReferenceRig(h)
	defer rc.wg.Wait()
	rc.overlaid = func(context.Context, string, time.Time, time.Time) ([]domain.Candle, error) {
		return nil, context.Canceled
	}
	if _, err := rc.Candles(context.Background(), "BTC-USDT", ref("BTC-USDT", "BTCUSDT"), "1m", time.Time{}, time.Time{}, 10); err == nil {
		t.Fatal("a failed read answered")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		rc.Observe(minute("2026-10-07T04:02:00Z", "84030", "84040", "84020", "84035", "1"))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the feed's update waits on the chart request's lock")
	}
}
