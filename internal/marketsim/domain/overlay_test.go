package domain

import (
	"math"
	"testing"
	"time"
)

func overlay() Event {
	start := time.Date(2026, 10, 7, 2, 0, 0, 0, time.UTC)
	return Event{
		ID: "e", Type: EventOverlay, Symbol: "BTC-USDT", TargetFactor: 1.16, RampUp: 15 * time.Second, Hold: 10 * time.Second,
		RampDown: 5 * time.Second, StartsAt: start, StartedAt: start, Status: EventRunning, CreatedBy: "ops", Reason: "a spike",
	}
}

// The factor climbs linearly to its target, holds, and comes back down
// linearly to 1 (J0 contract §3.1); done at 1 after its total.
func TestOverlayPath(t *testing.T) {
	e := overlay()
	at := func(s float64) time.Time { return e.StartedAt.Add(time.Duration(s * float64(time.Second))) }
	for _, c := range []struct {
		s    float64
		f    float64
		done bool
	}{
		{-1, 1, false},
		{0, 1, false},
		{7.5, 1.08, false},
		{15, 1.16, false},
		{24, 1.16, false},
		{27.5, 1.08, false},
		{29, 1.16 - 0.16*4/5, false},
		{30, 1, true},
		{300, 1, true},
	} {
		f, done := e.OverlayAt(at(c.s))
		if math.Abs(f-c.f) > 1e-9 || done != c.done {
			t.Fatalf("at %vs: %v %v, want %v %v", c.s, f, done, c.f, c.done)
		}
	}
	if p := e.OverlayProgress(at(15)); math.Abs(p-0.5) > 1e-9 {
		t.Fatalf("progress at 15 s of 30: %v", p)
	}
	// Ended at 10 s (1.1067): back to 1 in 3 seconds from there.
	e.EndedAt = at(10)
	from := 1 + 0.16*10/15
	if f, _ := e.OverlayAt(at(11.5)); math.Abs(f-(from+(1-from)/2)) > 1e-9 {
		t.Fatalf("half way back: %v", f)
	}
	if f, done := e.OverlayAt(at(13)); f != 1 || !done {
		t.Fatalf("three seconds after the end: %v %v", f, done)
	}
	// Ended late on the way down, the schedule is closer to 1: it goes on.
	e.EndedAt = at(29)
	if f, _ := e.OverlayAt(at(29.5)); math.Abs(f-(1.16-0.16*4.5/5)) > 1e-9 {
		t.Fatalf("ended on the way down: %v", f)
	}
}

// An overlay's settings: a pair, a target within ±90% and not 1, ramps of
// a second up and 3 down at least, 600 seconds in all at most, no
// duration; the other events have none of them.
func TestOverlayValidate(t *testing.T) {
	if err := overlay().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Event){
		"no pair":         func(e *Event) { e.Symbol = "" },
		"too far":         func(e *Event) { e.TargetFactor = 1.95 },
		"at 1":            func(e *Event) { e.TargetFactor = 1 },
		"ramp up of 0":    func(e *Event) { e.RampUp = 0 },
		"ramp down of 2s": func(e *Event) { e.RampDown = 2 * time.Second },
		"too long":        func(e *Event) { e.Hold = 10 * time.Minute },
		"a duration":      func(e *Event) { e.Duration = time.Minute },
	} {
		e := overlay()
		change(&e)
		if e.Validate() == nil {
			t.Fatalf("%s: valid", name)
		}
	}
	jump := Event{Type: EventJump, Size: 0.1, Symbol: "BTC-USDT", CreatedBy: "ops", Reason: "a jump"}
	if jump.Validate() == nil {
		t.Fatal("a jump with a pair")
	}
	if m := overlay().Move(0, 0); math.Abs(m-0.16) > 1e-9 {
		t.Fatalf("move %v", m)
	}
}

// HOUSE's worst share of a unit's value (J0 contract §3.3).
func TestOverlayLoss(t *testing.T) {
	for _, c := range []struct {
		f       float64
		inverse bool
		want    float64
	}{{1.25, false, 0.25}, {1.25, true, 0.2}, {0.8, false, 0.2}, {0.8, true, 0.25}} {
		if got := OverlayLoss(c.f, c.inverse); math.Abs(got-c.want) > 1e-12 {
			t.Fatalf("%v inverse %v: %v, want %v", c.f, c.inverse, got, c.want)
		}
	}
}
