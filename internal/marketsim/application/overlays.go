package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketsim/domain"
	"github.com/skill/exchange/internal/marketsim/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/svcsign"
)

// Price events on the pairs a reference market follows (design
// 2026-10-07, general price control; J0 contract §3): one request names
// up to ten pairs and a target (a price, or a share of the reference
// price) with three times in seconds; each followed pair gets an OVERLAY
// event whose factor market-data puts on the pair's reference data, and
// the simulated market's own pair a JUMP of its model (it has no reference
// market to come back to). The lease holder pushes each running event's
// factor every second (market-data drops it 5 seconds after the last
// push), so a restart goes on along the stored schedule.

// OverlayConfig is the overlays' setup.
type OverlayConfig struct {
	// MaxLoss is the most HOUSE may lose on one event's pair and its
	// perpetuals, as estimated from its rooms when the event is made and
	// again when one scheduled ahead starts (OVERLAY_MAX_LOSS_USDT).
	MaxLoss decimal.Decimal
	// MaxTotal is the longest event, its ramps and hold together
	// (OVERLAY_MAX_SECONDS, domain.OverlayMaxTotal at most).
	MaxTotal time.Duration
	// Every is how often the factors are pushed (a second).
	Every time.Duration
}

// The overlays' timing: a push holds overlayAhead (market-data keeps it
// 5 seconds at most); an event scheduled ahead whose start cannot read the
// reference price for overlayStartGrace is canceled, and one scheduled
// more than overlayRecheck ahead has HOUSE's rooms checked again when it
// starts; a running event's peak is saved every overlayPeakEvery.
const (
	overlayAhead      = 4 * time.Second
	overlayStartGrace = time.Minute
	overlayRecheck    = 5 * time.Second
	overlayPeakEvery  = 10 * time.Second
	overlaySystem     = "system:market-sim"
	overlayFlagActor  = "system:market.overlay"
	// The pushes failing (reviews C57 ③, C58 ③), by time: a push holds
	// overlayAhead, so longer than that without a successful one is a dip
	// back to the reference price. One dip is a market-data restart: the
	// pushes go on along the schedule once it answers (the overlay-restart
	// drill). A second dip - pushes coming and going, the pair jumping back
	// and forth - or overlayGiveUp without a successful push cancels the
	// event; so does a refusal that will not pass (a pair no longer
	// followed, a bad signature) at once.
	overlayDips   = 1
	overlayGiveUp = 2 * time.Minute
)

// Codes of the overlays' refusals (J0 contract §3.1).
var (
	ErrOverlayOff = apperr.New(apperr.KindForbidden, "SIM_OVERLAY_OFF",
		"price events on followed pairs are switched off (market.overlay)")
	ErrOverlayRunning = apperr.New(apperr.KindConflict, "SIM_OVERLAY_RUNNING",
		"the pair has a price event scheduled or running: one at a time")
	ErrOverlayTooFar = apperr.New(apperr.KindInvalid, "SIM_OVERLAY_TOO_FAR",
		"the target is more than 90% away from the reference price")
	ErrOverlayLossCap = apperr.New(apperr.KindConflict, "SIM_OVERLAY_LOSS_CAP",
		"HOUSE could lose more than the cap on the event (OVERLAY_MAX_LOSS_USDT), or its rooms could not be read")
	ErrNotOverlayable = apperr.New(apperr.KindInvalid, "SIM_NOT_OVERLAYABLE",
		"the pair follows no reference market and is not the simulated market's")
	// ErrOverlayUnconfigured answers while market-sim has no key to sign
	// its pushes with (OVERLAY_API_SECRET).
	ErrOverlayUnconfigured = apperr.New(apperr.KindUnavailable, "SIM_OVERLAY_UNCONFIGURED",
		"market-sim has no OVERLAY_API_SECRET: it cannot push price events to market-data")
)

// OverlayRequest is an operator's price event on one or more pairs: a
// target price or a share of the reference price in percent (several
// pairs: a share only), its ramps and hold, whether it reaches the
// perpetuals and the leverage (Risk), when it starts (zero: now).
type OverlayRequest struct {
	Symbols                []string
	TargetPrice            decimal.Decimal
	TargetPct              *float64
	RampUp, Hold, RampDown time.Duration
	Risk                   bool
	StartsAt               time.Time
	Actor, ApprovedBy      string
	Reason                 string
}

