package application

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// The operators' events (ASTRA design §6.2): the API creates and ends
// them, each round starts the ones due and applies the running ones'
// shape to the model, and the executors push the printed price after the
// quotes while one moves it (and for executeAfter more) or while the
// quotes walk the price band.
const (
	executeAfter = time.Minute
	sampleEvery  = 10 * time.Second
	samplesKept  = 24 * 60 * 60 / 10 // a day
)

// Codes of the events' refusals.
var (
	ErrEventsOff     = apperr.New(apperr.KindForbidden, "SIM_EVENTS_OFF", "the operators' price events are switched off (sim.events)")
	ErrNeedsApproval = apperr.New(apperr.KindForbidden, "SIM_EVENT_NEEDS_APPROVAL",
		"beyond what one operator may do alone (a jump of 30%, moves of 50% in an hour): a second operator approves")
	ErrEventNotOpen = apperr.New(apperr.KindConflict, apperr.CodeConflict, "the event is not scheduled or running")
)

// Sample is the target and the last price at a time, for the operators'
// chart.
type Sample struct {
	At     time.Time
	Target float64
	Last   decimal.Decimal
}

func (s *Sim) runningEvents() []*domain.Event {
	var out []*domain.Event
	for _, e := range s.events {
		if e.Status == domain.EventRunning {
			out = append(out, e)
		}
	}
	return out
}

func (s *Sim) runs(t domain.EventType) bool {
	return slices.ContainsFunc(s.runningEvents(), func(e *domain.Event) bool { return e.Type == t })
}

// startDue starts the scheduled events whose time has come: one that
// moves or holds the price waits while another such runs.
func (s *Sim) startDue(ctx context.Context, now time.Time) {
	for _, e := range s.events {
		if e.Status != domain.EventScheduled || e.StartsAt.After(now) {
			continue
		}
		if e.Moves() && slices.ContainsFunc(s.runningEvents(), func(r *domain.Event) bool { return r.Moves() }) {
			continue
		}
		e.Status, e.StartedAt = domain.EventRunning, now
		e.FromLogE, e.FromP = s.model.State.LogE, decimal.NewFromFloat(s.model.State.P)
		switch e.Type {
		case domain.EventReanchor:
			s.model.Reanchor(s.btc, s.eth)
			s.keepAnchor(ctx, e)
		case domain.EventHalt:
			s.stop(ctx)
			if err := s.pairs.SetPairStatus(ctx, s.cfg.Symbol, "HALT", "system:market-sim", "simulated market event "+e.ID+": "+e.Reason); err != nil {
				s.m.errors.WithLabelValues("halt").Inc()
				s.log.WarnContext(ctx, "simulated market: the pair not halted", "event", e.ID, "error", err)
			}
			s.pairAt = time.Time{}
		}
		s.persist(ctx, e, nil)
		s.log.InfoContext(ctx, "simulated market event started", "event", e.ID, "type", e.Type, "target", s.model.State.P)
	}
}

// finish ends the events that ran their course this step; the price p
// they held (a pause, a target) is where the model goes on from.
func (s *Sim) finish(ctx context.Context, now time.Time, ended []*domain.Event, p float64) {
	for _, e := range ended {
		if e.Type == domain.EventPause || e.Type == domain.EventTarget {
			s.model.Hold(p)
		}
		e.Status, e.EndedAt = domain.EventDone, now
		if e.Moves() {
			s.movedAt = now
		}
		s.persist(ctx, e, nil)
		s.log.InfoContext(ctx, "simulated market event done", "event", e.ID, "type", e.Type, "target", p)
	}
	s.events = slices.DeleteFunc(s.events, func(e *domain.Event) bool {
		return e.Status == domain.EventDone || e.Status == domain.EventCanceled
	})
}

// keepAnchor saves the new P0 of a re-anchoring in the settings: the
// model's next start, and the operators' form, go on from it.
func (s *Sim) keepAnchor(ctx context.Context, e *domain.Event) {
	p := s.params
	from := p.P0
	p.P0 = s.model.Params.P0
	details, _ := json.Marshal(map[string]any{"p0": map[string]float64{"from": from, "to": p.P0}, "event": e.ID})
	version, err := s.store.SaveSettings(ctx, p, e.CreatedBy, &ports.Audit{
		Action: "market.sim.params_changed", Target: "sim:" + s.cfg.Symbol, Actor: e.CreatedBy, Reason: "REANCHOR " + e.ID, Details: string(details),
	})
	if err != nil {
		s.m.errors.WithLabelValues("settings").Inc()
		s.log.WarnContext(ctx, "simulated market: the new anchor not saved", "event", e.ID, "error", err)
		return
	}
	s.params, s.version = p, version
}

func (s *Sim) persist(ctx context.Context, e *domain.Event, audit *ports.Audit) {
	if err := s.store.SaveEvent(ctx, *e, audit); err != nil {
		s.m.errors.WithLabelValues("event").Inc()
		s.log.WarnContext(ctx, "simulated market: an event not saved", "event", e.ID, "error", err)
	}
}

