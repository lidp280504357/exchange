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

// A threshold target (ASTRA design §3, §8.8; fixed seeds, coordinator
// 2026-10-04 06:20): the price gets there within the minute guard, with
// noise of 2.5 times the pace and a breather after three minutes its way,
// a quarter of the minutes at least against it; it crosses the level by
// the end, the event ends HIT, and the model goes on from there
// (re-anchored, saved), not back.
func TestAThresholdTargetHitsSlowlyAndFollows(t *testing.T) {
	for _, c := range []struct {
		level  string
		window time.Duration
	}{{"1.05", 10 * time.Minute}, {"1.08", 30 * time.Minute}} {
		r := newRig(t, nil)
		r.rounds(4)
		from := r.sim.Status().Target
		e := r.create(t, domain.Event{Type: domain.EventTarget, Price: d(c.level), Duration: c.window})
		if e.Direction != domain.Above || e.Then != domain.ThenFollow {
			t.Fatalf("inferred %+v", e)
		}
		var minutes []float64
		for i := 0; i < int((c.window+time.Minute)/(250*time.Millisecond)); i++ {
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
			got.CrossedAt.After(got.StartedAt.Add(c.window)) {
			t.Fatalf("+%s: the target %+v", c.level, got)
		}
		at, level := r.sim.Status().Target, d(c.level).InexactFloat64()
		if at < level || at > level*1.01 {
			t.Fatalf("+%s: crossed at %v", c.level, at)
		}
		// Within the minute guard, and both ways.
		against, prev := 0, from
		for i, p := range minutes {
			move := math.Log(p / prev)
			if math.Abs(move) > 0.03+1e-9 {
				t.Fatalf("+%s: minute %d moved %v", c.level, i, move)
			}
			if move < 0 {
				against++
			}
			prev = p
		}
		if float64(len(minutes)) < 0.8*c.window.Minutes() || float64(against) < 0.25*float64(len(minutes)) {
			t.Fatalf("+%s: %d of %d minutes against the target: %v", c.level, against, len(minutes), minutes)
		}
		// Re-anchored there and saved: the next minutes go on from it.
		if r.store.params == nil || math.Abs(r.store.params.P0-at)/at > 0.01 {
			t.Fatalf("+%s: the anchor saved: %+v", c.level, r.store.params)
		}
		r.rounds(4 * 60)
		if p := r.sim.Status().Target; math.Abs(p/at-1) > 0.01 {
			t.Fatalf("+%s: a minute later %v, crossed at %v", c.level, p, at)
		}
	}
}

// After three minutes its way, a target's next minute breathes: the
// minute's guide against it.
func TestATargetBreathes(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	e := r.create(t, domain.Event{Type: domain.EventTarget, Price: d("1.08"), Duration: 30 * time.Minute})
	breathers := 0
	for range 4 * 60 * 25 {
		r.rounds(1)
		for _, x := range r.sim.runningEvents() {
			if x.ID == e.ID && x.Breather == r.now.Unix()/60 && r.now.Second() == 30 && r.now.Nanosecond() == 0 {
				breathers++
				sh, _ := domain.ShapeOf([]*domain.Event{x}, r.now, r.sim.model.State.P, 0.03)
				if sh.Guide >= 0 {
					t.Fatalf("a breather minute pulls its way: %+v", sh)
				}
			}
		}
	}
	if breathers == 0 {
		t.Fatal("no breather in 25 minutes")
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

// Across seeds (the tuning of breatheAfter and the noise, coordinator
// 2026-10-04 06:20): every target of +3% in twelve minutes is HIT, and
// in nearly every one a quarter of the minutes or more go against it.
func TestTargetsHitAcrossSeeds(t *testing.T) {
	quarter := 0
	const seeds = 20
	for seed := uint64(1); seed <= seeds; seed++ {
		r := newSeededRig(t, nil, seed)
		r.rounds(4)
		from := r.sim.Status().Target
		e := r.create(t, domain.Event{Type: domain.EventTarget, Price: d("1.03"), Duration: 12 * time.Minute})
		var minutes []float64
		for i := 0; i < 4*60*13; i++ {
			r.rounds(1)
			if i%(4*60) == 4*60-1 {
				minutes = append(minutes, r.sim.Status().Target)
			}
			if r.store.event(e.ID).Status == domain.EventDone {
				break
			}
		}
		if got := r.store.event(e.ID); got.Result != domain.ResultHit {
			t.Fatalf("seed %d: %+v", seed, got)
		}
		against, prev := 0, from
		for _, p := range minutes {
			if p < prev {
				against++
			}
			prev = p
		}
		if 4*against >= len(minutes) {
			quarter++
		}
	}
	if quarter < seeds*85/100 {
		t.Fatalf("a quarter of the minutes against the target in %d of %d", quarter, seeds)
	}
}

// Review AW: a spike reaches at most as far as the quotes may go into the
// price band (10% × 0.7 here), whoever approved it; a target of the form
// before A6 (no direction, no window) is the jump it meant; one with a
// direction and too short a window is told the shortest.
func TestALegacyTargetJumpsAndASpikeStaysInTheBand(t *testing.T) {
	r := bandRig(t, nil)
	r.rounds(8)
	ctx := context.Background()
	_, err := r.sim.CreateEvent(ctx, domain.Event{Type: domain.EventSpike, Size: -0.08, ApprovedBy: "ops2", CreatedBy: "ops", Reason: "test"})
	if !apperrIs(err, "SIM_SPIKE_BEYOND_BAND") || !detailIs(err, "max", 0.07) {
		t.Fatalf("a spike of 8%% in a band of 10%%: %v", err)
	}
	// A legacy target made a jump brings no spikes: they would skip their
	// checks (review AX).
	_, err = r.sim.CreateEvent(ctx, domain.Event{Type: domain.EventTarget, Price: d("1.02"), Duration: 10 * time.Second, CreatedBy: "ops", Reason: "test"},
		domain.Event{StartsAt: r.now.Add(time.Minute), Size: 0.09, Width: 10 * time.Minute})
	if !apperrIs(err, "COMMON_INVALID_ARGUMENT") || len(r.store.events) != 0 {
		t.Fatalf("a legacy target with spikes: %v, %d events", err, len(r.store.events))
	}
	p := r.sim.Status().Target
	e := r.create(t, domain.Event{Type: domain.EventTarget, Price: d("1.02"), Duration: 10 * time.Second})
	if e.Type != domain.EventJump || math.Abs(e.Size-(1.02/p-1)) > 1e-6 || e.Duration != 10*time.Second {
		t.Fatalf("a legacy target: %+v (target %v)", e, p)
	}
	r.rounds(4 * 11)
	least := int(domain.MinWindow(r.sim.Status().Target, 1.04, 0.03).Seconds())
	_, err = r.sim.CreateEvent(ctx, domain.Event{
		Type: domain.EventTarget, Direction: domain.Above, Price: d("1.04"), Duration: 30 * time.Second, CreatedBy: "ops", Reason: "test",
	})
	if !apperrIs(err, "SIM_TARGET_INFEASIBLE") || !detailIs(err, "min_duration_seconds", least) || least < 60 {
		t.Fatalf("a target of thirty seconds: %v", err)
	}
}
