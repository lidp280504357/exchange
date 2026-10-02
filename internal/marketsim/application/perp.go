package application

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// The platform coin's perpetual (ASTRA design §5.2, batch A4): with
// sim.perp on and the contract trading, the makers keep ladders on it
// around the target as on the spot pair, and the takers trade it at the
// market. Each bot's position stays within PerpBotCap worth (beyond it the
// bot only reduces), and its FUTURES margin is topped up to PerpMargin
// from its spot USDT. The bots' positions net out across the pool.

// perpCheckEvery is how often the bots' positions and margins are read.
const perpCheckEvery = time.Minute

// perpBot is what the simulation knows of a bot on the perpetual.
type perpBot struct {
	quotedP   float64
	nextQuote time.Time
	position  decimal.Decimal // signed, at the last check plus own market orders
	futures   decimal.Decimal // available FUTURES balance at the last check
	known     bool
}

func (s *Sim) perpOf(b *bot) *perpBot {
	if s.perpBots == nil {
		s.perpBots = map[string]*perpBot{}
	}
	pb, ok := s.perpBots[b.UserID]
	if !ok {
		pb = &perpBot{}
		s.perpBots[b.UserID] = pb
	}
	return pb
}

// perp runs the bots on the perpetual for a round.
func (s *Sim) perp(ctx context.Context, now time.Time, p float64, dt time.Duration) {
	if s.cfg.Perp == "" || s.Derivatives == nil {
		return
	}
	if s.perpPairAt.IsZero() || now.Sub(s.perpPairAt) >= pairEvery {
		if k, err := s.Derivatives.Contract(ctx, s.cfg.Perp); err == nil {
			s.perpPair, s.perpPairAt = k, now
		} else {
			s.m.errors.WithLabelValues("contract").Inc()
		}
	}
	if !s.flags.Enabled(flags.KeySimPerp, flags.Subject{Symbol: s.cfg.Perp}) || !s.perpPair.Trading {
		if s.perpRunning {
			s.stopPerp(ctx)
		}
		return
	}
	if !s.perpRunning || now.Sub(s.perpCheckedAt) >= perpCheckEvery {
		s.checkPerp(ctx, now)
	}
	s.perpRunning = true
	s.quotePerp(ctx, now, p)
	if !s.runs(domain.EventPause) {
		s.takePerp(ctx, now, p, dt)
	}
}

// checkPerp reads the bots' positions and FUTURES balances and tops up a
// margin below half of PerpMargin from the bot's spot USDT.
func (s *Sim) checkPerp(ctx context.Context, now time.Time) {
	s.perpCheckedAt = now
	target := decimal.NewFromFloat(s.params.PerpMargin)
	for _, b := range s.bots {
		if !b.Enabled || (b.Role != domain.RoleMaker && b.Role != domain.RoleTaker) {
			continue
		}
		pb := s.perpOf(b)
		pos, err := s.Derivatives.Position(ctx, b.UserID, s.cfg.Perp)
		if err != nil {
			s.fail(ctx, b, "position", err)
			continue
		}
		fut, err := s.Derivatives.Futures(ctx, b.UserID)
		if err != nil {
			s.fail(ctx, b, "futures", err)
			continue
		}
		pb.position, pb.futures, pb.known = pos, fut, true
		if fut.LessThan(target.Div(decimal.NewFromInt(2))) {
			need := target.Sub(fut).Round(2)
			key := fmt.Sprintf("sim-margin-%s-%d", b.UserID, now.Unix()/60)
			if err := s.Derivatives.ToFutures(ctx, b.UserID, need, key); err != nil {
				s.fail(ctx, b, "to_futures", err)
				continue
			}
			pb.futures = pb.futures.Add(need)
			s.log.InfoContext(ctx, "simulated market: a bot's margin topped up", "bot", b.Label, "usdt", need.String())
		}
	}
}

// overCap reports whether a position is worth PerpBotCap or more at p.
func (s *Sim) overCap(pos decimal.Decimal, p float64) bool {
	return math.Abs(pos.InexactFloat64())*p >= s.params.PerpBotCap
}

// quotePerp lets the perpetual's makers requote in turn, one a round:
// their ladder around the target, without the side that would grow a
// position at the cap.
func (s *Sim) quotePerp(ctx context.Context, now time.Time, p float64) {
	makers := s.botsOf(domain.RoleMaker)
	if len(makers) == 0 {
		return
	}
	tick := s.perpPair.Tick.InexactFloat64()
	for i := range makers {
		b := makers[(s.perpTurn+i)%len(makers)]
		pb := s.perpOf(b)
		moved := tick > 0 && math.Abs(p-pb.quotedP)/tick >= float64(s.params.RequoteTick)
		if !moved && now.Before(pb.nextQuote) {
			continue
		}
		s.requotePerp(ctx, now, b, pb, p)
		break
	}
	s.perpTurn = (s.perpTurn + 1) % len(makers)
}