// execute lets an executor push the printed price after the quotes'
// center while an event moves the target, or the quotes walk the price
// band toward it: every second or two, a market order in the center's
// direction when the last trade is off by more than half the spread,
// larger the farther off. The trades move the band's anchor, and the
// walk goes on from there.
func (s *Sim) execute(ctx context.Context, now time.Time, center float64) {
	moving := slices.ContainsFunc(s.runningEvents(), func(e *domain.Event) bool { return e.Type == domain.EventJump || e.Type == domain.EventTarget })
	if (!moving && !s.walking && now.Sub(s.movedAt) > executeAfter) || now.Before(s.executeAt) {
		return
	}
	executors := s.botsOf(domain.RoleExecutor)
	if len(executors) == 0 || !s.last.IsPositive() {
		return
	}
	rng := s.model.Rand()
	s.executeAt = now.Add(time.Second + time.Duration(rng.Int64N(int64(time.Second))))
	gap := center/s.last.InexactFloat64() - 1
	if math.Abs(gap) <= s.params.Spread/2 {
		return
	}
	side := domain.Buy
	if gap < 0 {
		side = domain.Sell
	}
	b := pick(rng, executors, now)
	if b == nil {
		return
	}
	worth := s.params.LevelSize * math.Min(20, math.Max(1, math.Abs(gap)/0.001))
	s.market(ctx, now, b, side, worth, center)
}

// sample keeps the target and the last price every sampleEvery, a day of
// them.
func (s *Sim) sample(now time.Time, p float64) {
	if n := len(s.samples); n > 0 && now.Sub(s.samples[n-1].At) < sampleEvery {
		return
	}
	s.samples = append(s.samples, Sample{At: now, Target: p, Last: s.last})
	if len(s.samples) > samplesKept {
		s.samples = slices.Clone(s.samples[len(s.samples)-samplesKept:])
	}
}

// History returns the samples since t, oldest first.
func (s *Sim) History(t time.Time) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, _ := slices.BinarySearchFunc(s.samples, t, func(x Sample, t time.Time) int { return x.At.Compare(t) })
	return slices.Clone(s.samples[i:])
}

// CreateEvent schedules an operator's event, at once when it starts no
// later than now, after the checks of §6.2: sim.events on, the event
// valid, and within one operator's limits unless another approved it.
func (s *Sim) CreateEvent(ctx context.Context, e domain.Event) (domain.Event, error) {
	if !s.flags.Enabled(flags.KeySimEvents, flags.Subject{Symbol: s.cfg.Symbol}) {
		return domain.Event{}, ErrEventsOff
	}
	now := s.now()
	e.ID, e.Status, e.CreatedAt = uuid.Must(uuid.NewV7()).String(), domain.EventScheduled, now
	e.StartedAt, e.EndedAt, e.FromLogE, e.FromP, e.EndedBy = time.Time{}, time.Time{}, 0, decimal.Zero, ""
	if e.StartsAt.Before(now) {
		e.StartsAt = now
	}
	if err := e.Validate(); err != nil {
		return domain.Event{}, apperr.Invalid(err.Error())
	}
	recent, err := s.store.EventsSince(ctx, now.Add(-time.Hour))
	if err != nil {
		return domain.Event{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.model.State.P
	if domain.NeedsApproval(e, p, recent) && (e.ApprovedBy == "" || e.ApprovedBy == e.CreatedBy) {
		return domain.Event{}, ErrNeedsApproval.WithDetail("move", math.Round(e.Move(p)*1e4)/1e4)
	}
	details, _ := json.Marshal(map[string]any{
		"type": e.Type, "size": e.Size, "price": e.Price.String(), "mu": e.Mu, "factor": e.Factor,
		"duration_s": int(e.Duration.Seconds()), "hold_s": int(e.Hold.Seconds()), "starts_at": e.StartsAt, "approved_by": e.ApprovedBy,
		"target": p,
	})
	if err := s.store.SaveEvent(ctx, e, &ports.Audit{
		Action: "market.sim.event_created", Target: "sim-event:" + e.ID, Actor: e.CreatedBy, Reason: e.Reason, Details: string(details),
	}); err != nil {
		return domain.Event{}, err
	}
	s.events = append(s.events, &e)
	s.log.InfoContext(ctx, "simulated market event created", "event", e.ID, "type", e.Type, "by", e.CreatedBy, "starts_at", e.StartsAt)
	return e, nil
}

// EndEvent ends an event early for actor: a scheduled one is canceled, a
// running one done where it stands (a held price is where the model goes
// on from; a halted pair trades again).
func (s *Sim) EndEvent(ctx context.Context, id, actor, reason string) (domain.Event, error) {
	if actor == "" || len(reason) < 3 {
		return domain.Event{}, apperr.Invalid("the operator and a reason are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.events, func(e *domain.Event) bool { return e.ID == id })
	if i < 0 {
		return domain.Event{}, ErrEventNotOpen
	}
	e := s.events[i]
	now := s.now()
	action := "market.sim.event_canceled"
	if e.Status == domain.EventScheduled {
		e.Status = domain.EventCanceled
	} else {
		action = "market.sim.event_ended"
		switch e.Type {
		case domain.EventPause, domain.EventTarget:
			s.model.Hold(s.model.State.P)
		case domain.EventHalt:
			if err := s.pairs.SetPairStatus(ctx, s.cfg.Symbol, "TRADING", actor, "simulated market event "+e.ID+" ended: "+reason); err != nil {
				return domain.Event{}, err
			}
			s.pairAt = time.Time{}
		}
		if e.Moves() {
			s.movedAt = now
		}
		e.Status = domain.EventDone
	}
	e.EndedAt, e.EndedBy = now, actor
	if err := s.store.SaveEvent(ctx, *e, &ports.Audit{
		Action: action, Target: "sim-event:" + e.ID, Actor: actor, Reason: reason,
		Details: fmt.Sprintf(`{"type":%q,"target":%v}`, e.Type, s.model.State.P),
	}); err != nil {
		return domain.Event{}, err
	}
	s.events = slices.Delete(s.events, i, i+1)
	return *e, nil
}

// Events lists the scheduled and running events, or the latest limit.
func (s *Sim) Events(ctx context.Context, open bool, limit int) ([]domain.Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.store.Events(ctx, open, limit)
}
