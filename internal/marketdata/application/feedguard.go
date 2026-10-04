package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// Reference feed states (ADR-0010): clients show a banner once their data
// is FeedDelayed old; after FeedHaltAfter without data the guard halts the
// followed pairs while market.halt_on_feed_loss is on.
const (
	FeedDelayed   = 30 * time.Second
	FeedHaltAfter = 5 * time.Minute
	// feedResumeAfter is how long the feed must be back before the halted
	// pairs trade again.
	feedResumeAfter = 30 * time.Second
	haltReason      = "reference feed lost (market.halt_on_feed_loss)"
	resumeReason    = "reference feed recovered"
)

// FeedState is the reference feed's condition.
type FeedState string

// The feed states.
const (
	FeedOff     FeedState = "OFF"     // market.reference_feed is off
	FeedOK      FeedState = "OK"      // data within FeedDelayed
	FeedLate    FeedState = "DELAYED" // no data for FeedDelayed
	FeedDown    FeedState = "DOWN"    // no data for FeedHaltAfter
	pairTrading           = "TRADING"
	pairHalt              = "HALT"
)

// FeedStatus is what the admin console shows of the feed.
type FeedStatus struct {
	State    FeedState
	Received time.Time
	Followed []string
	Halted   []ports.Halt
}

// FeedGuard halts the pairs that follow a reference market when the feed
// has been lost for FeedHaltAfter while market.halt_on_feed_loss is on,
// and resumes the pairs it halted once the feed has been back for
// feedResumeAfter, or when either flag goes off. The halted pairs are
// recorded before they are halted, so a restart resumes them too; a pair
// an operator moved in the meantime is left as it is.
type FeedGuard struct {
	feed        *ReferenceFeed
	instruments ports.Instruments
	store       ports.Store
	flags       Flags
	log         *slog.Logger
	now         func() time.Time
	check       time.Duration

	// since is when the guard first saw the feed on (the loss of a feed
	// that never connected counts from there); backSince when data last
	// started arriving again.
	since     time.Time
	backSince time.Time

	state  *prometheus.GaugeVec
	halted prometheus.Gauge
}

// NewFeedGuard watches feed and registers market_reference_feed_state (1
// for the current state) and market_feed_halted_pairs with reg.
func NewFeedGuard(feed *ReferenceFeed, instruments ports.Instruments, store ports.Store, fl Flags, log *slog.Logger,
	reg prometheus.Registerer,
) *FeedGuard {
	g := &FeedGuard{
		feed: feed, instruments: instruments, store: store, flags: fl, log: log, now: time.Now, check: 5 * time.Second,
		state: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_reference_feed_state", Help: "1 for the reference feed's current state (OFF, OK, DELAYED, DOWN).",
		}, []string{"state"}),
		halted: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "market_feed_halted_pairs", Help: "Pairs halted because the reference feed was lost.",
		}),
	}
	reg.MustRegister(g.state, g.halted)
	return g
}

// State returns the feed's condition now; OFF while the feed is off or
// follows nothing.
func (g *FeedGuard) State() FeedState {
	if !g.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{}) || len(g.feed.Followed()) == 0 {
		return FeedOff
	}
	last := g.feed.Received()
	if last.Before(g.since) {
		last = g.since
	}
	switch age := g.now().Sub(last); {
	case last.IsZero() || age < FeedDelayed:
		return FeedOK
	case age < FeedHaltAfter:
		return FeedLate
	}
	return FeedDown
}

// Status returns the feed's condition and the pairs halted for it.
func (g *FeedGuard) Status(ctx context.Context) (FeedStatus, error) {
	halted, err := g.store.Read().Halts().List(ctx)
	if err != nil {
		return FeedStatus{}, err
	}
	followed := []string{}
	for _, r := range g.feed.Followed() {
		followed = append(followed, r.Symbol)
	}
	return FeedStatus{State: g.State(), Received: g.feed.Received(), Followed: followed, Halted: halted}, nil
}

// Run checks the feed until ctx ends (an app.Loop body).
func (g *FeedGuard) Run(ctx context.Context) error {
	t := time.NewTicker(g.check)
	defer t.Stop()
	for {
		if err := g.Step(ctx); err != nil && ctx.Err() == nil {
			g.log.WarnContext(ctx, "feed guard step failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step looks at the feed once, halting or resuming pairs as needed.
func (g *FeedGuard) Step(ctx context.Context) error {
	now := g.now()
	feedOn := g.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{})
	switch {
	case !feedOn:
		g.since, g.backSince = time.Time{}, time.Time{}
	case g.since.IsZero():
		g.since = now
	}
	state := g.State()
	for _, s := range []FeedState{FeedOff, FeedOK, FeedLate, FeedDown} {
		v := 0.0
		if s == state {
			v = 1
		}
		g.state.WithLabelValues(string(s)).Set(v)
	}
	switch {
	case state != FeedOK:
		g.backSince = time.Time{}
	case g.backSince.IsZero():
		g.backSince = now
	}
	haltOn := g.flags.Enabled(flags.KeyHaltOnFeedLoss, flags.Subject{})
	if state == FeedDown && haltOn {
		if err := g.halt(ctx, now); err != nil {
			return err
		}
	}
	halts, err := g.store.Read().Halts().List(ctx)
	if err != nil {
		return err
	}
	g.halted.Set(float64(len(halts)))
	back := state == FeedOK && !g.backSince.IsZero() && now.Sub(g.backSince) >= feedResumeAfter
	if len(halts) > 0 && (back || !feedOn || !haltOn) {
		return g.resume(ctx, halts)
	}
	return nil
}

// halt halts the followed pairs that are trading.
func (g *FeedGuard) halt(ctx context.Context, now time.Time) error {
	pairs, err := g.instruments.Pairs(ctx)
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if p.Reference.Remote == "" || p.Status != pairTrading {
			continue
		}
		if err := g.store.Read().Halts().Add(ctx, p.Symbol, now); err != nil {
			return err
		}
		if _, err := g.instruments.SetPairStatus(ctx, p.Symbol, pairHalt, haltReason); err != nil {
			if !apperr.Is(err, "INSTRUMENT_STATUS_TRANSITION_INVALID") {
				return err
			}
		}
		g.log.WarnContext(ctx, "pair halted: reference feed lost", "symbol", p.Symbol, "since", g.feed.Received())
	}
	return nil
}

// resume moves the pairs the guard halted back to trading; one an
// operator moved elsewhere in the meantime is only forgotten.
func (g *FeedGuard) resume(ctx context.Context, halts []ports.Halt) error {
	pairs, err := g.instruments.Pairs(ctx)
	if err != nil {
		return err
	}
	status := map[string]string{}
	for _, p := range pairs {
		status[p.Symbol] = p.Status
	}
	for _, h := range halts {
		if status[h.Symbol] == pairHalt {
			if _, err := g.instruments.SetPairStatus(ctx, h.Symbol, pairTrading, resumeReason); err != nil &&
				!apperr.Is(err, "INSTRUMENT_STATUS_TRANSITION_INVALID") {
				return err
			}
			g.log.InfoContext(ctx, "pair resumed: reference feed back", "symbol", h.Symbol)
		}
		if err := g.store.Read().Halts().Remove(ctx, h.Symbol); err != nil {
			return err
		}
	}
	return nil
}
