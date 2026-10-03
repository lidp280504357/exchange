package application

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Threshold targets and spikes (ASTRA design §3, §6.2, batch A6,
// 2026-10-04): a target guides the price above or below a level by the
// end of its window, slowly, the noise and the market left in; a spike
// moves the printed price for a few seconds and leaves the plan alone.
var (
	ErrTargetInfeasible = apperr.New(apperr.KindInvalid, "SIM_TARGET_INFEASIBLE",
		"the window is too short to reach the level slowly: at least min_duration_seconds")
	ErrTargetRunning = apperr.New(apperr.KindConflict, "SIM_TARGET_RUNNING",
		"a threshold target runs or is scheduled then: cancel it before a jump or a trend")
	ErrSpikesPerHour = apperr.New(apperr.KindConflict, "SIM_SPIKES_PER_HOUR",
		"at most 6 spikes start in any hour")
	ErrSpikeInClosing = apperr.New(apperr.KindConflict, "SIM_SPIKE_IN_CLOSING",
		"a spike may not start while a target closes in on its level (the last 10% of its window)")
	ErrSpikeBeyondBand = apperr.New(apperr.KindInvalid, "SIM_SPIKE_BEYOND_BAND",
		"a spike reaches at most as far as the quotes may go into the price band (details.max)")
)

// openTargets are the scheduled and running threshold targets.
func (s *Sim) openTargets() []*domain.Event {
	var out []*domain.Event
	for _, e := range s.events {
		if e.Type == domain.EventTarget && (e.Status == domain.EventScheduled || e.Status == domain.EventRunning) {
			out = append(out, e)
		}
	}
	return out
}

// targetOver reports whether an open target's window or hold holds t: a
// jump or a trend then is refused (§6.2).
func (s *Sim) targetOver(t time.Time) bool {
	return slices.ContainsFunc(s.openTargets(), func(e *domain.Event) bool {
		return !t.Before(e.Start()) && !t.After(e.EndsAt().Add(e.Hold))
	})
}

// checkTarget completes and checks a new threshold target against the
// target p: its direction and then, the hard cap, and a window long
// enough to get there slowly (§3), the shortest one told when it is not.
func (s *Sim) checkTarget(e *domain.Event, p float64) error {
	e.Infer(p)
	least := domain.MinWindow(p, e.Price.InexactFloat64(), s.params.MaxMinuteMove)
	if e.Duration < domain.MinTargetWindow && e.Price.IsPositive() {
		return ErrTargetInfeasible.WithDetail("min_duration_seconds", int(least.Seconds()))
	}
	if err := e.Validate(); err != nil {
		return apperr.Invalid(err.Error())
	}
	if l := e.Price.InexactFloat64(); p > 0 && (l/p-1 > domain.MaxJump || l/p-1 <= -0.9) {
		return apperr.Invalid("a target moves the price by at most +100% and by less than -90%")
	}
	if e.Duration < least {
		return ErrTargetInfeasible.WithDetail("min_duration_seconds", int(least.Seconds()))
	}
	return nil
}

// legacyJump makes a target of the form before A6 (no direction, a way
// shorter than a target's window) the jump it meant: to its price over its
// duration, at once when none (coordinator 2026-10-04 06:20).
func legacyJump(e *domain.Event, p float64) bool {
	if e.Type != domain.EventTarget || e.Direction != "" || e.Duration >= domain.MinTargetWindow || p <= 0 || !e.Price.IsPositive() {
		return false
	}
	e.Type, e.Size, e.Price, e.Hold, e.Then = domain.EventJump, math.Round((e.Price.InexactFloat64()/p-1)*1e6)/1e6, decimal.Zero, 0, ""
	return true
}

// checkSpikes checks spikes starting when they say, a target's when parent
// is set (the new target they come with): within its window, before it
// closes in; none while an open target closes in; at most SpikesPerHour
// in any hour, the ones stored and the new ones together; beyond
// SoloSpike, approved.
func (s *Sim) checkSpikes(ctx context.Context, spikes []domain.Event, parent *domain.Event) error {
	targets := s.openTargets()
	if parent != nil {
		targets = append(targets, parent)
	}
	for i := range spikes {
		x := &spikes[i]
		if err := x.Validate(); err != nil {
			return apperr.Invalid(err.Error())
		}
		if band := s.pair.Band; band > 0 && math.Abs(x.Size) > band*domain.BandReach+1e-12 {
			return ErrSpikeBeyondBand.WithDetail("max", math.Round(band*domain.BandReach*1e4)/1e4) // review AW
		}
		if math.Abs(x.Size) > domain.SoloSpike && (x.ApprovedBy == "" || x.ApprovedBy == x.CreatedBy) {
			return ErrNeedsApproval.WithDetail("move", x.Size)
		}
		if parent != nil && (x.StartsAt.Before(parent.Start()) || !x.StartsAt.Before(parent.EndsAt())) {
			return apperr.Invalid("a target's spike starts within its window")
		}
		for _, t := range targets {
			if !x.StartsAt.Before(t.ClosingAt()) && !x.StartsAt.After(t.EndsAt()) {
				return ErrSpikeInClosing.WithDetail("target", t.ID)
			}
		}
		stored, err := s.store.EventsStarting(ctx, x.StartsAt.Add(-time.Hour), x.StartsAt.Add(time.Hour))
		if err != nil {
			return err
		}
		var others []time.Time
		for _, e := range stored {
			if e.Type == domain.EventSpike {
				others = append(others, e.StartsAt)
			}
		}
		for j := range spikes {
			if j != i {
				others = append(others, spikes[j].StartsAt)
			}
		}
		if tooMany(x.StartsAt, others) {
			return ErrSpikesPerHour
		}
	}
	return nil
}

// tooMany reports whether some hour that holds at holds SpikesPerHour of
// others already.
func tooMany(at time.Time, others []time.Time) bool {
	starts := []time.Time{at.Add(-time.Hour), at}
	for _, o := range others {
		for _, w := range []time.Time{o, o.Add(-time.Hour)} {
			if !w.Before(at.Add(-time.Hour)) && !w.After(at) {
				starts = append(starts, w)
			}
		}
	}
	for _, w := range starts {
		n := 0
		for _, o := range others {
			if !o.Before(w) && !o.After(w.Add(time.Hour)) {
				n++
			}
		}
		if n >= domain.SpikesPerHour {
			return true
		}
	}
	return false
}

// breatheAfter is how many minutes in a row a target goes its way before
// a breather; behind its plan, it breathes only while the pace it needs is
// within breatheRoom of the guide's most (a third of the minute guard).
// Tried over 80 seeds each: every target of +5% in 10 minutes, +3% in 12
// and +8% in 30 HIT, with a quarter of the minutes or more against it in
// 78%, 97% and 96% of them (after three minutes, half the pace back,
// always, as first decided: one +5% target in seven missed).
const (
	breatheAfter = 2
	breatheRoom  = 0.5
)

// breathe counts the running target's minutes (UTC, as the candles) that
// went its way, from the target at each minute's turn; after
// breatheAfter in a row, outside its closing (and behind its plan only
// with room to make it up), the next one breathes: its pace against it
// (Event.Guide), which the minutes after make up.
func (s *Sim) breathe(now time.Time) {
	i := slices.IndexFunc(s.events, func(e *domain.Event) bool {
		return e.Type == domain.EventTarget && e.Status == domain.EventRunning && e.CrossedAt.IsZero()
	})
	if i < 0 {
		s.breath = breath{}
		return
	}
	e, minute, p := s.events[i], now.Unix()/60, s.model.State.P
	if s.breath.event != e.ID {
		s.breath = breath{event: e.ID, minute: minute, p: p}
		return
	}
	if minute == s.breath.minute {
		return
	}
	if (e.Direction == domain.Below) == (p < s.breath.p) && p != s.breath.p {
		s.breath.run++
	} else {
		s.breath.run = 0
	}
	s.breath.minute, s.breath.p = minute, p
	behind := (e.Direction == domain.Below) == (p > e.PlanAtTime(now))
	pace := math.Abs(math.Log(e.Price.InexactFloat64()/p)) / math.Max(e.EndsAt().Sub(now).Minutes(), 1)
	room := pace <= s.params.MaxMinuteMove/3*breatheRoom
	if s.breath.run >= breatheAfter && now.Before(e.ClosingAt()) && (!behind || room) {
		e.Breather, s.breath.run = minute, 0
	}
}

// breath is what breathe knows of the running target's minutes.
type breath struct {
	event  string
	minute int64
	p      float64
	run    int
}

// watchTarget follows the running threshold target once the step took the
// target to p: at the end of a window it did not cross in, MISSED (after
// the last push, §3); its crossing, HIT, ending it at once (FOLLOW) or
// after the hold (HOLD); whether it is at risk.
func (s *Sim) watchTarget(ctx context.Context, now time.Time, p float64) {
	risk := false
	defer func() {
		s.m.targetAtRisk.Set(map[bool]float64{false: 0, true: 1}[risk])
		s.targetAtRisk = risk
	}()
	i := slices.IndexFunc(s.events, func(e *domain.Event) bool { return e.Type == domain.EventTarget && e.Status == domain.EventRunning })
	if i < 0 {
		return
	}
	e := s.events[i]
	switch {
	case e.CrossedAt.IsZero() && !now.Before(e.EndsAt()):
		e.Result = domain.ResultMissed
		s.log.WarnContext(ctx, "simulated market: a target missed its level", "event", e.ID, "direction", e.Direction, "level", e.Price,
			"target", p)
		s.endTarget(ctx, now, e)
		return
	case e.CrossedAt.IsZero() && e.Crossed(p):
		e.CrossedAt, e.Result = now, domain.ResultHit
		s.log.InfoContext(ctx, "simulated market: a target crossed its level", "event", e.ID, "level", e.Price, "target", p, "then", e.Then)
		if e.Then == domain.ThenFollow {
			s.endTarget(ctx, now, e)
			return
		}
		s.persist(ctx, e, nil)
	case !e.CrossedAt.IsZero() && !now.Before(e.HoldUntil()):
		s.endTarget(ctx, now, e)
		return
	}
	risk = e.AtRisk(now, p, s.params.MaxMinuteMove)
	if risk && !s.targetAtRisk {
		s.log.WarnContext(ctx, "simulated market: a target cannot reach its level in time at the minute guard's pace", "event", e.ID,
			"level", e.Price, "target", p, "ends_at", e.EndsAt())
	}
}

// endTarget ends a target (crossed and done, missed, or ended by an
// operator) where the price is: the market from there again, a new anchor
// at the target (FOLLOW, §3), saved like a re-anchoring.
func (s *Sim) endTarget(ctx context.Context, now time.Time, e *domain.Event) {
	s.model.Reanchor(s.btc, s.eth)
	s.keepAnchor(ctx, e, "TARGET "+e.ID+" "+e.Result)
	e.Status, e.EndedAt, s.movedAt = domain.EventDone, now, now
	details, _ := json.Marshal(map[string]any{
		"direction": e.Direction, "level": e.Price.String(), "result": e.Result, "crossed_at": e.CrossedAt, "target": s.model.State.P,
	})
	s.persist(ctx, e, &ports.Audit{
		Action: "market.sim.target_done", Target: "sim-event:" + e.ID, Actor: e.CreatedBy, Reason: e.Result, Details: string(details),
	})
	s.m.targets.WithLabelValues(e.Result).Inc()
	s.log.InfoContext(ctx, "simulated market target done", "event", e.ID, "result", e.Result, "target", s.model.State.P)
	s.events = slices.DeleteFunc(s.events, func(x *domain.Event) bool { return x == e })
}

// cancelSpikes cancels the scheduled spikes of target id (ended early by
// an operator).
func (s *Sim) cancelSpikes(ctx context.Context, id string, now time.Time, actor string) {
	for _, x := range s.events {
		if x.ParentID == id && x.Status == domain.EventScheduled {
			x.Status, x.EndedAt, x.EndedBy = domain.EventCanceled, now, actor
			s.persist(ctx, x, nil)
		}
	}
	s.events = slices.DeleteFunc(s.events, func(x *domain.Event) bool { return x.Status == domain.EventCanceled })
}

// SpikesOf returns the open spikes of target id.
func (s *Sim) SpikesOf(id string) []domain.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Event
	for _, x := range s.events {
		if x.ParentID == id {
			out = append(out, *x)
		}
	}
	return out
}

