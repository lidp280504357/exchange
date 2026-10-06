package application

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/marketdata/ports"
)

type guardRig struct {
	guard *FeedGuard
	feed  *ReferenceFeed
	list  listing
	store *memStore
	fl    *switchFlags
	now   time.Time
}

func newGuardRig(t *testing.T) *guardRig {
	t.Helper()
	list := testListing()
	store := newMemStore()
	fl := &switchFlags{}
	fl.on.Store(true)
	fl.halt.Store(true)
	feed := NewReferenceFeed(&fakeSource{}, store, fl, list, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	if _, err := feed.follow(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := &guardRig{feed: feed, list: list, store: store, fl: fl, now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	r.guard = NewFeedGuard(feed, list, store, fl, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	r.guard.now = func() time.Time { return r.now }
	feed.now = r.guard.now
	return r
}

// receive notes a stream message now.
func (r *guardRig) receive() {
	r.feed.mu.Lock()
	r.feed.received[ports.MarketSpot] = r.now
	r.feed.mu.Unlock()
}

func (r *guardRig) step(t *testing.T, after time.Duration) {
	t.Helper()
	r.now = r.now.Add(after)
	if err := r.guard.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFeedGuardHaltsAndResumesTheFollowedPairs(t *testing.T) {
	r := newGuardRig(t)
	r.receive()
	r.step(t, 0)
	if s := r.guard.State(); s != FeedOK {
		t.Fatalf("state %s", s)
	}
	r.step(t, time.Minute)
	if s := r.guard.State(); s != FeedLate {
		t.Fatalf("a minute without data: %s", s)
	}
	r.step(t, 4*time.Minute)
	if s := r.guard.State(); s != FeedDown {
		t.Fatalf("five minutes without data: %s", s)
	}
	// Only the followed pair is halted (ETH-BTC follows nothing).
	if m := r.list.moved(); !slices.Equal(m, []string{"BTC-USDT TRADING->HALT"}) {
		t.Fatalf("moves %v", m)
	}
	halts, _ := r.store.Read().Halts().List(context.Background())
	if len(halts) != 1 || halts[0].Symbol != "BTC-USDT" {
		t.Fatalf("halts %+v", halts)
	}
	// Back, but not for long enough yet.
	r.receive()
	r.step(t, 0)
	r.receive()
	r.step(t, 20*time.Second)
	if len(r.list.moved()) != 1 {
		t.Fatalf("resumed too early: %v", r.list.moved())
	}
	r.receive()
	r.step(t, 15*time.Second)
	if m := r.list.moved(); !slices.Equal(m, []string{"BTC-USDT TRADING->HALT", "BTC-USDT HALT->TRADING"}) {
		t.Fatalf("moves %v", m)
	}
	if halts, _ := r.store.Read().Halts().List(context.Background()); len(halts) != 0 {
		t.Fatalf("halts left %+v", halts)
	}
}

func TestFeedGuardLeavesPairsAnOperatorMoved(t *testing.T) {
	r := newGuardRig(t)
	r.step(t, 0)
	r.step(t, 6*time.Minute) // never connected: counts from when the feed went on
	if len(r.list.moved()) != 1 {
		t.Fatalf("moves %v", r.list.moved())
	}
	// An operator puts the pair in cancel-only while it is halted; the
	// halt flag goes off: the guard forgets its halt without moving it.
	r.list.setStatus("BTC-USDT", "CANCEL_ONLY")
	r.fl.halt.Store(false)
	r.step(t, time.Second)
	if len(r.list.moved()) != 1 {
		t.Fatalf("moved a pair an operator moved: %v", r.list.moved())
	}
	if halts, _ := r.store.Read().Halts().List(context.Background()); len(halts) != 0 {
		t.Fatalf("halts left %+v", halts)
	}
}

func TestFeedGuardNeedsItsFlag(t *testing.T) {
	r := newGuardRig(t)
	r.fl.halt.Store(false)
	r.step(t, 0)
	r.step(t, 10*time.Minute)
	if r.guard.State() != FeedDown || len(r.list.moved()) != 0 {
		t.Fatalf("state %s, moves %v", r.guard.State(), r.list.moved())
	}
	r.fl.on.Store(false)
	if r.guard.State() != FeedOff {
		t.Fatalf("feed off: %s", r.guard.State())
	}
}
