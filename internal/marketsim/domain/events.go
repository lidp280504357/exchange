package domain

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"
)

// EventType is one of the operators' price events (ASTRA design §6.2).
type EventType string

// The events: a jump of the price by a share, at once or over a time; a
// target price reached along an exponential path and held there; a trend
// (a drift per day); a volatility factor; a pause of the price; a halt of
// the pair; a new anchor at the current price.
const (
	EventJump       EventType = "JUMP"
	EventTarget     EventType = "TARGET"
	EventTrend      EventType = "TREND"
	EventVolatility EventType = "VOLATILITY"
	EventPause      EventType = "PAUSE"
	EventHalt       EventType = "HALT"
	EventReanchor   EventType = "REANCHOR"
)

// An event's course: scheduled, running, done (ended by itself or by an
// operator), or canceled before it started.
const (
	EventScheduled = "SCHEDULED"
	EventRunning   = "RUNNING"
	EventDone      = "DONE"
	EventCanceled  = "CANCELED"
)

// The guards (§6.2, clarified after the A3 review): one operator alone
// moves the price by at most 30% at a time (a jump, a target, the
// equivalent of a trend or a volatility, a change of the settings) and by
// at most 50% within any hour, counted by when the moves take effect, and
// changes the day's turnover by at most a factor of two within any hour;
// beyond, a second operator approves. Even approved, a jump or a target
// moves the price by at most +100% (and less than −90%), and an event is
// scheduled at most MaxLead ahead.
const (
	SoloJump   = 0.30
	SoloHour   = 0.50
	SoloVolume = math.Ln2
	MaxJump    = 1.0
	MaxLead    = 24 * time.Hour
)

// Event is an operator's price event and its course.
type Event struct {
	ID   string
	Type EventType
	// What it does: a jump's size (0.1 is +10%), a target price, a trend
	// per day, a volatility factor.
	Size   float64
	Price  decimal.Decimal
	Mu     float64
	Factor float64
	// How long it takes (a jump: 0 at once; a trend, a volatility or a
	// pause: 0 until ended; a target: the way there) and, for a target,
	// how long it holds the price there (0: not at all).
	Duration time.Duration
	Hold     time.Duration
	StartsAt time.Time
	Status   string
	// Who created it, who approved it (beyond one operator's limits), why.
	CreatedBy  string
	ApprovedBy string
	Reason     string
	CreatedAt  time.Time
	// Its course: when it started and ended, the event factor (log) and
	// the target when it started, who ended it early.
	StartedAt time.Time
	EndedAt   time.Time
	FromLogE  float64
	FromP     decimal.Decimal
	EndedBy   string
}

// Validate checks an event's settings.
func (e Event) Validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if e.Duration < 0 || e.Hold < 0 || e.Duration > 24*time.Hour || e.Hold > 24*time.Hour {
		fail("duration and hold are between 0 and 24 hours")
	}
	switch e.Type {
	case EventJump:
		if e.Size <= -0.9 || e.Size > MaxJump || e.Size == 0 || math.IsNaN(e.Size) {
			fail("a jump's size is a share above -0.9 and at most 1 (+100%%), not 0")
		}
		if e.Duration > 10*time.Minute {
			fail("a jump takes at most 10 minutes")
		}
	case EventTarget:
		if !e.Price.IsPositive() {
			fail("a target needs a positive price")
		}
	case EventTrend:
		if math.Abs(e.Mu) > 1 || math.IsNaN(e.Mu) {
			fail("a trend is within ±1 a day")
		}
	case EventVolatility:
		if e.Factor <= 0 || e.Factor > 20 || math.IsNaN(e.Factor) {
			fail("a volatility factor is above 0 and at most 20")
		}
	case EventPause, EventHalt, EventReanchor:
	default:
		fail("type must be JUMP, TARGET, TREND, VOLATILITY, PAUSE, HALT or REANCHOR")
	}
	if e.CreatedBy == "" || len(e.Reason) < 3 {
		fail("the operator and a reason are required")
	}
	return errors.Join(errs...)
}

// Moves reports whether the event moves or holds the price: one such
// event runs at a time.
func (e Event) Moves() bool {
	return e.Type == EventJump || e.Type == EventTarget || e.Type == EventPause
}

// Move is how far the event moves the price, as a share, for the guards:
// a jump's size; the way to a target price from where it started (from p,
// the target now, before it starts); a trend's drift over its time; a
// volatility factor's extra one-sigma move over its time (sigma being the
// model's volatility a day). A trend or a volatility counts a day at most,
// and a day when it has no end. The other events move nothing.
func (e Event) Move(p, sigma float64) float64 {
	days := 1.0
	if e.Duration > 0 {
		days = math.Min(1, e.Duration.Hours()/24)
	}
	switch e.Type {
	case EventJump:
		return e.Size
	case EventTarget:
		from := p
		if e.FromP.IsPositive() {
			from = e.FromP.InexactFloat64()
		}
		if from > 0 {
			return e.Price.InexactFloat64()/from - 1
		}
	case EventTrend:
		return math.Expm1(e.Mu * days)
	case EventVolatility:
		return sigma * math.Abs(e.Factor-1) * math.Sqrt(days)
	}
	return 0
}

