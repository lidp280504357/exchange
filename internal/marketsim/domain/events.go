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

// What one operator may do alone (§6.2): a jump of at most 30%, and moves
// of at most 50% in an hour together; beyond, a second operator approves.
const (
	SoloJump = 0.30
	SoloHour = 0.50
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
		if e.Size <= -0.9 || e.Size > 10 || e.Size == 0 || math.IsNaN(e.Size) {
			fail("a jump's size is a share above -0.9 and at most 10, not 0")
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

// Move is how far the event moves the target from p, as a share: a jump's
// size, the way to a target price; 0 for the others.
func (e Event) Move(p float64) float64 {
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
	}
	return 0
}

// NeedsApproval reports whether one operator may not create e alone at
// the target p: a move beyond SoloJump, or the moves of the hour (recent,
// the events created in the last hour) and e together beyond SoloHour.
func NeedsApproval(e Event, p float64, recent []Event) bool {
	move := math.Abs(e.Move(p))
	if move > SoloJump {
		return true
	}
	sum := move
	for _, r := range recent {
		if r.Status != EventCanceled {
			sum += math.Abs(r.Move(p))
		}
	}
	return sum > SoloHour
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