// TargetView is a threshold target's plan (from the price where it
// started, the target now while scheduled) and where the target is
// against it now.
type TargetView struct {
	Event     domain.Event
	From      float64
	Points    []domain.PlanPoint
	Spikes    []domain.Event
	Now       domain.PlanPoint
	Target    float64
	Deviation float64
	AtRisk    bool
}

func (s *Sim) view(e domain.Event, now time.Time) TargetView {
	from := e.FromP.InexactFloat64()
	if from <= 0 {
		from = s.model.State.P
	}
	v := TargetView{Event: e, From: from, Points: domain.Plan(e, from, s.params), Target: s.model.State.P}
	for _, x := range s.events {
		if x.ParentID == e.ID {
			v.Spikes = append(v.Spikes, *x)
		}
	}
	if e.Status == domain.EventRunning && len(v.Points) > 0 {
		v.Now = domain.PlanAt(v.Points, now)
		if v.Now.Plan > 0 && v.Target > 0 {
			v.Deviation = math.Log(v.Target / v.Now.Plan)
		}
		v.AtRisk = s.targetAtRisk
	}
	return v
}

// RunningTarget is the running threshold target's view, false when none
// runs (the operators' stream).
func (s *Sim) RunningTarget() (TargetView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.events, func(e *domain.Event) bool { return e.Type == domain.EventTarget && e.Status == domain.EventRunning })
	if i < 0 {
		return TargetView{}, false
	}
	return s.view(*s.events[i], s.now()), true
}