func (s *Sim) requotePerp(ctx context.Context, now time.Time, b *bot, pb *perpBot, p float64) {
	rng := s.model.Rand()
	pb.nextQuote = now.Add(time.Second + time.Duration(rng.Int64N(int64(2*time.Second))))
	open, err := s.Derivatives.OpenContract(ctx, b.UserID, s.cfg.Perp)
	if err != nil {
		s.fail(ctx, b, "open_contract", err)
		return
	}
	bids, asks := domain.LadderPrices(p, b.phase, s.params, s.perpPair)
	if s.overCap(pb.position, p) {
		if pb.position.IsPositive() {
			bids = nil // long at the cap: no more buying
		} else {
			asks = nil
		}
	}
	cancel, placeBids, placeAsks := domain.Diff(open, bids, asks)
	for _, o := range cancel {
		if !s.cancels.Take(now) {
			s.m.throttled.WithLabelValues("cancel").Inc()
			break
		}
		if err := s.Derivatives.CancelContract(ctx, b.UserID, o.ID); err != nil {
			s.fail(ctx, b, "cancel_contract", err)
			continue
		}
		s.m.cancels.WithLabelValues("PERP_" + string(b.Role)).Inc()
	}
	place := func(side domain.Side, price decimal.Decimal) bool {
		if !s.orders.Take(now) {
			s.m.throttled.WithLabelValues("order").Inc()
			return false
		}
		qty := domain.Quantity(domain.Worth(rng, s.params.LevelSize, 0.5), price, s.perpPair)
		_, err := s.Derivatives.LimitContract(ctx, b.UserID, s.cfg.Perp, side, price, qty)
		s.placedPerp(ctx, b, err)
		return true
	}
	for i := 0; i < max(len(placeBids), len(placeAsks)); i++ {
		if i < len(placeBids) && !place(domain.Buy, placeBids[i]) {
			break
		}
		if i < len(placeAsks) && !place(domain.Sell, placeAsks[i]) {
			break
		}
	}
	pb.quotedP = p
}

// takePerp sends the takers' market orders on the perpetual that arrived
// in dt; a taker at its cap only reduces.
func (s *Sim) takePerp(ctx context.Context, now time.Time, p float64, dt time.Duration) {
	takers := s.botsOf(domain.RoleTaker)
	if len(takers) == 0 || s.params.PerpDailyVolume <= 0 {
		return
	}
	rng := s.model.Rand()
	flow := s.params
	flow.DailyVolume = s.params.PerpDailyVolume
	for range domain.Arrivals(rng, flow, dt, now) {
		b := takers[rng.IntN(len(takers))]
		pb := s.perpOf(b)
		side, reduce := domain.TakerSide(rng, s.params, 0), false
		if s.overCap(pb.position, p) {
			side, reduce = domain.Sell, true
			if pb.position.IsNegative() {
				side = domain.Buy
			}
		}
		qty := domain.Quantity(domain.Worth(rng, s.params.OrderSize, 0.8), decimal.NewFromFloat(p), s.perpPair)
		if reduce {
			qty = decimal.Min(qty, pb.position.Abs())
		}
		if !qty.IsPositive() {
			continue
		}
		if !s.orders.Take(now) {
			s.m.throttled.WithLabelValues("order").Inc()
			return
		}
		err := s.Derivatives.MarketContract(ctx, b.UserID, s.cfg.Perp, side, qty, reduce)
		s.placedPerp(ctx, b, err)
		if err == nil {
			if side == domain.Buy {
				pb.position = pb.position.Add(qty)
			} else {
				pb.position = pb.position.Sub(qty)
			}
		}
	}
}

func (s *Sim) placedPerp(ctx context.Context, b *bot, err error) {
	role := "PERP_" + string(b.Role)
	switch {
	case err == nil:
		s.m.orders.WithLabelValues(role, "placed").Inc()
	case isFunds(err):
		s.m.orders.WithLabelValues(role, "unfunded").Inc()
	default:
		s.m.orders.WithLabelValues(role, "failed").Inc()
		s.fail(ctx, b, "contract_order", err)
	}
}

// stopPerp cancels the makers' orders on the perpetual.
func (s *Sim) stopPerp(ctx context.Context) {
	for _, b := range s.botsOf(domain.RoleMaker) {
		if err := s.Derivatives.CancelAllContract(ctx, b.UserID, s.cfg.Perp); err != nil {
			s.fail(ctx, b, "cancel_all_contract", err)
		}
		pb := s.perpOf(b)
		pb.quotedP, pb.nextQuote = 0, time.Time{}
	}
	s.perpRunning = false
	s.log.InfoContext(ctx, "simulated market: the bots left the perpetual", "symbol", s.cfg.Perp)
}
