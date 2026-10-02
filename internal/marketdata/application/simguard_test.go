package application

import (
	"context"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

type simHaltFlag struct{ on atomic.Bool }

func (f *simHaltFlag) Enabled(key string, _ flags.Subject) bool {
	return key == flags.KeySimHaltOnLoss && f.on.Load()
}

// A pair whose simulated market falls silent for a minute halts with its
// perpetual; the reports back for 30 seconds resume both; the flag off
// resumes them too. A pair never heard from is left alone.
func TestSimGuardHaltsASilentPairAndResumesIt(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	list := newListing([]ports.Pair{
		{Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", Status: "TRADING", Reference: ref("BTC-USDT", "BTCUSDT")},
		{Symbol: "ASTRA-USDT", Base: "ASTRA", Quote: "USDT", Status: "TRADING"},
	}, []ports.Contract{{Symbol: "ASTRA-USDT-PERP", IndexSymbol: "ASTRA-USDT", Status: "TRADING"}})
	store := newMemStore()
	svc := newService(t, store, &now)
	platform := &PlatformReference{Svc: svc, Refs: NewReferenceMap(list, slog.New(slog.DiscardHandler)), Now: func() time.Time { return now }}
	fl := &simHaltFlag{}
	fl.on.Store(true)
	g := NewSimGuard(platform, list, store, fl, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	g.now = func() time.Time { return now }
	step := func(after time.Duration) {
		t.Helper()
		now = now.Add(after)
		if err := g.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	report := func() {
		t.Helper()
		if err := platform.Report(ctx, "ASTRA-USDT", d("1.01")); err != nil {
			t.Fatal(err)
		}
	}
	report()
	step(50 * time.Second)
	if len(*list.moves) != 0 {
		t.Fatalf("50 seconds: %v", *list.moves)
	}
	step(11 * time.Second)
	if want := []string{"ASTRA-USDT TRADING->HALT", "ASTRA-USDT-PERP TRADING->HALT"}; !slices.Equal(*list.moves, want) {
		t.Fatalf("a minute silent: %v", *list.moves)
	}
	if halts, _ := store.Read().SimHalts().List(ctx); len(halts) != 1 {
		t.Fatalf("halts %+v", halts)
	}
	step(5 * time.Second) // still silent: no second halt
	if len(*list.moves) != 2 {
		t.Fatalf("halted twice: %v", *list.moves)
	}
	for range 6 { // reports back every 5 seconds
		report()
		step(5 * time.Second)
	}
	if len(*list.moves) != 2 {
		t.Fatalf("back for 25 seconds: %v", *list.moves)
	}
	report()
	step(6 * time.Second)
	if want := []string{"ASTRA-USDT HALT->TRADING", "ASTRA-USDT-PERP HALT->TRADING"}; !slices.Equal((*list.moves)[2:], want) {
		t.Fatalf("back for 30 seconds: %v", *list.moves)
	}
	if halts, _ := store.Read().SimHalts().List(ctx); len(halts) != 0 {
		t.Fatalf("halts %+v", halts)
	}
	// Silent again with the flag off: nothing; on, a halt; off again, the
	// pair trades though still silent.
	fl.on.Store(false)
	step(2 * time.Minute)
	if len(*list.moves) != 4 {
		t.Fatalf("flag off: %v", *list.moves)
	}
	fl.on.Store(true)
	step(time.Second)
	fl.on.Store(false)
	step(time.Second)
	if want := []string{"ASTRA-USDT TRADING->HALT", "ASTRA-USDT-PERP TRADING->HALT", "ASTRA-USDT HALT->TRADING", "ASTRA-USDT-PERP HALT->TRADING"}; !slices.Equal((*list.moves)[4:], want) {
		t.Fatalf("on then off: %v", *list.moves)
	}
}

// A halt that failed (instrument-service unavailable) is recorded and
// finished by a later step while the market stays silent.
func TestSimGuardFinishesAHaltThatFailed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	list := newListing([]ports.Pair{{Symbol: "ASTRA-USDT", Base: "ASTRA", Quote: "USDT", Status: "TRADING"}},
		[]ports.Contract{{Symbol: "ASTRA-USDT-PERP", IndexSymbol: "ASTRA-USDT", Status: "TRADING"}})
	store := newMemStore()
	svc := newService(t, store, &now)
	platform := &PlatformReference{Svc: svc, Refs: NewReferenceMap(list, slog.New(slog.DiscardHandler)), Now: func() time.Time { return now }}
	fl := &simHaltFlag{}
	fl.on.Store(true)
	g := NewSimGuard(platform, list, store, fl, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	g.now = func() time.Time { return now }
	if err := platform.Report(ctx, "ASTRA-USDT", d("1.01")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	list.down.Store(true)
	if err := g.Step(ctx); err == nil {
		t.Fatal("the halt failed, the step passed")
	}
	if halts, _ := store.Read().SimHalts().List(ctx); len(halts) != 1 || len(*list.moves) != 0 {
		t.Fatalf("recorded %+v, moves %v", halts, *list.moves)
	}
	list.down.Store(false)
	now = now.Add(5 * time.Second)
	if err := g.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ASTRA-USDT TRADING->HALT", "ASTRA-USDT-PERP TRADING->HALT"}; !slices.Equal(*list.moves, want) {
		t.Fatalf("finished: %v", *list.moves)
	}
	now = now.Add(5 * time.Second)
	if err := g.Step(ctx); err != nil || len(*list.moves) != 2 {
		t.Fatalf("halted again: %v %v", err, *list.moves)
	}
}
