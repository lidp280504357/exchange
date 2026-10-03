package application

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
)

// The price band must not lock the market (ASTRA design §4, the user's
// decision 2026-10-02), in three layers:
//
//  1. The makers quote around the target pulled into the band around the
//     anchor as the trading service has it (domain.QuoteCenter); while the
//     target is beyond, the executors trade at the quotes, the anchor
//     follows and the quotes walk on.
//  2. The target is reported to market-data-service, where it stands in
//     for the pair's reference while its book has no middle: once the
//     pair has gone five minutes without a trade, the trading service
//     anchors the band on the book's middle or on the target.
//  3. A watchdog: three minutes without a trade, or with every level the
//     makers place refused for the band, rebases the model at the anchor
//     and is counted for the alert MarketSimBandDeadlock.
const (
	anchorEvery  = time.Second
	anchorWindow = 3 * time.Second
	reportEvery  = 5 * time.Second
	// beatStale stops the heartbeat when no round started for this long:
	// the round loop is stuck and the pair should halt.
	beatStale = 30 * time.Second
	// recentTrade is how old a last trade may be and still anchor the band
	// (the trading service's prices.RecentTrade).
	recentTrade = 5 * time.Minute
	watchAfter  = 3 * time.Minute
	// staleReads is how long the last trade may go unread before the
	// watchdog, unable to tell a locked market from a silent
	// market-data-service, starts over.
	staleReads = 10 * time.Second
	// watchdogActor ends the events the watchdog stops.
	watchdogActor = "system:watchdog"
)

// refreshAnchor reads the pair's last trade and the band's anchor as the
// trading service works it out: the last trade of the last five minutes,
// else the pair's fresh reference (its book's middle, or the target
// reported), else the last trade however old.
func (s *Sim) refreshAnchor(ctx context.Context, now time.Time) {
	if !s.anchorAt.IsZero() && now.Sub(s.anchorAt) < anchorEvery {
		return
	}
	s.anchorAt = now
	price, at, err := s.prices.LastTrade(ctx, s.cfg.Symbol)
	if err != nil {
		// The reads of before stay: without them the quotes would center
		// on the bare target and the band would refuse their far levels
		// while market data is out (the watchdog waits meanwhile, readAt).
		s.m.errors.WithLabelValues("last_trade").Inc()
		return
	}
	s.readAt = now
	s.anchorReads = slices.DeleteFunc(s.anchorReads, func(m domain.Mark) bool { return now.Sub(m.At) >= anchorWindow })
	if price.IsPositive() {
		s.last, s.lastTradeAt = price, at
		s.m.last.Set(price.InexactFloat64())
	}
	anchor := price
	if !price.IsPositive() || now.Sub(at) > recentTrade {
		if ref, fresh, err := s.prices.Reference(ctx, s.cfg.Symbol); err != nil {
			s.m.errors.WithLabelValues("reference").Inc()
		} else if fresh && ref.IsPositive() {
			anchor = ref
		}
	}
	if anchor.IsPositive() {
		s.anchorReads = append(s.anchorReads, domain.Mark{At: now, P: anchor.InexactFloat64()})
	}
}

// anchors are the band's anchor as read in the last anchorWindow.
func (s *Sim) anchors() domain.Anchors {
	var a domain.Anchors
	for i, m := range s.anchorReads {
		if i == 0 || m.P < a.Lo {
			a.Lo = m.P
		}
		if i == 0 || m.P > a.Hi {
			a.Hi = m.P
		}
	}
	return a
}

// anchor is the latest read of the band's anchor, 0 when there is none.
func (s *Sim) anchor() float64 {
	if n := len(s.anchorReads); n > 0 {
		return s.anchorReads[n-1].P
	}
	return 0
}

// beat is what the heartbeat reports: the target on the pair's tick, as
// a round saw it, and when.
type beat struct {
	at    time.Time
	price decimal.Decimal
}

// noteBeat keeps the target p of a round starting at now for the
// heartbeat.
func (s *Sim) noteBeat(now time.Time, p float64) {
	if p <= 0 {
		return
	}
	price := decimal.NewFromFloat(p)
	if s.pair.Tick.IsPositive() {
		price = price.Div(s.pair.Tick).Round(0).Mul(s.pair.Tick)
	}
	if price.IsPositive() {
		s.beat.Store(&beat{at: now, price: price})
	}
}