// TargetPlan is target id's view: an open one's, or one of the latest
// 200 events'.
func (s *Sim) TargetPlan(ctx context.Context, id string) (TargetView, error) {
	s.mu.Lock()
	i := slices.IndexFunc(s.events, func(e *domain.Event) bool { return e.ID == id })
	if i >= 0 {
		defer s.mu.Unlock()
		if s.events[i].Type != domain.EventTarget {
			return TargetView{}, apperr.NotFound("no such target")
		}
		return s.view(*s.events[i], s.now()), nil
	}
	s.mu.Unlock()
	recent, err := s.store.Events(ctx, false, 200)
	if err != nil {
		return TargetView{}, err
	}
	j := slices.IndexFunc(recent, func(e domain.Event) bool { return e.ID == id && e.Type == domain.EventTarget })
	if j < 0 {
		return TargetView{}, apperr.NotFound("no such target")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.view(recent[j], s.now())
	for _, x := range recent {
		if x.ParentID == id && !slices.ContainsFunc(v.Spikes, func(o domain.Event) bool { return o.ID == x.ID }) {
			v.Spikes = append(v.Spikes, x)
		}
	}
	return v, nil
}

// TargetPreview is what a threshold target would be if created now: its
// plan from the target now, whether its window is long enough and the
// shortest that is, its move, and whether one operator may make it alone.
type TargetPreview struct {
	Event         domain.Event
	Feasible      bool
	MinDuration   time.Duration
	Move          float64
	NeedsApproval bool
	Points        []domain.PlanPoint
}

// PreviewTarget previews a threshold target (the operators' form).
func (s *Sim) PreviewTarget(ctx context.Context, direction string, level decimal.Decimal, window time.Duration, startsAt time.Time) (
	TargetPreview, error,
) {
	if !level.IsPositive() || window <= 0 {
		return TargetPreview{}, apperr.Invalid("a positive price and duration_seconds are required")
	}
	now := s.now()
	if startsAt.Before(now) {
		startsAt = now
	}
	spent, err := s.budget(ctx, startsAt)
	if err != nil {
		return TargetPreview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.model.State.P
	e := domain.Event{Type: domain.EventTarget, Direction: direction, Price: level, Duration: window, StartsAt: startsAt}
	e.Infer(p)
	out := TargetPreview{Event: e, MinDuration: domain.MinWindow(p, level.InexactFloat64(), s.params.MaxMinuteMove), Points: domain.Plan(e, p, s.params)}
	out.Feasible, out.Move = window >= out.MinDuration, e.Move(p, s.params.Sigma)
	out.NeedsApproval = domain.NeedsApproval(domain.Spend{At: startsAt, Move: out.Move}, spent.spends(p, s.params.Sigma))
	return out, nil
}
