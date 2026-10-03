package application

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// detailIs reports whether err carries detail key with value.
func detailIs(err error, key string, value any) bool {
	var e *apperr.Error
	return errors.As(err, &e) && e.Details[key] == value
}

// A threshold target of +5% in ten minutes (ASTRA design §3, §8.8): the
// price gets there slowly, at most a third of the minute guard a minute
// before it closes in, with a minute in two against the way; it crosses
// the level by the end, the event ends HIT, and the model goes on from
// there (re-anchored, saved), not back.
func TestAThresholdTargetHitsSlowlyAndFollows(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	from := r.sim.Status().Target
	e := r.create(t, domain.Event{Type: domain.EventTarget, Price: d("1.05"), Duration: 10 * time.Minute})
	if e.Direction != domain.Above || e.Then != domain.ThenFollow {
		t.Fatalf("inferred %+v", e)
	}
	var minutes []float64
	for i := 0; i < 4*60*11; i++ {
		r.rounds(1)
		if i%(4*60) == 4*60-1 {
			minutes = append(minutes, r.sim.Status().Target)
		}
		if got := r.store.event(e.ID); got.Status == domain.EventDone {
			break
		}
	}
	got := r.store.event(e.ID)
	if got.Status != domain.EventDone || got.Result != domain.ResultHit || got.CrossedAt.IsZero() ||
		got.CrossedAt.After(got.StartedAt.Add(10*time.Minute)) {
		t.Fatalf("the target: %+v", got)
	}
	at := r.sim.Status().Target
	if at < 1.05 || at > 1.05*1.01 {
		t.Fatalf("crossed at %v", at)
	}
	// Slowly, and both ways: no minute beyond the guard's third (bar the
	// last, closing in); at least a quarter of the minutes against it.
	against, prev := 0, from
	for i, p := range minutes {
		move := math.Log(p / prev)
		if i < len(minutes)-1 && math.Abs(move) > 0.03/3*3+1e-9 {
			t.Fatalf("minute %d moved %v", i, move)
		}
		if move < 0 {
			against++
		}
		prev = p
	}
	if len(minutes) < 8 || float64(against) < 0.25*float64(len(minutes)) {
		t.Fatalf("%d of %d minutes against the target: %v", against, len(minutes), minutes)
	}
	// Re-anchored there and saved: the next minutes go on from it.
	if r.store.params == nil || math.Abs(r.store.params.P0-at)/at > 0.01 {
		t.Fatalf("the anchor saved: %+v", r.store.params)
	}
	r.rounds(4 * 60)
	if p := r.sim.Status().Target; math.Abs(p/at-1) > 0.01 {
		t.Fatalf("a minute later %v, crossed at %v", p, at)
	}
}

// BTC and ETH fall 12% at once while a target needs +3% in three
// minutes (§3): it is at risk, and at the end of its window, not
// crossed, it gets a last push and ends MISSED.
func TestATargetTheMarketRunsAwayFromIsMissed(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	e := r.create(t, domain.Event{Type: domain.EventTarget, Direction: domain.Above, Price: d("1.03"), Duration: 3 * time.Minute})
	r.rounds(4 * 10)
	r.prices.mu.Lock()
	r.prices.btc, r.prices.eth = d("52800"), d("2640")
	r.prices.mu.Unlock()
	risky := false
	for range 4 * 60 * 3 {
		r.rounds(1)
		risky = risky || r.sim.targetAtRisk
		if got := r.store.event(e.ID); got.Status == domain.EventDone {
			break
		}
	}
	got := r.store.event(e.ID)
	if got.Status != domain.EventDone || got.Result != domain.ResultMissed || !got.CrossedAt.IsZero() || !risky {
		t.Fatalf("the target: %+v, at risk %v", got, risky)
	}
}

// A target below, held for two minutes after its crossing: HIT and still
// running while it holds, done after.
func TestAHeldTarget(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	e := r.create(t, domain.Event{Type: domain.EventTarget, Price: d("0.98"), Duration: 4 * time.Minute, Hold: 2 * time.Minute})
	if e.Direction != domain.Below || e.Then != domain.ThenHold {
		t.Fatalf("inferred %+v", e)
	}
	for range 4 * 60 * 5 {
		r.rounds(1)
		if !r.store.event(e.ID).CrossedAt.IsZero() {
			break
		}
	}
	got := r.store.event(e.ID)
	if got.Result != domain.ResultHit || got.Status != domain.EventRunning {
		t.Fatalf("crossed: %+v", got)
	}
	r.rounds(4 * 60)
	if p := r.sim.Status().Target; p > 0.98*1.003 {
		t.Fatalf("held below the level: %v", p)
	}
	r.rounds(4*60 + 4)
	if got := r.store.event(e.ID); got.Status != domain.EventDone || got.Result != domain.ResultHit {
		t.Fatalf("after the hold: %+v", got)
	}
}