// OverlayCreated is the event a request made on one pair: its type
// (OVERLAY, or JUMP on the simulated market's pair), status, target
// factor and the price it starts from (the reference price, or the
// simulated market's target).
type OverlayCreated struct {
	Symbol       string
	Event        domain.Event
	TargetFactor float64
	BasePrice    decimal.Decimal
}

// PlatformEvents is the simulated market's own pair (Sim).
type PlatformEvents interface {
	Symbol() string
	TargetPrice() float64
	CreateEvent(ctx context.Context, e domain.Event, spikes ...domain.Event) (domain.Event, error)
	EndEvent(ctx context.Context, id, actor, reason string) (domain.Event, error)
}

// Overlays runs the price events on followed pairs.
type Overlays struct {
	cfg      OverlayConfig
	market   ports.Overlays // nil: no key to sign the pushes with
	house    ports.House
	store    ports.Store
	flags    ports.Flags
	platform PlatformEvents
	log      *slog.Logger
	now      func() time.Time

	// ops serializes the creations, from the checks to the save.
	ops sync.Mutex

	// mu guards the map of the open events alone, never across a request:
	// each event has its own lock for its state (overlayRun.mu; review C57
	// ②). busy counts the rounds' work on the events under way.
	mu    sync.Mutex
	open  map[string]*overlayRun // scheduled and running, by event ID
	ready atomic.Bool
	busy  sync.WaitGroup

	factor *prometheus.GaugeVec
	pushes *prometheus.CounterVec
	saves  prometheus.Counter
}

// overlayRun is an open event as the runner keeps it. Its pair and start
// are set when it is made (read under Overlays.mu alone); mu guards the
// rest: the runner holds it while it works on the event, its requests to
// market-data included, and End while it changes the event - one event's
// slow pushes hold up no other's, nor the lookups.
type overlayRun struct {
	symbol string
	starts time.Time

	mu        sync.Mutex
	gone      bool // no longer open: done or canceled
	e         domain.Event
	savedPeak decimal.Decimal // its peak as stored
	peakAt    time.Time       // when its peak was last saved
	failing   bool            // its last push failed (logged once)
	pushedAt  time.Time       // its last successful push (or its start here)
	dipped    bool            // the failures since pushedAt made a dip
	dips      int             // its dips back to the reference price
}

// NewOverlays returns the overlays; market nil leaves them unconfigured
// (creations answer ErrOverlayUnconfigured). Register the metrics with
// reg.
func NewOverlays(cfg OverlayConfig, market ports.Overlays, house ports.House, store ports.Store, fl ports.Flags, platform PlatformEvents,
	log *slog.Logger, reg prometheus.Registerer,
) *Overlays {
	if cfg.Every <= 0 {
		cfg.Every = time.Second
	}
	if cfg.MaxTotal <= 0 || cfg.MaxTotal > domain.OverlayMaxTotal {
		cfg.MaxTotal = domain.OverlayMaxTotal
	}
	o := &Overlays{
		cfg: cfg, market: market, house: house, store: store, flags: fl, platform: platform, log: log, now: time.Now,
		open: map[string]*overlayRun{},
		factor: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_sim_overlay_factor", Help: "The factor market-sim pushes for a running price event on a followed pair.",
		}, []string{"symbol"}),
		pushes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_sim_overlay_pushes_total", Help: "Pushes of the price events' factors to market-data, by result (ok, failed).",
		}, []string{"result"}),
		saves: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_sim_overlay_save_failures_total", Help: "Price events on followed pairs whose new course could not be saved.",
		}),
	}
	reg.MustRegister(o.factor, o.pushes, o.saves)
	return o
}

// Start loads the open events; the API answers ErrNotReady before.
func (o *Overlays) Start(ctx context.Context) error {
	open, err := o.store.Events(ctx, true, 0)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, e := range open {
		if e.Type == domain.EventOverlay {
			o.open[e.ID] = newRun(e, o.now())
		}
	}
	o.ready.Store(true)
	return nil
}

// Run pushes the factors every cfg.Every until ctx ends, then waits for
// the work under way.
func (o *Overlays) Run(ctx context.Context) error {
	t := time.NewTicker(o.cfg.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			o.settle()
			return nil
		case <-t.C:
		}
		o.Round(ctx)
	}
}