// heartbeat reports the target to market-data-service every reportEvery
// until ctx ends. The report is the heartbeat that service watches (a
// pair whose simulated market went silent halts, ASTRA design §9): it
// goes out on its own, so that neither a slow round nor a slow
// market-data-service holds up the other, and whether the bots trade or
// not; a round loop stuck for beatStale stops it.
func (s *Sim) heartbeat(ctx context.Context) {
	t := time.NewTicker(reportEvery)
	defer t.Stop()
	for {
		s.beatOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Sim) beatOnce(ctx context.Context) {
	b := s.beat.Load()
	if b == nil || s.now().Sub(b.at) >= beatStale {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, reportEvery)
	defer cancel()
	if err := s.prices.Report(ctx, s.cfg.Symbol, b.price); err != nil && ctx.Err() == nil {
		s.m.errors.WithLabelValues("report").Inc()
	}
}

// refused notes how a maker's requote went for the band: placed is how
// many levels the platform took, outOfBand how many it refused for the
// band, wanted how many were to place.
func (s *Sim) refused(now time.Time, wanted, placed, outOfBand int) {
	switch {
	case wanted == 0 || placed > 0:
		s.refusedSince = time.Time{}
	case outOfBand > 0 && s.refusedSince.IsZero():
		s.refusedSince = now
	}
}

// watch is the watchdog of a locked market: while the bots trade and no
// pause or halt runs, three minutes without a trade where trades are
// expected, or with every level the makers placed refused for the band,
// end a running jump or target and rebase the model at the band's
// anchor. Without fresh reads of the last trade it starts over.
func (s *Sim) watch(ctx context.Context, now time.Time, sh domain.Shape) {
	if sh.Halted || s.runs(domain.EventPause) || s.watchFrom.IsZero() || now.Sub(s.readAt) > staleReads {
		s.watchFrom, s.refusedSince = now, time.Time{}
		return
	}
	// Without takers (daily_volume 0) nothing need trade while the quotes
	// stand: only a walk or an event moving the price expects trades, and
	// the quiet counts from then.
	expected := s.params.DailyVolume > 0 || s.walking || slices.ContainsFunc(s.runningEvents(), func(e *domain.Event) bool {
		return e.Type == domain.EventJump || e.Type == domain.EventTarget
	})
	if !expected {
		s.watchFrom = now
	}
	from := s.watchFrom
	if s.lastTradeAt.After(from) {
		from = s.lastTradeAt
	}
	quiet := now.Sub(from) >= watchAfter
	refused := !s.refusedSince.IsZero() && now.Sub(s.refusedSince) >= watchAfter
	if !quiet && !refused {
		return
	}
	why := "nothing traded for three minutes"
	if refused {
		why = "the price band refused every level the makers placed for three minutes"
	}
	target, anchor := s.model.State.P, s.anchor()
	for _, e := range s.runningEvents() {
		if e.Type != domain.EventJump && e.Type != domain.EventTarget {
			continue
		}
		e.Status, e.EndedAt, e.EndedBy = domain.EventDone, now, watchdogActor
		if e.Type == domain.EventTarget && e.CrossedAt.IsZero() {
			e.Result = domain.ResultCanceled
		}
		s.persist(ctx, e, &ports.Audit{
			Action: "market.sim.event_ended", Target: "sim-event:" + e.ID, Actor: watchdogActor, Reason: "the market was locked: " + why,
			Details: fmt.Sprintf(`{"type":%q,"target":%v,"anchor":%v,"result":%q}`, e.Type, target, anchor, e.Result),
		})
		if e.Type == domain.EventTarget {
			s.m.targets.WithLabelValues(e.Result).Inc()
			s.cancelSpikes(ctx, e.ID, now, watchdogActor)
		}
		s.movedAt = now
	}
	s.events = slices.DeleteFunc(s.events, func(e *domain.Event) bool { return e.Status == domain.EventDone })
	if anchor > 0 {
		s.model.Rebase(anchor)
		s.save(ctx) // a restart goes on from the anchor, not from the lock
	}
	s.watchFrom, s.refusedSince = now, time.Time{}
	s.deadlocks, s.deadlockAt = s.deadlocks+1, now
	s.m.deadlocks.Inc()
	s.log.WarnContext(ctx, "simulated market: the market was locked; the model goes on from the band's anchor",
		"symbol", s.cfg.Symbol, "why", why, "target", target, "anchor", anchor)
}