// What a target and a spike may not do (§6.2): a window too short for the
// way (the shortest given), more than one operator's share, a jump or a
// trend over a target, a spike beyond 5% alone or 10% at all, while a
// target closes in, or a seventh in an hour; a target's spikes come with
// it and go with it.
func TestTheTargetsAndSpikesGuards(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	ctx := context.Background()
	create := func(e domain.Event, spikes ...domain.Event) error {
		if e.CreatedBy == "" {
			e.CreatedBy, e.Reason = "ops", "test"
		}
		_, err := r.sim.CreateEvent(ctx, e, spikes...)
		return err
	}
	err := create(domain.Event{Type: domain.EventTarget, Price: d("1.2"), Duration: 2 * time.Minute})
	if !apperrIs(err, "SIM_TARGET_INFEASIBLE") || !detailIs(err, "min_duration_seconds", 660) {
		t.Fatalf("too short: %v", err)
	}
	if err := create(domain.Event{Type: domain.EventTarget, Price: d("1.36"), Duration: time.Hour}); !apperrIs(err, "SIM_EVENT_NEEDS_APPROVAL") {
		t.Fatalf("ln 1.36 alone: %v", err)
	}
	now := r.now
	target, err := r.sim.CreateEvent(ctx, domain.Event{
		Type: domain.EventTarget, Price: d("1.03"), Duration: 20 * time.Minute, CreatedBy: "ops", Reason: "test",
	}, domain.Event{StartsAt: now.Add(5 * time.Minute), Size: -0.04}, domain.Event{StartsAt: now.Add(10 * time.Minute), Size: 0.03, Width: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	spikes := r.sim.SpikesOf(target.ID)
	if len(spikes) != 2 || spikes[0].ParentID != target.ID || spikes[0].Width != domain.DefaultSpikeWidth || spikes[1].Width != 30*time.Second {
		t.Fatalf("its spikes %+v", spikes)
	}
	if err := create(domain.Event{Type: domain.EventJump, Size: 0.02}); !apperrIs(err, "SIM_TARGET_RUNNING") {
		t.Fatalf("a jump over the target: %v", err)
	}
	if err := create(domain.Event{Type: domain.EventTrend, Mu: 0.1, StartsAt: now.Add(15 * time.Minute)}); !apperrIs(err, "SIM_TARGET_RUNNING") {
		t.Fatalf("a trend over the target: %v", err)
	}
	if err := create(domain.Event{Type: domain.EventSpike, Size: 0.02, StartsAt: now.Add(19 * time.Minute)}); !apperrIs(err, "SIM_SPIKE_IN_CLOSING") {
		t.Fatalf("a spike in the closing window: %v", err)
	}
	if err := create(domain.Event{Type: domain.EventSpike, Size: 0.06, StartsAt: now.Add(3 * time.Minute)}); !apperrIs(err, "SIM_EVENT_NEEDS_APPROVAL") {
		t.Fatalf("6%% alone: %v", err)
	}
	if err := create(domain.Event{Type: domain.EventSpike, Size: 0.11, ApprovedBy: "ops2", StartsAt: now.Add(3 * time.Minute)}); !apperrIs(err, "COMMON_INVALID_ARGUMENT") {
		t.Fatalf("11%%: %v", err)
	}
	for i := range 4 {
		if err := create(domain.Event{Type: domain.EventSpike, Size: 0.01, StartsAt: now.Add(time.Duration(2+i) * time.Minute)}); err != nil {
			t.Fatalf("spike %d: %v", i+3, err)
		}
	}
	if err := create(domain.Event{Type: domain.EventSpike, Size: 0.01, StartsAt: now.Add(8 * time.Minute)}); !apperrIs(err, "SIM_SPIKES_PER_HOUR") {
		t.Fatalf("a seventh in the hour: %v", err)
	}
	if _, err := r.sim.EndEvent(ctx, target.ID, "ops", "changed my mind"); err != nil {
		t.Fatal(err)
	}
	if got := r.store.event(target.ID); got.Result != domain.ResultCanceled || len(r.sim.SpikesOf(target.ID)) != 0 ||
		r.store.event(spikes[0].ID).Status != domain.EventCanceled {
		t.Fatalf("ended early: %+v", got)
	}
}

// A spike moves where the makers quote, to its tip in a few seconds and
// back over its width, and leaves the plan alone: the target goes the way
// it goes without the spike (§6.2, §8.8).
func TestASpikeLeavesThePlanAlone(t *testing.T) {
	run := func(spike bool) (tip, after float64, targets []float64) {
		r := newRig(t, nil)
		r.rounds(8)
		start := r.now.Add(time.Second)
		if spike {
			r.create(t, domain.Event{Type: domain.EventSpike, Size: -0.04, StartsAt: start})
		}
		for i := 0; i < 4*90; i++ {
			r.rounds(1)
			st := r.sim.Status()
			targets = append(targets, st.Target)
			if r.now.Equal(start.Add(domain.SpikeRise + time.Second)) {
				tip = st.Center / st.Target
			}
		}
		st := r.sim.Status()
		return tip, st.Center / st.Target, targets
	}
	tip, after, spiked := run(true)
	_, _, plain := run(false)
	if math.Abs(tip-0.96*(1+0.04/20)) > 0.003 || math.Abs(after-1) > 1e-9 {
		t.Fatalf("the quotes at the tip %v, after %v", tip, after)
	}
	for i := range plain {
		if spiked[i] != plain[i] {
			t.Fatalf("round %d: the target %v with the spike, %v without", i, spiked[i], plain[i])
		}
	}
}
