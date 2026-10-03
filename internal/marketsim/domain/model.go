package domain

import (
	"math"
	"math/rand/v2"
	"time"
)

// State is the price model's state, saved so that a restart goes on from
// where it stopped: the reference prices at the anchor, the logarithms of
// the market factor (as last computed), the own deviation and the event
// factor, the last target and when, the targets of the last minute (for
// the guard) and the random source.
type State struct {
	BTC0   float64   `json:"btc0"`
	ETH0   float64   `json:"eth0"`
	LogM   float64   `json:"log_m"`
	X      float64   `json:"x"`
	LogE   float64   `json:"log_e"`
	P      float64   `json:"p"`
	At     time.Time `json:"at"`
	Minute []Mark    `json:"minute,omitempty"`
	RNG    []byte    `json:"rng,omitempty"`
	// Noise is the model's own random source (its deviation's draws), apart
	// from the bots': what the bots do, a spike's executors say, never
	// changes the path (ASTRA design §6.2, 2026-10-04).
	Noise []byte `json:"noise,omitempty"`
}

// Mark is a target price at a time.
type Mark struct {
	At time.Time `json:"at"`
	P  float64   `json:"p"`
}

// Guard names what bounded a step.
type Guard string

// What can bound the target.
const (
	GuardNone   Guard = ""
	GuardMinute Guard = "minute" // moved more than MaxMinuteMove in a minute
	GuardFloor  Guard = "floor"
	GuardCeil   Guard = "ceiling"
)

// maxStep is the longest time one step covers: after a pause (a restart)
// the model goes on as if no more had passed.
const maxStep = 5 * time.Second

// Model advances the target price (§3):
//
//	P(t) = P0 × M(t) × exp(X(t)) × E(t)
//
// M follows BTC's and ETH's returns since the anchor, weighted and times
// beta; X is an Ornstein–Uhlenbeck deviation of its own; E carries the
// operators' events (none yet: E is 1).
type Model struct {
	Params   Params
	State    State
	pcg      *rand.PCG
	rng      *rand.Rand
	noisePCG *rand.PCG
	noise    *rand.Rand
}

// NewModel goes on from st, its random sources restored, or seeded with
// seed when st has none (a first start; the model's own source alone after
// an upgrade that brought it).
func NewModel(p Params, st State, seed uint64) *Model {
	restore := func(saved []byte, a, b uint64) *rand.PCG {
		pcg := rand.NewPCG(a, b)
		if len(saved) > 0 {
			if err := pcg.UnmarshalBinary(saved); err != nil {
				pcg = rand.NewPCG(a, b)
			}
		}
		return pcg
	}
	pcg := restore(st.RNG, seed, seed^0x9e3779b97f4a7c15)
	noise := restore(st.Noise, seed^0x94d049bb133111eb, seed^0xbf58476d1ce4e5b9)
	//nolint:gosec // a simulation that repeats from its seed, no secret
	return &Model{Params: p, State: st, pcg: pcg, rng: rand.New(pcg), noisePCG: noise, noise: rand.New(noise)}
}

// Rand is the model's random source, which the bots share so that a run
// can be repeated from a seed.
func (m *Model) Rand() *rand.Rand { return m.rng }

// Snapshot is the state to save, with the random source's.
func (m *Model) Snapshot() State {
	st := m.State
	st.Minute = append([]Mark(nil), m.State.Minute...)
	st.RNG, _ = m.pcg.MarshalBinary()
	st.Noise, _ = m.noisePCG.MarshalBinary()
	return st
}

// Step advances the model to now under the running events' shape. btc and
// eth are the reference prices, 0 when not fresh: the market factor then
// keeps its last value, and a first fresh price anchors it. It returns
// the target, the model's price within the guards, and the guard that
// bounded it, if any.
func (m *Model) Step(now time.Time, btc, eth float64, sh Shape) (float64, Guard) {
	p, st := m.Params, &m.State
	dt := maxStep
	if !st.At.IsZero() {
		dt = min(max(now.Sub(st.At), 0), maxStep)
	}
	if st.BTC0 <= 0 && btc > 0 {
		st.BTC0 = btc
	}
	if st.ETH0 <= 0 && eth > 0 {
		st.ETH0 = eth
	}
	if btc > 0 && eth > 0 && st.BTC0 > 0 && st.ETH0 > 0 {
		st.LogM = p.Beta * (p.WBTC*math.Log(btc/st.BTC0) + p.WETH*math.Log(eth/st.ETH0))
	}
	mu, sigma := p.Mu, p.Sigma
	if sh.Mu != nil {
		mu = *sh.Mu
	}
	if sh.Vol > 0 {
		sigma *= sh.Vol
	}
	if sh.Closing {
		sigma /= 4
	}
	if sh.LogE != nil {
		st.LogE = *sh.LogE
	}
	st.LogE += sh.Guide*dt.Minutes() + sh.Push
	hours, days := dt.Hours(), dt.Hours()/24
	st.X += -p.Theta*st.X*hours + mu*days + sigma*math.Sqrt(days)*m.noise.NormFloat64()

	price := p.P0 * math.Exp(st.LogM+st.X+st.LogE)
	if sh.Pin > 0 {
		price = sh.Pin
	}
	guard := GuardNone
	// The minute's reference: the oldest target within the last minute.
	cut := now.Add(-time.Minute)
	keep := st.Minute[:0]
	for _, mk := range st.Minute {
		if !mk.At.Before(cut) {
			keep = append(keep, mk)
		}
	}
	st.Minute = keep
	ref := st.P
	if len(st.Minute) > 0 {
		ref = st.Minute[0].P
	}
	if sh.Moving {
		// An event moves the price: the guard starts again from it.
		ref, st.Minute = 0, st.Minute[:0]
	}
	if ref > 0 {
		lo, hi := ref*(1-p.MaxMinuteMove), ref*(1+p.MaxMinuteMove)
		if price < lo || price > hi {
			price, guard = math.Min(math.Max(price, lo), hi), GuardMinute
		}
	}
	switch {
	case price < p.Floor:
		price, guard = p.Floor, GuardFloor
	case price > p.Ceiling:
		price, guard = p.Ceiling, GuardCeil
	}
	// Only the target is bounded, not the model: after a jump of the market
	// the target catches up at MaxMinuteMove a minute.
	st.P, st.At = price, now
	st.Minute = append(st.Minute, Mark{At: now, P: price})
	return price, guard
}

// Hold makes the model's price p now, through the event factor: a pause
// or a target's hold ends where it held the price, and the model goes on
// from there.
func (m *Model) Hold(p float64) {
	if p > 0 && m.Params.P0 > 0 {
		m.State.LogE = math.Log(p/m.Params.P0) - m.State.LogM - m.State.X
	}
}

// Rebase makes the target p now and the model go on from there, the
// minute guard counting from p (the watchdog's way out of a locked
// market, ASTRA design §4): through the event factor, like Hold.
func (m *Model) Rebase(p float64) {
	if p <= 0 {
		return
	}
	m.Hold(p)
	m.State.P, m.State.Minute = p, nil
}

// Reanchor makes the current target the anchor: P0 is the target, the
// market factor starts again from the reference prices of now (0 when not
// fresh: the next fresh ones), and the deviation and events are spent.
func (m *Model) Reanchor(btc, eth float64) {
	if m.State.P > 0 {
		m.Params.P0 = m.State.P
	}
	m.State.BTC0, m.State.ETH0, m.State.LogM, m.State.X, m.State.LogE = btc, eth, 0, 0, 0
}
