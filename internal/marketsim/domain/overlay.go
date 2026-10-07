package domain

import (
	"math"
	"time"
)

// Price events on the pairs a reference market follows (design 2026-10-07,
// general price control; J0 contract §3): an OVERLAY event has market-data
// multiply one pair's reference data on the platform by a factor that
// ramps from 1 to its target in RampUp, holds it for Hold and ramps back
// to 1 in RampDown, the reference market's own moves riding on top; back
// at 1 the pair is the reference market's again.
const (
	// OverlayMinFactor and OverlayMaxFactor are the hard limits (±90%).
	OverlayMinFactor = 0.1
	OverlayMaxFactor = 1.9
	// OverlayMaxSymbols is how many pairs one request names at most.
	OverlayMaxSymbols = 10
	// OverlayMinRampDown is the shortest way back to 1.
	OverlayMinRampDown = 3 * time.Second
	// OverlayMaxTotal is the longest event (its ramps and hold); the
	// service may allow less (OVERLAY_MAX_SECONDS).
	OverlayMaxTotal = 10 * time.Minute
	// OverlayEndRamp is how long an event ended early takes back to 1
	// from where it stood (the console's "restore now").
	OverlayEndRamp = 3 * time.Second
)

// OverlayTotal is how long an OVERLAY event runs on its schedule.
func (e Event) OverlayTotal() time.Duration { return e.RampUp + e.Hold + e.RampDown }

// OverlayEnds is when a started OVERLAY is to be back at 1: at the end of
// its schedule, or of its way back after an operator ended it.
func (e Event) OverlayEnds() time.Time {
	end := e.Start().Add(e.OverlayTotal())
	if !e.EndedAt.IsZero() && e.EndedAt.Add(OverlayEndRamp).Before(end) {
		return e.EndedAt.Add(OverlayEndRamp)
	}
	return end
}

// overlayPlanned is an OVERLAY's factor at now on its schedule from its
// start: 1 before, linear up to TargetFactor in RampUp, held, linear back
// down to 1 in RampDown, and 1 (done) after.
func (e Event) overlayPlanned(now time.Time) (float64, bool) {
	t := now.Sub(e.Start())
	f := e.TargetFactor
	switch {
	case t < 0:
		return 1, false
	case t < e.RampUp:
		return 1 + (f-1)*t.Seconds()/e.RampUp.Seconds(), false
	case t < e.RampUp+e.Hold:
		return f, false
	case t < e.OverlayTotal():
		return f + (1-f)*(t-e.RampUp-e.Hold).Seconds()/e.RampDown.Seconds(), false
	}
	return 1, true
}

// OverlayAt is a running OVERLAY's factor at now and whether it is back
// at 1 for good. One an operator ended (EndedAt set while it runs) goes
// from where it stood then back to 1 in OverlayEndRamp, or along its
// schedule when that is closer to 1.
func (e Event) OverlayAt(now time.Time) (float64, bool) {
	f, done := e.overlayPlanned(now)
	if e.EndedAt.IsZero() || done {
		return f, done
	}
	from, _ := e.overlayPlanned(e.EndedAt)
	u := now.Sub(e.EndedAt)
	if u >= OverlayEndRamp {
		return 1, true
	}
	back := from
	if u > 0 {
		back = from + (1-from)*u.Seconds()/OverlayEndRamp.Seconds()
	}
	if math.Abs(back-1) < math.Abs(f-1) {
		return back, false
	}
	return f, false
}

// OverlayProgress is how far a running OVERLAY is through its schedule,
// 0 to 1 (an early end's way back counts as the rest).
func (e Event) OverlayProgress(now time.Time) float64 {
	total := e.OverlayTotal()
	if e.Status != EventRunning || total <= 0 {
		if e.Status == EventDone {
			return 1
		}
		return 0
	}
	if !e.EndedAt.IsZero() {
		done := e.EndedAt.Sub(e.Start()).Seconds() / total.Seconds()
		return math.Min(1, done+(1-done)*math.Min(1, now.Sub(e.EndedAt).Seconds()/OverlayEndRamp.Seconds()))
	}
	return math.Min(1, math.Max(0, now.Sub(e.Start()).Seconds()/total.Seconds()))
}

// OverlayLoss is the worst share of a unit's value HOUSE loses on a unit it
// takes at the factor f and holds back to 1 (J0 contract §3.3): above 1
// it buys what users sell high (f-1), below it sells what they buy low
// (1-f); an inverse contract (worth a fixed face in its coin) loses
// 1-1/f and 1/f-1.
func OverlayLoss(f float64, inverse bool) float64 {
	switch {
	case f >= 1 && inverse:
		return 1 - 1/f
	case f >= 1:
		return f - 1
	case inverse:
		return 1/f - 1
	}
	return 1 - f
}
