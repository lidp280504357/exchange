package domain

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/shopspring/decimal"
)

// EventType is one of the operators' price events (ASTRA design §6.2).
type EventType string

// The events: a jump of the price by a share, at once or over a time; a
// threshold target (the price above or below a level by the end of a
// window, guided there slowly, §3); a spike of the printed price at a
// time; a trend (a drift per day); a volatility factor; a pause of the
// price; a halt of the pair; a new anchor at the current price.
const (
	EventJump       EventType = "JUMP"
	EventTarget     EventType = "TARGET"
	EventSpike      EventType = "SPIKE"
	EventTrend      EventType = "TREND"
	EventVolatility EventType = "VOLATILITY"
	EventPause      EventType = "PAUSE"
	EventHalt       EventType = "HALT"
	EventReanchor   EventType = "REANCHOR"
)

// A threshold target's direction (the price at or above the level by the
// end, or at or below), what follows its crossing (FOLLOW: the market
// again from where it is; HOLD: the level kept as a floor or a ceiling for
// Hold, then FOLLOW), and how it ended: crossed in time, not, or ended by
// an operator before it crossed (ASTRA design §3, §6.2, 2026-10-04).
const (
	Above = "ABOVE"
	Below = "BELOW"

	ThenFollow = "FOLLOW"
	ThenHold   = "HOLD"

	ResultHit      = "HIT"
	ResultMissed   = "MISSED"
	ResultCanceled = "CANCELED"
)

// A threshold target's rules (§3): it is feasible when the way to the
// level takes at most Feasibility of what the minute guard allows over the
// window (the rest is the noise's and the market's); its last
// ClosingShare of the window (at least MinClosing) closes in on the level;
// a window is at least MinTargetWindow. A spike (§6.2) moves the printed
// price by at most SoloSpike alone and MaxSpike approved, rises in
// SpikeRise and falls back over its width (DefaultSpikeWidth, at most
// MaxSpikeWidth); at most SpikesPerHour start in any hour.
const (
	Feasibility       = 0.6
	ClosingShare      = 0.1
	MinClosing        = time.Minute
	MinTargetWindow   = time.Minute
	SoloSpike         = 0.05
	MaxSpike          = 0.10
	SpikeRise         = 3 * time.Second
	DefaultSpikeWidth = 20 * time.Second
	MaxSpikeWidth     = time.Minute
	SpikesPerHour     = 6
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
	// What it does: a jump's or a spike's size (0.1 is +10%), a target's
	// level, a trend per day, a volatility factor.
	Size   float64
	Price  decimal.Decimal
	Mu     float64
	Factor float64
	// How long it takes (a jump: 0 at once; a trend, a volatility or a
	// pause: 0 until ended; a target: its window) and, for a target held
	// after its crossing, how long it holds the level (0: not at all).
	Duration time.Duration
	Hold     time.Duration
	StartsAt time.Time
	Status   string
	// A target's direction and what follows its crossing; a spike's width
	// and its target, when it is one's (the spikes a target was created
	// with).
	Direction string
	Then      string
	Width     time.Duration
	ParentID  string
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
	// A target's crossing of its level and how it ended.
	CrossedAt time.Time
	Result    string
}

// Start is when the event started, or is to start.
func (e Event) Start() time.Time {
	if !e.StartedAt.IsZero() {
		return e.StartedAt
	}
	return e.StartsAt
}

// EndsAt is when a target's window ends.
func (e Event) EndsAt() time.Time { return e.Start().Add(e.Duration) }

// ClosingAt is when a target's last ClosingShare of the window (at least
// MinClosing) starts: from then it closes in on the level.
func (e Event) ClosingAt() time.Time {
	c := max(time.Duration(float64(e.Duration)*ClosingShare), MinClosing)
	return e.EndsAt().Add(-min(c, e.Duration))
}

// HoldUntil is when a held target's hold ends, zero before its crossing.
func (e Event) HoldUntil() time.Time {
	if e.Then != ThenHold || e.CrossedAt.IsZero() {
		return time.Time{}
	}
	return e.CrossedAt.Add(e.Hold)
}

