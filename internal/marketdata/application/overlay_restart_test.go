package application

import (
	"errors"
	"testing"
	"time"

	"github.com/skill/exchange/internal/marketdata/domain"
)

// Before the listing of the followed pairs is read (market-data just
// started), a push is answered "not ready" - market-sim pushes again - not
// "not followed", which it takes for good (review C58 ①).
func TestAPushBeforeTheListingIsNotReady(t *testing.T) {
	o, _, now := newOverlayRig()
	listed := false
	o.WithListing(func() bool { return listed })
	push := OverlayPush{Factor: d("1.1"), Until: now.Add(4 * time.Second), Risk: true, EventID: "e1", Seq: 1}
	if err := o.Set("BTC-USDT", push); !errors.Is(err, ErrOverlayNotReady) {
		t.Fatalf("before the listing: %v", err)
	}
	listed = true
	if err := o.Set("BTC-USDT", push); err != nil {
		t.Fatal(err)
	}
	if err := o.Set("SOL-USDT", push); !errors.Is(err, ErrNotFollowed) {
		t.Fatalf("a pair not followed: %v", err)
	}
}

// Whether a minute began within the event is the event's own start that
// its pushes tell (review C58 ②): after a restart of market-data in the
// middle of an event its next minute has the scaled prices only; an event
// that begins within a minute keeps the reference market's prices before
// it, also right after another event on the pair.
func TestTheCandlesGoByWhenTheEventBegan(t *testing.T) {
	o, _, now := newOverlayRig()
	oc := newOverlayCandles(o) // a market-data that just started
	minute := now.Truncate(time.Minute)
	started := minute.Add(-2 * time.Minute)
	if err := o.Set("BTC-USDT", OverlayPush{Factor: d("1.1"), Until: now.Add(4 * time.Second), EventID: "e1", Seq: 1, Since: started}); err != nil {
		t.Fatal(err)
	}
	c := domain.Candle{Symbol: "BTC-USDT", OpenTime: minute, Open: d("86000"), High: d("86020"), Low: d("85980"), Close: d("86010")}
	if got, _ := oc.apply(c); !got.Open.Equal(d("94600")) || !got.Low.Equal(d("94578")) {
		t.Fatalf("the first minute after a restart within the event: %+v", got)
	}
	// The event ends; another begins in the middle of the next minute.
	if err := o.Set("BTC-USDT", OverlayPush{Factor: d("1"), Until: now.Add(4 * time.Second), EventID: "e1", Seq: 2}); err != nil {
		t.Fatal(err)
	}
	*now = minute.Add(time.Minute + 30*time.Second)
	if err := o.Set("BTC-USDT", OverlayPush{Factor: d("1.1"), Until: now.Add(4 * time.Second), EventID: "e2", Seq: 1, Since: *now}); err != nil {
		t.Fatal(err)
	}
	next := domain.Candle{
		Symbol: "BTC-USDT", OpenTime: minute.Add(time.Minute), Open: d("86000"), High: d("86020"), Low: d("85980"), Close: d("86010"),
	}
	if got, _ := oc.apply(next); !got.Open.Equal(d("86000")) || !got.Low.Equal(d("85980")) || !got.High.Equal(d("94611")) {
		t.Fatalf("a minute the next event began in: %+v", got)
	}
}