// Spend is what a move of the operators takes from the guards' budget: a
// move of the price (a share) and of the day's turnover (a logarithm), at
// the time it takes effect.
type Spend struct {
	At           time.Time
	Move, Volume float64
}

// NeedsApproval reports whether one operator may not make s alone, given
// the others made or scheduled: a move beyond SoloJump, or, within some
// hour that holds s, the moves together beyond SoloHour or the turnover's
// changes beyond SoloVolume.
func NeedsApproval(s Spend, others []Spend) bool {
	if math.Abs(s.Move) > SoloJump || math.Abs(s.Volume) > SoloVolume {
		return true
	}
	// The hours that hold s start between an hour before it and s itself;
	// which spends they hold changes only where an hour starts or ends at
	// one of them.
	lo, hi := s.At.Add(-time.Hour), s.At
	starts := []time.Time{lo, hi}
	for _, o := range others {
		for _, w := range []time.Time{o.At, o.At.Add(-time.Hour)} {
			if !w.Before(lo) && !w.After(hi) {
				starts = append(starts, w)
			}
		}
	}
	for _, w := range starts {
		move, volume := math.Abs(s.Move), math.Abs(s.Volume)
		for _, o := range others {
			if !o.At.Before(w) && !o.At.After(w.Add(time.Hour)) {
				move, volume = move+math.Abs(o.Move), volume+math.Abs(o.Volume)
			}
		}
		if move > SoloHour+1e-12 || volume > SoloVolume+1e-12 {
			return true
		}
	}
	return false
}

// ParamsMove is how far a change of the settings moves the price, for the
// guards: the anchor P0's change, the target p forced into a new floor or
// ceiling, and the extra move a minute a higher max_minute_move allows.
func ParamsMove(from, to Params, p float64) float64 {
	m := math.Max(0, to.MaxMinuteMove-from.MaxMinuteMove)
	if from.P0 > 0 {
		m += math.Abs(to.P0/from.P0 - 1)
	}
	if p > 0 {
		m += math.Abs(math.Min(math.Max(p, to.Floor), to.Ceiling)/p - 1)
	}
	return m
}

// VolumeMove is how far a change of the settings moves the day's taker
// turnover, a logarithm: stopping the takers or starting them from none
// counts as twice what one operator may do alone.
func VolumeMove(from, to Params) float64 {
	switch {
	case from.DailyVolume == to.DailyVolume:
		return 0
	case from.DailyVolume <= 0 || to.DailyVolume <= 0:
		return 2 * SoloVolume
	}
	return math.Abs(math.Log(to.DailyVolume / from.DailyVolume))
}

// Shape is what the running events do to the model's next step.
type Shape struct {
	// Mu, when set, replaces the drift; Vol, when positive, scales the
	// volatility.
	Mu  *float64
	Vol float64
	// LogE, when set, is the event factor's logarithm now.
	LogE *float64
	// Pin, when positive, holds the target there (a pause, a target's way
	// and hold).
	Pin float64
	// Moving is true while an event moves or holds the price: the minute
	// guard stands aside and starts again from the event's price.
	Moving bool
	// Halted is true while a halt runs.
	Halted bool
}

// ShapeOf works out what the running events do at now, and which of them
// end with this step.
func ShapeOf(running []*Event, now time.Time) (sh Shape, ended []*Event) {
	for _, e := range running {
		elapsed := now.Sub(e.StartedAt)
		over := func(d time.Duration) bool { return d > 0 && elapsed >= d }
		progress := 1.0
		if e.Duration > 0 {
			progress = math.Min(1, math.Max(0, elapsed.Seconds()/e.Duration.Seconds()))
		}
		switch e.Type {
		case EventJump:
			logE := e.FromLogE + math.Log1p(e.Size)*progress
			sh.LogE, sh.Moving = &logE, true
			if progress >= 1 {
				ended = append(ended, e)
			}
		case EventTarget:
			from, to := e.FromP.InexactFloat64(), e.Price.InexactFloat64()
			if from <= 0 || to <= 0 {
				ended = append(ended, e)
				continue
			}
			sh.Pin, sh.Moving = from*math.Pow(to/from, progress), true
			if progress >= 1 && (e.Hold == 0 || elapsed >= e.Duration+e.Hold) {
				ended = append(ended, e)
			}
		case EventTrend:
			mu := e.Mu
			sh.Mu = &mu
			if over(e.Duration) {
				ended = append(ended, e)
			}
		case EventVolatility:
			sh.Vol = e.Factor
			if over(e.Duration) {
				ended = append(ended, e)
			}
		case EventPause:
			sh.Pin, sh.Moving = e.FromP.InexactFloat64(), true
			if over(e.Duration) {
				ended = append(ended, e)
			}
		case EventHalt:
			sh.Halted = true
		case EventReanchor:
			ended = append(ended, e)
		}
	}
	return sh, ended
}
