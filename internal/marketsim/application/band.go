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
	// recentTrade is how old a last trade may be and still anchor the band
	// (the trading service's prices.RecentTrade).
	recentTrade = 5 * time.Minute
	watchAfter  = 3 * time.Minute
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
		s.m.errors.WithLabelValues("last_trade").Inc()
		return
	}
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
	s.anchorReads = slices.DeleteFunc(s.anchorReads, func(m domain.Mark) bool { return now.Sub(m.At) >= anchorWindow })
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

// report gives market-data-service the target, on the pair's tick, every
// reportEvery.
func (s *Sim) report(ctx context.Context, now time.Time, p float64) {
	if (!s.reportedAt.IsZero() && now.Sub(s.reportedAt) < reportEvery) || p <= 0 {
		return
	}
	s.reportedAt = now
	price := decimal.NewFromFloat(p)
	if s.pair.Tick.IsPositive() {
		price = price.Div(s.pair.Tick).Round(0).Mul(s.pair.Tick)
	}
	if !price.IsPositive() {
		return
	}
	if err := s.prices.Report(ctx, s.cfg.Symbol, price); err != nil {
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
// pause or halt runs, three minutes without a trade, or with every level
// the makers placed refused for the band, end a running jump or target
// and rebase the model at the band's anchor.
func (s *Sim) watch(ctx context.Context, now time.Time, sh domain.Shape) {
	if sh.Halted || s.runs(domain.EventPause) || s.watchFrom.IsZero() {
		s.watchFrom, s.refusedSince = now, time.Time{}
		return
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
		s.persist(ctx, e, &ports.Audit{
			Action: "market.sim.event_ended", Target: "sim-event:" + e.ID, Actor: watchdogActor, Reason: "the market was locked: " + why,
			Details: fmt.Sprintf(`{"type":%q,"target":%v,"anchor":%v}`, e.Type, target, anchor),
		})
		s.movedAt = now
	}
	s.events = slices.DeleteFunc(s.events, func(e *domain.Event) bool { return e.Status == domain.EventDone })
	if anchor > 0 {
		s.model.Rebase(anchor)
	}
	s.watchFrom, s.refusedSince = now, time.Time{}
	s.deadlocks, s.deadlockAt = s.deadlocks+1, now
	s.m.deadlocks.Inc()
	s.log.WarnContext(ctx, "simulated market: the market was locked; the model goes on from the band's anchor",
		"symbol", s.cfg.Symbol, "why", why, "target", target, "anchor", anchor)
}