// Crossed reports whether p is at or beyond the target's level.
func (e Event) Crossed(p float64) bool {
	l := e.Price.InexactFloat64()
	if e.Direction == Below {
		return p <= l
	}
	return p >= l
}

// MinWindow is the shortest window in which a target may take the price
// from p to level, a minute guard of maxMinute allowing (§3).
func MinWindow(p, level, maxMinute float64) time.Duration {
	if p <= 0 || level <= 0 || maxMinute <= 0 {
		return 0
	}
	minutes := math.Ceil(math.Abs(math.Log(level/p)) / (Feasibility * maxMinute))
	return max(time.Duration(minutes)*time.Minute, MinTargetWindow)
}

// SpikeAt is a running spike's share of the planned price at now: it
// rises to Size in SpikeRise, then falls back to 0 over its width; 0
// outside.
func (e Event) SpikeAt(now time.Time) float64 {
	t := now.Sub(e.Start())
	switch {
	case t < 0 || t >= SpikeRise+e.Width:
		return 0
	case t < SpikeRise:
		return e.Size * t.Seconds() / SpikeRise.Seconds()
	}
	return e.Size * (1 - (t-SpikeRise).Seconds()/e.Width.Seconds())
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
			fail("a target needs a positive level")
		}
		if e.Direction != Above && e.Direction != Below {
			fail("a target's direction is ABOVE or BELOW")
		}
		if e.Duration < MinTargetWindow {
			fail("a target's window is a minute at least")
		}
		switch {
		case e.Then == ThenHold && e.Hold <= 0:
			fail("a target held after its crossing holds a while (hold_seconds)")
		case e.Then == ThenFollow && e.Hold != 0:
			fail("a target that follows the market after its crossing holds nothing")
		case e.Then != ThenFollow && e.Then != ThenHold:
			fail("then is FOLLOW or HOLD")
		}
	case EventSpike:
		if e.Size == 0 || math.Abs(e.Size) > MaxSpike || math.IsNaN(e.Size) {
			fail("a spike's size is a share within ±0.1, not 0")
		}
		if e.Width <= 0 || e.Width > MaxSpikeWidth {
			fail("a spike's width is above 0 and at most 60 seconds")
		}
		if e.Duration != 0 || e.Hold != 0 {
			fail("a spike has a width, no duration or hold")
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
		fail("type must be JUMP, TARGET, SPIKE, TREND, VOLATILITY, PAUSE, HALT or REANCHOR")
	}
	if e.Type != EventTarget && (e.Direction != "" || e.Then != "") {
		fail("only a target has a direction and a then")
	}
	if e.Type != EventSpike && (e.Width != 0 || e.ParentID != "") {
		fail("only a spike has a width and a target")
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

// Move is how far the event moves the price, for the guards: a jump's or
// a spike's size (a share); the way to a target's level from where it
// started (from p, the target now, before it starts), a logarithm (§6.2,
// 2026-10-04); a trend's drift over its time; a volatility factor's extra
// one-sigma move over its time (sigma being the model's volatility a day).
// A trend or a volatility counts a day at most, and a day when it has no
// end. The other events move nothing.
func (e Event) Move(p, sigma float64) float64 {
	days := 1.0
	if e.Duration > 0 {
		days = math.Min(1, e.Duration.Hours()/24)
	}
	switch e.Type {
	case EventJump, EventSpike:
		return e.Size
	case EventTarget:
		from := p
		if e.FromP.IsPositive() {
			from = e.FromP.InexactFloat64()
		}
		if from > 0 && e.Price.IsPositive() {
			return math.Log(e.Price.InexactFloat64() / from)
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

// Infer completes a target made before its direction and then were
// given: the direction toward the level from p, held when it held.
func (e *Event) Infer(p float64) {
	if e.Type != EventTarget {
		return
	}
	if e.Direction == "" {
		e.Direction = Above
		if l := e.Price.InexactFloat64(); p > 0 && l < p {
			e.Direction = Below
		}
	}
	if e.Then == "" {
		e.Then = ThenFollow
		if e.Hold > 0 {
			e.Then = ThenHold
		}
	}
}

// Guide is a target's pull at now on the price p (§3), a logarithm a
// minute: before its crossing, the way to the level spread over the
// minutes left, at most a third of the minute guard maxMinute, in pairs of
// minutes (UTC, as the candles), one of each pair against the way (minus
// it) and the other three times it: the way's pace on average, and a 1m
// candle in two against the target, which the noise alone (σ a day, 0.05%
// a minute) would not give (§8.8: a quarter at least); while it closes in,
// the way a little past the level within half the time left, at up to the
// whole minute guard, the noise a quarter; at the end
// of a window it did not cross in, a last push of the minute guard's size
// at most; after its crossing, held, a third of the minute guard back
// while the price is back past the level.
func (e Event) Guide(now time.Time, p, maxMinute float64) (guide float64, closing bool, push float64) {
	l := e.Price.InexactFloat64()
	if p <= 0 || l <= 0 || maxMinute <= 0 {
		return 0, false, 0
	}
	g, c := math.Log(l/p), maxMinute/3
	switch {
	case !e.CrossedAt.IsZero():
		if e.Then == ThenHold && now.Before(e.HoldUntil()) && !e.Crossed(p) {
			return math.Copysign(c, g), false, 0
		}
		return 0, false, 0
	case !now.Before(e.EndsAt()):
		return 0, false, clamp(g, maxMinute)
	case !now.Before(e.ClosingAt()):
		// Aimed a little past the level within half the time left (a way
		// that would arrive just at the end crosses or not as the noise
		// says): across before the end.
		over := math.Copysign(closingOvershoot, g)
		return clamp((g+over)/math.Max(e.EndsAt().Sub(now).Minutes()/2, 1.0/60), maxMinute), true, 0
	}
	way := clamp(g/math.Max(e.EndsAt().Sub(now).Minutes(), 1), c)
	if e.againstAt(now) {
		return -way, false, 0
	}
	return 3 * way, false, 0
}

// closingOvershoot is how far past its level a target closing in aims.
const closingOvershoot = 0.001

// againstAt reports whether the minute (UTC) of now is the one of its pair
// that goes against a target's way: one of the two, drawn from the
// target and the pair, so that a restart draws the same.
func (e Event) againstAt(now time.Time) bool {
	minute := now.Unix() / 60
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s/%d", e.ID, minute/2)
	return int64(h.Sum32()%2) == minute%2
}

func clamp(x, limit float64) float64 { return math.Max(-limit, math.Min(limit, x)) }

// Shape is what the running events do to the model's next step.
type Shape struct {
	// Mu, when set, replaces the drift; Vol, when positive, scales the
	// volatility.
	Mu  *float64
	Vol float64
	// LogE, when set, is the event factor's logarithm now.
	LogE *float64
	// Guide, a logarithm a minute, moves the event factor toward a
	// target's level; Closing quarters the noise while a target closes in;
	// Push moves the event factor once (a target's last step).
	Guide   float64
	Closing bool
	Push    float64
	// Spike is the share of the planned price the running spikes add to
	// where the makers quote (the plan itself unchanged).
	Spike float64
	// Pin, when positive, holds the target there (a pause, a target's way
	// and hold).
	Pin float64
	// Moving is true while an event moves or holds the price: the minute
	// guard stands aside and starts again from the event's price.
	Moving bool
	// Halted is true while a halt runs.
	Halted bool
}

// ShapeOf works out what the running events do at now to the target p,
// a minute guard of maxMinute allowing, and which of them end with this
// step (a target ends when its crossing or its window says, which the
// price after the step tells: not here).
func ShapeOf(running []*Event, now time.Time, p, maxMinute float64) (sh Shape, ended []*Event) {
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
			sh.Guide, sh.Closing, sh.Push = e.Guide(now, p, maxMinute)
		case EventSpike:
			sh.Spike += e.SpikeAt(now)
			if elapsed >= SpikeRise+e.Width {
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