// settle waits until the rounds' work on the events is done.
func (o *Overlays) settle() { o.busy.Wait() }

// Has reports whether id is an open overlay event.
func (o *Overlays) Has(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.open[id]
	return ok
}

// Create makes a request's events after the checks of the J0 contract
// §3.1: each followed pair's target within ±90% of its reference price,
// no other open event on it, within one operator's share of the pair's
// budget unless another operator approved, and HOUSE's worst loss on it
// within the cap; the simulated market's pair gets a JUMP with that
// market's own checks. All or none.
func (o *Overlays) Create(ctx context.Context, req OverlayRequest) ([]OverlayCreated, error) {
	if !o.ready.Load() {
		return nil, ErrNotReady
	}
	symbols, err := o.check(&req)
	if err != nil {
		return nil, err
	}
	platform := ""
	if o.platform != nil {
		if i := slices.Index(symbols, o.platform.Symbol()); i >= 0 {
			platform = symbols[i]
			symbols = slices.Delete(symbols, i, i+1)
		}
	}
	if len(symbols) > 0 {
		if o.market == nil {
			return nil, ErrOverlayUnconfigured
		}
		if !o.flags.Enabled(flags.KeyOverlay, flags.Subject{}) {
			return nil, ErrOverlayOff
		}
	}
	o.ops.Lock()
	defer o.ops.Unlock()
	now := o.now()
	starts := req.StartsAt
	if starts.Before(now) {
		starts = now
	}
	spent, err := o.store.EventsStarting(ctx, starts.Add(-time.Hour), starts.Add(time.Hour))
	if err != nil {
		return nil, err
	}
	approved := req.ApprovedBy != "" && req.ApprovedBy != req.Actor
	events := make([]domain.Event, 0, len(symbols))
	audits := make([]ports.Audit, 0, len(symbols))
	for _, symbol := range symbols {
		e, audit, err := o.plan(ctx, req, symbol, starts, now, spent, approved)
		if err != nil {
			return nil, err
		}
		events, audits = append(events, e), append(audits, audit)
	}
	var out []OverlayCreated
	var jump domain.Event
	if platform != "" {
		p := o.platform.TargetPrice()
		if p <= 0 {
			return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the simulated market has no price yet")
		}
		size := req.TargetPrice.InexactFloat64()/p - 1
		if req.TargetPct != nil {
			size = *req.TargetPct / 100
		}
		jump, err = o.platform.CreateEvent(ctx, domain.Event{
			Type: domain.EventJump, Size: size, Duration: req.RampUp, StartsAt: starts, CreatedBy: req.Actor, ApprovedBy: req.ApprovedBy,
			Reason: req.Reason,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, OverlayCreated{Symbol: platform, Event: jump, TargetFactor: 1 + size, BasePrice: decimal.NewFromFloat(p).Round(8)})
	}
	if len(events) > 0 {
		if err := o.store.SaveEventsEach(ctx, events, audits); err != nil {
			if jump.ID != "" {
				if _, endErr := o.platform.EndEvent(ctx, jump.ID, overlaySystem, "the request's events on followed pairs were not saved"); endErr != nil {
					o.log.WarnContext(ctx, "price event: the simulated market's jump of a failed request not ended", "event", jump.ID, "error", endErr)
				}
			}
			if errors.Is(err, ports.ErrOverlayOpen) {
				return nil, ErrOverlayRunning
			}
			return nil, err
		}
	}
	o.mu.Lock()
	for _, e := range events {
		o.open[e.ID] = newRun(e, now)
		out = append(out, OverlayCreated{Symbol: e.Symbol, Event: e, TargetFactor: e.TargetFactor, BasePrice: e.BasePrice})
		o.log.InfoContext(ctx, "price event created", "event", e.ID, "symbol", e.Symbol, "factor", e.TargetFactor, "by", e.CreatedBy,
			"starts_at", e.StartsAt, "risk", e.Risk)
	}
	o.mu.Unlock()
	return out, nil
}

// check validates a request's form and returns its pairs, upper case.
func (o *Overlays) check(req *OverlayRequest) ([]string, error) {
	var symbols []string
	for _, s := range req.Symbols {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "" || slices.Contains(symbols, s) {
			return nil, apperr.Invalid("symbols are distinct pairs")
		}
		symbols = append(symbols, s)
	}
	var errs []string
	fail := func(m string) { errs = append(errs, m) }
	if len(symbols) == 0 || len(symbols) > domain.OverlayMaxSymbols {
		fail("an event names 1 to 10 pairs")
	}
	switch {
	case req.TargetPct != nil && req.TargetPrice.IsPositive(), req.TargetPct == nil && !req.TargetPrice.IsPositive():
		fail("give target_price or target_pct")
	case req.TargetPct != nil && (*req.TargetPct == 0 || math.IsNaN(*req.TargetPct) || math.IsInf(*req.TargetPct, 0)):
		fail("target_pct is a share in percent, not 0")
	case req.TargetPct == nil && len(symbols) > 1:
		fail("several pairs take target_pct, not one target_price")
	}
	if req.RampUp < time.Second || req.Hold < 0 || req.RampDown < domain.OverlayMinRampDown {
		fail("ramp_up_seconds is 1 at least, hold_seconds 0 at least, ramp_down_seconds 3 at least")
	}
	if req.RampUp+req.Hold+req.RampDown > o.cfg.MaxTotal {
		fail(fmt.Sprintf("an event runs %d seconds at most", int(o.cfg.MaxTotal.Seconds())))
	}
	if req.Actor == "" || len(req.Reason) < 3 {
		fail("the operator and a reason are required")
	}
	if req.StartsAt.After(o.now().Add(domain.MaxLead)) {
		fail("an event starts within 24 hours")
	}
	if len(errs) > 0 {
		return nil, apperr.Invalid(strings.Join(errs, "; "))
	}
	return symbols, nil
}

// plan makes symbol's event of the request after its checks, with its
// audit record; it starts now (running, from the reference price now)
// when it is due.
func (o *Overlays) plan(ctx context.Context, req OverlayRequest, symbol string, starts, now time.Time, spent []domain.Event, approved bool,
) (domain.Event, ports.Audit, error) {
	ref, err := o.market.Followed(ctx, symbol)
	if err != nil {
		o.log.WarnContext(ctx, "price event: the reference price not read", "symbol", symbol, "error", err)
		return domain.Event{}, ports.Audit{}, apperr.Unavailable(err)
	}
	if !ref.Followed {
		return domain.Event{}, ports.Audit{}, ErrNotOverlayable.WithDetail("symbol", symbol)
	}
	if !ref.Fresh || !ref.Source.IsPositive() {
		return domain.Event{}, ports.Audit{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable,
			"the reference price of "+symbol+" is not fresh")
	}
	f := req.TargetPrice.InexactFloat64() / ref.Source.InexactFloat64()
	if req.TargetPct != nil {
		f = 1 + *req.TargetPct/100
	}
	f = math.Round(f*1e8) / 1e8
	if f < domain.OverlayMinFactor || f > domain.OverlayMaxFactor {
		return domain.Event{}, ports.Audit{}, ErrOverlayTooFar.WithDetail("symbol", symbol).WithDetail("factor", round4(f))
	}
	if f == 1 {
		return domain.Event{}, ports.Audit{}, apperr.Invalid("the target of " + symbol + " is its reference price")
	}
	if o.openOn(symbol) {
		return domain.Event{}, ports.Audit{}, ErrOverlayRunning.WithDetail("symbol", symbol)
	}
	var others []domain.Spend
	for _, x := range spent {
		if x.Type == domain.EventOverlay && x.Symbol == symbol {
			others = append(others, domain.Spend{At: x.StartsAt, Move: x.Move(0, 0)})
		}
	}
	if domain.NeedsApproval(domain.Spend{At: starts, Move: f - 1}, others) && !approved {
		return domain.Event{}, ports.Audit{}, ErrNeedsApproval.WithDetail("move", round4(f-1)).WithDetail("symbol", symbol)
	}
	loss, err := o.lossOf(ctx, symbol, f, req.Risk)
	if err != nil {
		o.log.WarnContext(ctx, "price event: HOUSE's rooms not read", "symbol", symbol, "error", err)
		return domain.Event{}, ports.Audit{}, ErrOverlayLossCap.WithDetail("symbol", symbol).WithDetail("reason", "HOUSE's rooms could not be read")
	}
	if loss.GreaterThan(o.cfg.MaxLoss) {
		return domain.Event{}, ports.Audit{}, ErrOverlayLossCap.WithDetail("symbol", symbol).
			WithDetail("estimate_usdt", loss.Round(2).String()).WithDetail("cap_usdt", o.cfg.MaxLoss.String())
	}
	target := req.TargetPrice
	if req.TargetPct != nil {
		target = ref.Source.Mul(decimal.NewFromFloat(f)).Round(8)
	}
	e := domain.Event{
		ID: uuid.Must(uuid.NewV7()).String(), Type: domain.EventOverlay, Symbol: symbol, TargetFactor: f, Price: target,
		RampUp: req.RampUp, Hold: req.Hold, RampDown: req.RampDown, Risk: req.Risk, StartsAt: starts, Status: domain.EventScheduled,
		CreatedBy: req.Actor, ApprovedBy: req.ApprovedBy, Reason: req.Reason, CreatedAt: now,
	}
	if err := e.Validate(); err != nil {
		return domain.Event{}, ports.Audit{}, apperr.Invalid(err.Error())
	}
	if !starts.After(now) {
		e.Status, e.StartedAt, e.BasePrice = domain.EventRunning, now, ref.Source
	}
	details, _ := json.Marshal(map[string]any{
		"type": e.Type, "symbol": symbol, "target_factor": f, "target_price": target.String(), "ramp_up_s": int(req.RampUp.Seconds()),
		"hold_s": int(req.Hold.Seconds()), "ramp_down_s": int(req.RampDown.Seconds()), "risk": req.Risk, "starts_at": starts,
		"approved_by": req.ApprovedBy, "reference_price": ref.Source.String(), "loss_estimate_usdt": loss.Round(2).String(),
		"signed_by": svcsign.KeyID(ctx),
	})
	return e, ports.Audit{
		Action: "market.sim.event_created", Target: "sim-event:" + e.ID, Actor: req.Actor, Reason: req.Reason, Details: string(details),
	}, nil
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

func (o *Overlays) openOn(symbol string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.open {
		if r.symbol == symbol {
			return true
		}
	}
	return false
}

func newRun(e domain.Event, now time.Time) *overlayRun {
	return &overlayRun{symbol: e.Symbol, starts: e.StartsAt, e: e, savedPeak: e.PeakPrice, pushedAt: now}
}

// forget takes an event out of the open ones; r.mu is held.
func (o *Overlays) forget(r *overlayRun) {
	r.gone = true
	o.mu.Lock()
	delete(o.open, r.e.ID)
	o.mu.Unlock()
}

// perpetualsOf are the perpetuals a pair is the index of: X-USDT-PERP and
// the coin-margined X-USD-PERP of X-USDT.
func perpetualsOf(symbol string) []string {
	out := []string{symbol + "-PERP"}
	if base, ok := strings.CutSuffix(symbol, "-USDT"); ok {
		out = append(out, base+"-USD-PERP")
	}
	return out
}

// lossOf is HOUSE's worst loss in USDT on an event of factor f on symbol
// (J0 contract §3.3): all it may still buy (f above 1) or sell (below)
// there, and with risk on the pair's perpetuals, taken at the factor and
// held back to 1. HOUSE not quoting the pair is an error (its rooms are
// unknown); a perpetual it does not quote adds nothing.
func (o *Overlays) lossOf(ctx context.Context, symbol string, f float64, risk bool) (decimal.Decimal, error) {
	symbols := []string{symbol}
	if risk {
		symbols = append(symbols, perpetualsOf(symbol)...)
	}
	total := decimal.Zero
	for i, s := range symbols {
		r, ok, err := o.house.Rooms(ctx, s)
		if err != nil {
			return decimal.Zero, err
		}
		if !ok {
			if i == 0 {
				return decimal.Zero, fmt.Errorf("HOUSE does not quote %s", s)
			}
			continue
		}
		units := r.Sell
		if f > 1 {
			units = r.Buy
		}
		total = total.Add(units.Mul(r.UnitValue).Mul(decimal.NewFromFloat(domain.OverlayLoss(f, r.Inverse)).Round(8)))
	}
	return total, nil
}

// End ends an event early for actor: a scheduled one is canceled; a
// running one goes back to 1 from where it stands in 3 seconds
// (domain.OverlayEndRamp), then is done, its result CANCELED.
func (o *Overlays) End(ctx context.Context, id, actor, reason string) (domain.Event, error) {
	if actor == "" || len(reason) < 3 {
		return domain.Event{}, apperr.Invalid("the operator and a reason are required")
	}
	o.mu.Lock()
	r, ok := o.open[id]
	o.mu.Unlock()
	if !ok {
		return domain.Event{}, ErrEventNotOpen
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gone {
		return domain.Event{}, ErrEventNotOpen
	}
	e := r.e
	now := o.now()
	switch {
	case e.Status == domain.EventScheduled:
		e.Status, e.Result, e.EndedAt, e.EndedBy = domain.EventCanceled, domain.ResultCanceled, now, actor
		if err := o.store.SaveEvent(ctx, e, o.audit(ctx, e, "market.sim.event_canceled", actor, reason, now)); err != nil {
			return domain.Event{}, err
		}
		r.e = e
		o.forget(r)
	case e.EndedAt.IsZero():
		e.Result, e.EndedAt, e.EndedBy = domain.ResultCanceled, now, actor
		if err := o.store.SaveEvent(ctx, e, o.audit(ctx, e, "market.sim.event_ended", actor, reason, now)); err != nil {
			return domain.Event{}, err
		}
		r.e = e
	}
	return e, nil
}

// audit is an event's audit record at now: its course and the factor.
func (o *Overlays) audit(ctx context.Context, e domain.Event, action, actor, reason string, now time.Time) *ports.Audit {
	f, _ := e.OverlayAt(now)
	if e.Status != domain.EventRunning {
		f = 1
	}
	details, _ := json.Marshal(map[string]any{
		"type": e.Type, "symbol": e.Symbol, "status": e.Status, "result": e.Result, "factor": round4(f), "target_factor": e.TargetFactor,
		"base_price": e.BasePrice.String(), "peak_price": e.PeakPrice.String(), "end_reference_price": e.EndReferencePrice.String(),
		"end_platform_price": e.EndPlatformPrice.String(), "signed_by": svcsign.KeyID(ctx),
	})
	return &ports.Audit{Action: action, Target: "sim-event:" + e.ID, Actor: actor, Reason: reason, Details: string(details)}
}

// Round starts the events due, pushes the running ones' factors and ends
// the ones back at 1; with market.overlay off it cancels them (market-data
// is at 1 then already). Each event is worked on by itself, a round not
// waiting for another event's requests; an event still worked on from the
// round before (its market-data slow) skips this one (review C58 ③).
func (o *Overlays) Round(ctx context.Context) {
	if o.market == nil {
		return
	}
	now := o.now()
	on := o.flags.Enabled(flags.KeyOverlay, flags.Subject{})
	o.mu.Lock()
	runs := make([]*overlayRun, 0, len(o.open))
	for _, r := range o.open {
		runs = append(runs, r)
	}
	o.mu.Unlock()
	for _, r := range runs {
		if !r.mu.TryLock() {
			continue
		}
		o.busy.Add(1)
		go func() {
			defer o.busy.Done()
			defer r.mu.Unlock()
			o.work(ctx, r, now, on)
		}()
	}
}

// work does a round's work on one event; r.mu is held.
func (o *Overlays) work(ctx context.Context, r *overlayRun, now time.Time, on bool) {
	switch {
	case r.gone:
	case r.e.Status == domain.EventScheduled && r.e.StartsAt.After(now):
	case r.e.Status == domain.EventScheduled && !on:
		o.cancel(ctx, r, now, "market.overlay is off at its start")
	case r.e.Status == domain.EventScheduled:
		o.start(ctx, r, now)
	case !on:
		o.abort(ctx, r, now, overlayFlagActor, "market.overlay was switched off")
	default:
		o.step(ctx, r, now)
	}
}

// cancel ends a scheduled event that cannot start.
func (o *Overlays) cancel(ctx context.Context, r *overlayRun, now time.Time, why string) {
	e := r.e
	e.Status, e.Result, e.EndedAt, e.EndedBy = domain.EventCanceled, domain.ResultCanceled, now, overlaySystem
	if err := o.store.SaveEvent(ctx, e, o.audit(ctx, e, "market.sim.event_canceled", overlaySystem, why, now)); err != nil {
		o.saves.Inc()
		o.log.WarnContext(ctx, "price event: its cancellation not saved", "event", e.ID, "error", err)
		return
	}
	r.e = e
	o.forget(r)
	o.log.WarnContext(ctx, "price event canceled", "event", e.ID, "symbol", e.Symbol, "why", why)
}

// start starts a scheduled event from the reference price now, its whole
// schedule from now; one made well ahead has HOUSE's worst loss checked
// again. One that cannot start within overlayStartGrace of its time (the
// reference price unread, market-sim down) is canceled.
func (o *Overlays) start(ctx context.Context, r *overlayRun, now time.Time) {
	e := r.e
	if now.Sub(e.StartsAt) > overlayStartGrace {
		o.cancel(ctx, r, now, "not started within a minute of its time")
		return
	}
	ref, err := o.market.Followed(ctx, e.Symbol)
	if err != nil || !ref.Followed || !ref.Fresh || !ref.Source.IsPositive() {
		return // the reference price next second
	}
	if e.StartsAt.Sub(e.CreatedAt) > overlayRecheck {
		loss, err := o.lossOf(ctx, e.Symbol, e.TargetFactor, e.Risk)
		switch {
		case err != nil:
			return // HOUSE's rooms next second
		case loss.GreaterThan(o.cfg.MaxLoss):
			o.cancel(ctx, r, now, fmt.Sprintf("HOUSE's worst loss %s USDT is beyond the cap %s at its start", loss.Round(2), o.cfg.MaxLoss))
			return
		}
	}
	e.Status, e.StartedAt, e.BasePrice = domain.EventRunning, now, ref.Source
	if err := o.store.SaveEvent(ctx, e, nil); err != nil {
		o.saves.Inc()
		o.log.WarnContext(ctx, "price event: its start not saved", "event", e.ID, "error", err)
		return
	}
	r.e, r.pushedAt = e, now
	o.log.InfoContext(ctx, "price event started", "event", e.ID, "symbol", e.Symbol, "reference", ref.Source, "factor", e.TargetFactor)
	o.step(ctx, r, now)
}

// step pushes a running event's factor now and notes the platform's peak;
// back at 1 for good, the event is done.
func (o *Overlays) step(ctx context.Context, r *overlayRun, now time.Time) {
	f, done := r.e.OverlayAt(now)
	if done {
		o.finish(ctx, r, now)
		return
	}
	if !o.push(ctx, r, f, now) {
		return
	}
	ref, err := o.market.Followed(ctx, r.e.Symbol)
	if err != nil || !ref.Shown.IsPositive() {
		return
	}
	peak := r.e.PeakPrice
	moved := !peak.IsPositive() || (r.e.TargetFactor > 1 && ref.Shown.GreaterThan(peak)) || (r.e.TargetFactor < 1 && ref.Shown.LessThan(peak))
	if moved {
		r.e.PeakPrice = ref.Shown
	}
	// The peak is saved every overlayPeakEvery while it moves, and once it
	// stops: a restart goes on from it.
	if !r.e.PeakPrice.Equal(r.savedPeak) && (!moved || now.Sub(r.peakAt) >= overlayPeakEvery) {
		r.peakAt = now
		if err := o.store.SaveEvent(ctx, r.e, nil); err != nil {
			o.saves.Inc()
			return
		}
		r.savedPeak = r.e.PeakPrice
	}
}

// send pushes the factor f of a running event to market-data.
func (o *Overlays) send(ctx context.Context, e domain.Event, f float64, now time.Time) error {
	err := o.market.Push(ctx, e.Symbol, ports.OverlayPush{
		Factor: decimal.NewFromFloat(f).Round(8), Until: now.Add(overlayAhead), Risk: e.Risk, EventID: e.ID, Seq: now.UnixMilli(),
		EndsAt: e.OverlayEnds(), StartedAt: e.StartedAt,
	})
	if err != nil {
		o.pushes.WithLabelValues("failed").Inc()
		return err
	}
	o.pushes.WithLabelValues("ok").Inc()
	return nil
}

// push sends the factor f of a running event; false when the event is
// canceled for its failures (overlayDips, overlayGiveUp).
func (o *Overlays) push(ctx context.Context, r *overlayRun, f float64, now time.Time) bool {
	e := r.e
	if err := o.send(ctx, e, f, now); err != nil {
		code := ports.Code(err)
		if ports.Refused(err) && code != "MARKET_OVERLAY_OFF" && code != "MARKET_OVERLAY_BUSY" {
			o.abort(ctx, r, now, overlaySystem, fmt.Sprintf("market-data refused its factor (%s)", code))
			return false
		}
		quiet := now.Sub(r.pushedAt)
		if quiet >= overlayAhead && !r.dipped { // the last success's factor ran out (review C59 ①)
			r.dipped = true
			r.dips++
		}
		if r.dips > overlayDips || quiet >= overlayGiveUp {
			o.abort(ctx, r, now, overlaySystem, fmt.Sprintf("no push of its factor for %s, the pair back at the reference price %d times (%s)",
				quiet.Round(time.Second), r.dips, err))
			return false
		}
		if !r.failing {
			o.log.WarnContext(ctx, "price event: its factor not pushed; again next second", "event", e.ID, "symbol", e.Symbol, "error", err)
		}
		r.failing = true
		return true
	}
	if r.failing {
		o.log.InfoContext(ctx, "price event: its factor pushed again", "event", e.ID, "symbol", e.Symbol)
	}
	r.failing, r.dipped, r.pushedAt = false, false, now
	o.factor.WithLabelValues(e.Symbol).Set(f)
	return true
}

// finish ends an event back at 1: the last push says so (market-data ends
// the overlay at once), the reference and the platform's prices then are
// its end, and it is done (CANCELED when an operator ended it).
func (o *Overlays) finish(ctx context.Context, r *overlayRun, now time.Time) {
	if err := o.send(ctx, r.e, 1, now); err != nil { // market-data drops the factor by itself in seconds
		o.log.WarnContext(ctx, "price event: its last push failed", "event", r.e.ID, "symbol", r.e.Symbol, "error", err)
	}
	o.factor.DeleteLabelValues(r.e.Symbol)
	e := r.e
	if ref, err := o.market.Followed(ctx, e.Symbol); err == nil {
		e.EndReferencePrice, e.EndPlatformPrice = ref.Source, ref.Shown
	}
	e.Status = domain.EventDone
	if e.EndedAt.IsZero() {
		e.EndedAt = now
	}
	actor := e.EndedBy
	if actor == "" {
		actor = overlaySystem
	}
	if err := o.store.SaveEvent(ctx, e, o.audit(ctx, e, "market.sim.event_done", actor, "back at the reference price", now)); err != nil {
		o.saves.Inc()
		o.log.WarnContext(ctx, "price event: its end not saved; again next second", "event", e.ID, "error", err)
		return
	}
	r.e = e
	o.forget(r)
	o.log.InfoContext(ctx, "price event done", "event", e.ID, "symbol", e.Symbol, "result", e.Result, "base", e.BasePrice, "peak", e.PeakPrice,
		"end_reference", e.EndReferencePrice, "end_platform", e.EndPlatformPrice)
}

// abort ends a running event at once for actor (market.overlay switched
// off: market-data is at 1 already; its pushes failing): market-data's
// overlay is cleared, the event is done, CANCELED, why audited.
func (o *Overlays) abort(ctx context.Context, r *overlayRun, now time.Time, actor, why string) {
	if err := o.market.Clear(ctx, r.e.Symbol); err != nil {
		o.log.WarnContext(ctx, "price event: its overlay not cleared", "event", r.e.ID, "error", err)
	}
	o.factor.DeleteLabelValues(r.e.Symbol)
	e := r.e
	e.Status, e.Result, e.EndedBy = domain.EventDone, domain.ResultCanceled, actor
	if e.EndedAt.IsZero() {
		e.EndedAt = now
	}
	if ref, err := o.market.Followed(ctx, e.Symbol); err == nil {
		e.EndReferencePrice, e.EndPlatformPrice = ref.Source, ref.Shown
	}
	if err := o.store.SaveEvent(ctx, e, o.audit(ctx, e, "market.sim.event_ended", actor, why, now)); err != nil {
		o.saves.Inc()
		o.log.WarnContext(ctx, "price event: its end not saved; again next second", "event", e.ID, "error", err)
		return
	}
	r.e = e
	o.forget(r)
	o.log.WarnContext(ctx, "price event canceled", "event", e.ID, "symbol", e.Symbol, "why", why)
}
