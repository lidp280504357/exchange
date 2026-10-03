package domain

import (
	"math"
	"time"
)

// planMarketVol is the market factor's volatility a day (the square root
// of a day) a target's plan allows for around its path: BTC's and ETH's
// returns, about 3% a day, times beta.
const planMarketVol = 0.03

// PlanPoint is a target's planned price at a time, and the band around
// it the noise and the market factor keep the price in about 95 times in
// a hundred (two standard deviations).
type PlanPoint struct {
	At              time.Time
	Plan, Low, High float64
}

// Plan is a threshold target's plan from the price from at its start
// (§3): the guide's way alone, minute by minute (spread over the minutes
// left, a third of the minute guard at most, all of it while it closes
// in), which is where the guide's pairs of minutes, the noise and the
// market leave the price on average; around it the band their volatility
// makes (the noise a quarter while it closes in) and the two minutes'
// ways a pair of minutes strays by. A point a minute, the window's start
// and end included.
func Plan(e Event, from float64, p Params) []PlanPoint {
	if from <= 0 || !e.Price.IsPositive() || e.Duration <= 0 {
		return nil
	}
	level, start, end := math.Log(e.Price.InexactFloat64()), e.Start(), e.EndsAt()
	noise := p.Sigma / math.Sqrt(24*60)
	market := planMarketVol * p.Beta / math.Sqrt(24*60)
	x, variance := math.Log(from), 0.0
	out := []PlanPoint{{At: start, Plan: from, Low: from, High: from}}
	for at := start; at.Before(end); {
		next := at.Add(time.Minute)
		if next.After(end) {
			next = end
		}
		minutes := next.Sub(at).Minutes()
		way, sd := clamp((level-x)/math.Max(end.Sub(at).Minutes(), 1), p.MaxMinuteMove/3), noise
		if !at.Before(e.ClosingAt()) {
			// Closing in: across, a little past the level, at up to the
			// whole minute guard.
			way = clamp((level-x+math.Copysign(closingOvershoot, level-x))/minutes, p.MaxMinuteMove)
			sd = noise / 4
		}
		x += way * minutes
		variance += (sd*sd + market*market) * minutes
		band := 2*math.Sqrt(variance) + 2*math.Abs(way)
		out = append(out, PlanPoint{At: next, Plan: math.Exp(x), Low: math.Exp(x - band), High: math.Exp(x + band)})
		at = next
	}
	return out
}

// PlanAt is the plan at t, between its points (log-linear); the first or
// the last outside them.
func PlanAt(points []PlanPoint, t time.Time) PlanPoint {
	if len(points) == 0 {
		return PlanPoint{}
	}
	if !t.After(points[0].At) {
		return points[0]
	}
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if t.After(b.At) {
			continue
		}
		f := t.Sub(a.At).Seconds() / b.At.Sub(a.At).Seconds()
		mix := func(x, y float64) float64 { return math.Exp(math.Log(x) + (math.Log(y)-math.Log(x))*f) }
		return PlanPoint{At: t, Plan: mix(a.Plan, b.Plan), Low: mix(a.Low, b.Low), High: mix(a.High, b.High)}
	}
	return points[len(points)-1]
}

// AtRisk reports whether a target that has not crossed its level can no
// longer reach it from p by the end of its window even at the minute
// guard's pace (BTC and ETH having pushed the price away): an operator
// may lengthen or cancel it (§3, MarketSimTargetAtRisk).
func (e Event) AtRisk(now time.Time, p, maxMinute float64) bool {
	if e.Type != EventTarget || !e.CrossedAt.IsZero() || e.Crossed(p) || p <= 0 || !e.Price.IsPositive() {
		return false
	}
	left := math.Max(e.EndsAt().Sub(now).Minutes(), 0)
	return math.Abs(math.Log(e.Price.InexactFloat64()/p)) > maxMinute*left
}
