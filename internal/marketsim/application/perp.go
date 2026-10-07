package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketsim/domain"
	"github.com/skill/exchange/internal/marketsim/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

// The platform coin's perpetuals (ASTRA design §4 and §5.2, batch A4; the
// coin-margined one, design 2026-10-06 §2.3, batch G2): with sim.perp on
// for a contract and the contract trading, the makers keep ladders on it
// around its mark price (the design's quote center for the perpetual: the
// contract's price band is around it), pulled halfway to the index — the
// mark is the index times one plus the EMA of the book's premium, so
// quotes at the mark itself would keep any premium the takers' flow made,
// and the funding rate with it; halfway, the premium decays — and the
// takers trade it at the market. Each contract is run on its own, with
// the same settings: each bot's position on it stays within PerpBotCap
// worth (beyond it the bot only reduces), and its FUTURES margin in the
// contract's settlement asset is topped up to PerpMargin worth from its
// spot holdings — USDT for the linear contract, the coin for the
// coin-margined one, whose quantities are whole contracts of a USD face
// value. The bots' positions net out across the pool.

// perpCheckEvery is how often the bots' positions and margins are read
// (often enough that the makers' fills between two reads take a position
// little beyond PerpBotCap), markEvery the mark price, perpTradeEvery the
// perpetual's last trade (for perpQuietTake).
const (
	perpCheckEvery = 10 * time.Second
	markEvery      = time.Second
	perpTradeEvery = 5 * time.Second
)

// perpQuietTake is how long a perpetual goes without a trade before a
// taker trades the contract's minimum anyway, as the pair's takers do
// after quietTake (coordinator 2026-10-04: about a fifth of the
// perpetual's minutes had no trade and its 1-minute candles broke up); not
// while paused (the design's §0 of 10-03: no orders in a pause or a halt;
// the perpetual's index is the spot pair's, whose own quiet taker keeps
// it). The order is the takers' (PERP_TAKER), at the market: it fills
// within the contract's band around the mark price. It counts in
// perp_daily_volume (the same decision): its worth is taken off the
// takers' next orders (domain.Repay), owed at most an hour of the budget
// (a budget below what the quiet orders alone trade cannot be kept).
const perpQuietTake = 45 * time.Second

// perpetual is the simulation's state on one of the coin's perpetuals.
type perpetual struct {
	symbol string
	pair   domain.Pair
	pairAt time.Time
	// The contract's mark and index prices as last read.
	mark, index float64
	markAt      time.Time
	running     bool
	turn        int
	bots        map[string]*perpBot
	checkedAt   time.Time
	// The contract's last trade as last read (perpTradeEvery), when the
	// bots started on it and its last quiet order (perpQuietTake).
	tradeAt, tradeReadAt, watchFrom, quietAt time.Time
	// quietDebt is what the quiet orders traded on the contract ahead of
	// perp_daily_volume, in USD, taken off the takers' next orders.
	quietDebt float64
	// capWarned is set once the coin margin's cap was logged.
	capWarned bool
}

// perpBot is what the simulation knows of a bot on a perpetual.
type perpBot struct {
	quotedP   float64
	nextQuote time.Time
	position  decimal.Decimal // signed, at the last check plus own market orders
	futures   decimal.Decimal // available FUTURES balance in the settlement asset at the last check
	known     bool
	// After a refused order the bot waits on the perpetual until retryAt.
	wait    time.Duration
	retryAt time.Time
}

func (pb *perpBot) ready(now time.Time) bool { return !now.Before(pb.retryAt) }

// perpetuals are the contracts the bots trade (Config.Perps), in order.
func (s *Sim) perpetuals() []*perpetual {
	if s.perps == nil {
		s.perps = []*perpetual{}
		for _, symbol := range s.cfg.Perps {
			s.perps = append(s.perps, &perpetual{symbol: symbol, bots: map[string]*perpBot{}})
		}
	}
	return s.perps
}

func (k *perpetual) botOf(b *bot) *perpBot {
	pb, ok := k.bots[b.UserID]
	if !ok {
		pb = &perpBot{}
		k.bots[b.UserID] = pb
	}
	return pb
}

// settleAsset is the contract's margin asset: its pair's, USDT until read.
func (k *perpetual) settleAsset() string {
	if k.pair.SettleAsset == "" {
		return "USDT"
	}
	return k.pair.SettleAsset
}

// runPerps runs the bots on each perpetual for a round.
func (s *Sim) runPerps(ctx context.Context, now time.Time, p float64, dt time.Duration) {
	if s.Derivatives == nil {
		return
	}
	for _, k := range s.perpetuals() {
		s.perp(ctx, k, now, p, dt)
	}
}

// stopPerps cancels the makers' orders on every perpetual they trade.
func (s *Sim) stopPerps(ctx context.Context) {
	for _, k := range s.perpetuals() {
		if k.running {
			s.stopPerp(ctx, k)
		}
	}
}

// perp runs the bots on one perpetual for a round; p is the coin's price
// in USDT, the quotes' center before the contract's first mark price.
func (s *Sim) perp(ctx context.Context, k *perpetual, now time.Time, p float64, dt time.Duration) {
	if k.pairAt.IsZero() || now.Sub(k.pairAt) >= pairEvery {
		if c, err := s.Derivatives.Contract(ctx, k.symbol); err == nil {
			k.pair, k.pairAt = c, now
		} else {
			s.failed("contract", k.symbol)
		}
	}
	if !s.flags.Enabled(flags.KeySimPerp, flags.Subject{Symbol: k.symbol}) || !k.pair.Trading || s.flags.Closed(lineOf(k.pair)) {
		if k.running {
			s.stopPerp(ctx, k)
		}
		return
	}
	if !k.running || now.Sub(k.checkedAt) >= perpCheckEvery {
		s.checkPerp(ctx, k, now, p)
	}
	if !k.running {
		k.watchFrom = now
	}
	k.running = true
	if k.markAt.IsZero() || now.Sub(k.markAt) >= markEvery {
		k.markAt = now
		if mark, index, err := s.prices.Mark(ctx, k.symbol); err != nil {
			s.failed("mark", k.symbol)
		} else if mark.IsPositive() {
			k.mark, k.index = mark.InexactFloat64(), index.InexactFloat64()
		}
	}
	if k.tradeReadAt.IsZero() || now.Sub(k.tradeReadAt) >= perpTradeEvery {
		k.tradeReadAt = now
		if _, at, err := s.prices.LastTrade(ctx, k.symbol); err != nil {
			s.failed("perp_last_trade", k.symbol)
		} else if !at.IsZero() {
			k.tradeAt = at
		}
	}
	center := p // before the contract's first mark price
	if k.mark > 0 {
		center = k.mark
		if k.index > 0 {
			center = (k.mark + k.index) / 2
		}
	}
	s.quotePerp(ctx, k, now, center)
	if s.runs(domain.EventPause) {
		return
	}
	if s.takePerp(ctx, k, now, center, dt) == 0 && s.quietPerp(k, now) {
		s.takeQuietPerp(ctx, k, now, center)
	}
}

// lineOf is the product line of a perpetual (design 2026-10-07, product
// switches): the coin-margined contracts' or the USDT-margined ones'. Its
// bots stop while an operator has it closed.
func lineOf(contract domain.Pair) string {
	if contract.Inverse() {
		return flags.KeyProductCoinM
	}
	return flags.KeyProductUSDTM
}

// quietPerp reports whether the perpetual did not trade for perpQuietTake
// (since the bots started on it, when that is later) and no quiet order
// went out since; never while its takers are switched off
// (perp_daily_volume 0).
func (s *Sim) quietPerp(k *perpetual, now time.Time) bool {
	since := k.tradeAt
	if k.watchFrom.After(since) {
		since = k.watchFrom
	}
	if k.quietAt.After(since) {
		since = k.quietAt
	}
	return s.params.PerpDailyVolume > 0 && !since.IsZero() && now.Sub(since) >= perpQuietTake
}

// takeQuietPerp has a taker not waiting after a refusal trade the
// contract's minimum (its minimum notional, rounded up to the lot) at the
// market, reducing a position at the cap; stamped once placed, so a
// throttled or refused one is tried again next round.
func (s *Sim) takeQuietPerp(ctx context.Context, k *perpetual, now time.Time, p float64) {
	takers := s.botsOf(domain.RoleTaker)
	if len(takers) == 0 || p <= 0 {
		return
	}
	rng := s.model.Rand()
	b := takers[rng.IntN(len(takers))]
	pb := k.botOf(b)
	if !pb.ready(now) {
		return
	}
	price := decimal.NewFromFloat(p)
	side, reduce := domain.TakerSide(rng, s.params, 0), false
	if s.overCap(k, pb.position, price) {
		side, reduce = domain.Sell, true
		if pb.position.IsNegative() {
			side = domain.Buy
		}
	}
	qty := domain.Quantity(k.pair.MinNotional.InexactFloat64(), price, k.pair)
	if reduce {
		qty = decimal.Min(qty, pb.position.Abs())
	}
	if !qty.IsPositive() {
		return
	}
	if !s.orders.Take(now) {
		s.m.throttled.WithLabelValues("order").Inc()
		return
	}
	err := s.Derivatives.MarketContract(ctx, b.UserID, k.symbol, side, qty, reduce)
	s.placedPerp(ctx, k, now, b, pb, err, true)
	if err != nil {
		return
	}
	k.quietAt = now
	k.quietDebt = math.Min(k.quietDebt+k.pair.Notional(qty, price).InexactFloat64(), s.params.PerpDailyVolume/24)
	s.m.perpQuiet.WithLabelValues(k.symbol).Inc()
	if side == domain.Buy {
		pb.position = pb.position.Add(qty)
	} else {
		pb.position = pb.position.Sub(qty)
	}
}

// coinMarginShare is the most of a bot's coin (spot and FUTURES) its
// margin on a coin-margined perpetual takes: at a very low price
// perp_margin's worth would be more coin than the bot keeps for its spot
// quotes, or has (review EU ③).
var coinMarginShare = decimal.NewFromFloat(0.25)

// coinMarginTarget is perp_margin's worth in the coin at price p, at most
// coinMarginShare of what the bot holds (coin in SPOT and fut in FUTURES),
// in 4 places; capped reports the limit took.
func coinMarginTarget(perpMargin, p float64, coin, fut decimal.Decimal) (target decimal.Decimal, capped bool) {
	target = decimal.NewFromFloat(perpMargin / p).Round(4)
	if most := coin.Add(fut).Mul(coinMarginShare).Round(4); most.LessThan(target) {
		return most, true
	}
	return target, false
}

// checkPerp reads the bots' positions and FUTURES balances in the
// contract's settlement asset and tops up a margin below half of
// PerpMargin worth from the bot's spot holdings of that asset: PerpMargin
// in USDT, or its worth in the coin at the coin's price p
// (coinMarginTarget).
func (s *Sim) checkPerp(ctx context.Context, k *perpetual, now time.Time, p float64) {
	k.checkedAt = now
	asset := k.settleAsset()
	if asset != "USDT" && p <= 0 {
		return // the coin's worth unknown yet
	}
	for _, b := range s.bots {
		if !b.Enabled || (b.Role != domain.RoleMaker && b.Role != domain.RoleTaker) {
			continue
		}
		pb := k.botOf(b)
		pos, err := s.Derivatives.Position(ctx, b.UserID, k.symbol)
		if err != nil {
			s.fail(ctx, b, "position", k.symbol, err)
			continue
		}
		fut, err := s.Derivatives.Futures(ctx, b.UserID, asset)
		if err != nil {
			s.fail(ctx, b, "futures", k.symbol, err)
			continue
		}
		pb.position, pb.futures, pb.known = pos, fut, true
		target, places := decimal.NewFromFloat(s.params.PerpMargin), int32(2)
		if asset != "USDT" {
			var capped bool
			target, capped = coinMarginTarget(s.params.PerpMargin, p, b.coin, fut)
			places = 4
			if capped && !k.capWarned {
				k.capWarned = true
				s.log.WarnContext(ctx, "simulated market: the coin-margined perpetual's margin is capped at a quarter of the bots' coin",
					"contract", k.symbol, "price", p, "bot", b.Label, "target", target.String())
			}
		}
		if fut.LessThan(target.Div(decimal.NewFromInt(2))) {
			need := target.Sub(fut).Round(places)
			key := fmt.Sprintf("sim-margin-%s-%s-%d", b.UserID, asset, now.Unix()/int64(perpCheckEvery/time.Second))
			if asset == "USDT" {
				// The key the bots used before the coin-margined contract:
				// a top-up retried across the deploy stays one.
				key = fmt.Sprintf("sim-margin-%s-%d", b.UserID, now.Unix()/int64(perpCheckEvery/time.Second))
			}
			if err := s.Derivatives.ToFutures(ctx, b.UserID, asset, need, key); err != nil {
				s.fail(ctx, b, "to_futures", k.symbol, err)
				continue
			}
			pb.futures = pb.futures.Add(need)
			s.log.InfoContext(ctx, "simulated market: a bot's margin topped up", "bot", b.Label, "contract", k.symbol, "asset", asset,
				"amount", need.String())
		}
	}
}

// overCap reports whether a position is worth PerpBotCap or more at price.
func (s *Sim) overCap(k *perpetual, pos, price decimal.Decimal) bool {
	return k.pair.Notional(pos.Abs(), price).InexactFloat64() >= s.params.PerpBotCap
}

// quotePerp lets the perpetual's makers requote in turn, one a round:
// their ladder around the mark price p, within the contract's price band,
// without the side that would grow a position at the cap.
func (s *Sim) quotePerp(ctx context.Context, k *perpetual, now time.Time, p float64) {
	makers := s.botsOf(domain.RoleMaker)
	if len(makers) == 0 {
		return
	}
	tick := k.pair.Tick.InexactFloat64()
	for i := range makers {
		b := makers[(k.turn+i)%len(makers)]
		pb := k.botOf(b)
		if !pb.ready(now) {
			continue
		}
		moved := tick > 0 && math.Abs(p-pb.quotedP)/tick >= float64(s.params.RequoteTick)
		if !moved && now.Before(pb.nextQuote) {
			continue
		}
		s.requotePerp(ctx, k, now, b, pb, p)
		break
	}
	k.turn = (k.turn + 1) % len(makers)
}

func (s *Sim) requotePerp(ctx context.Context, k *perpetual, now time.Time, b *bot, pb *perpBot, p float64) {
	rng := s.model.Rand()
	pb.nextQuote = now.Add(time.Second + time.Duration(rng.Int64N(int64(2*time.Second))))
	open, err := s.Derivatives.OpenContract(ctx, b.UserID, k.symbol)
	if err != nil {
		s.fail(ctx, b, "open_contract", k.symbol, err)
		return
	}
	bids, asks := domain.LadderPrices(p, b.phase, s.params, k.pair)
	if k.mark > 0 {
		mark := domain.Anchors{Lo: k.mark, Hi: k.mark}
		bids, asks = domain.InBand(bids, mark, k.pair.Band), domain.InBand(asks, mark, k.pair.Band)
	}
	if s.overCap(k, pb.position, decimal.NewFromFloat(p)) {
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
			s.fail(ctx, b, "cancel_contract", k.symbol, err)
			continue
		}
		s.m.cancels.WithLabelValues("PERP_"+string(b.Role), k.symbol).Inc()
	}
	stop, unfunded := false, map[domain.Side]bool{}
	place := func(side domain.Side, price decimal.Decimal) {
		if stop || unfunded[side] {
			return
		}
		if !s.orders.TakeLeaving(now, s.params.OrdersPerSecond*makerReserve) {
			s.m.throttled.WithLabelValues("order").Inc()
			stop = true
			return
		}
		qty := domain.Quantity(domain.Worth(rng, s.params.LevelSize, 0.5), price, k.pair)
		_, err := s.Derivatives.LimitContract(ctx, b.UserID, k.symbol, side, price, qty)
		switch {
		case err == nil:
		case isFunds(err):
			unfunded[side] = true // the side waits; the bot does not
		default:
			stop = true
		}
		s.placedPerp(ctx, k, now, b, pb, err, !isFunds(err))
	}
	for i := 0; i < max(len(placeBids), len(placeAsks)); i++ {
		if i < len(placeBids) {
			place(domain.Buy, placeBids[i])
		}
		if i < len(placeAsks) {
			place(domain.Sell, placeAsks[i])
		}
	}
	pb.quotedP = p
}

// takePerp sends the takers' market orders on the perpetual that arrived
// in dt, less what the quiet orders traded ahead of the budget; a taker at
// its cap only reduces, one waiting after a refusal sits the order out. It
// returns how many arrived.
func (s *Sim) takePerp(ctx context.Context, k *perpetual, now time.Time, p float64, dt time.Duration) int {
	takers := s.botsOf(domain.RoleTaker)
	if len(takers) == 0 || s.params.PerpDailyVolume <= 0 {
		return 0
	}
	rng := s.model.Rand()
	price := decimal.NewFromFloat(p)
	n := domain.Arrivals(rng, s.params.PerpDailyVolume, s.params.OrderSize, dt, now)
	for range n {
		b := takers[rng.IntN(len(takers))]
		pb := k.botOf(b)
		if !pb.ready(now) {
			continue
		}
		side, reduce := domain.TakerSide(rng, s.params, 0), false
		if s.overCap(k, pb.position, price) {
			side, reduce = domain.Sell, true
			if pb.position.IsNegative() {
				side = domain.Buy
			}
		}
		worth, owed := domain.Repay(domain.Worth(rng, s.params.OrderSize, domain.OrderSpread), k.quietDebt,
			k.pair.MinNotional.InexactFloat64())
		if worth == 0 {
			k.quietDebt = owed // the order went to the quiet ones
			continue
		}
		qty := domain.Quantity(worth, price, k.pair)
		if reduce {
			qty = decimal.Min(qty, pb.position.Abs())
		}
		if !qty.IsPositive() {
			continue
		}
		if !s.orders.Take(now) {
			s.m.throttled.WithLabelValues("order").Inc()
			return n
		}
		err := s.Derivatives.MarketContract(ctx, b.UserID, k.symbol, side, qty, reduce)
		s.placedPerp(ctx, k, now, b, pb, err, true)
		if err == nil {
			k.quietDebt = owed
			if side == domain.Buy {
				pb.position = pb.position.Add(qty)
			} else {
				pb.position = pb.position.Sub(qty)
			}
		}
	}
	return n
}

// placedPerp counts an order's result on perpetual k as placed does for
// the pair, the waits being the bot's on that perpetual.
func (s *Sim) placedPerp(ctx context.Context, k *perpetual, now time.Time, b *bot, pb *perpBot, err error, backOff bool) {
	role, result := "PERP_"+string(b.Role), "placed"
	switch {
	case err == nil:
		pb.wait, pb.retryAt = 0, time.Time{}
	case isFunds(err):
		result = "unfunded"
	case errors.Is(err, ports.ErrOutOfBand):
		result = "out_of_band"
	default:
		result = "failed"
		s.fail(ctx, b, "contract_order", k.symbol, err)
	}
	s.m.orders.WithLabelValues(role, result, k.symbol).Inc()
	if err == nil {
		return
	}
	if ports.Refused(err) {
		s.orders.Return()
	}
	switch {
	case !backOff:
	case errors.Is(err, ports.ErrOutOfBand):
		pb.wait = domain.BandBackoff(pb.wait)
		pb.retryAt = now.Add(pb.wait)
	default:
		pb.wait = domain.Backoff(pb.wait)
		pb.retryAt = now.Add(pb.wait)
	}
}

// stopPerp cancels the makers' orders on the perpetual.
func (s *Sim) stopPerp(ctx context.Context, k *perpetual) {
	for _, b := range s.botsOf(domain.RoleMaker) {
		if err := s.Derivatives.CancelAllContract(ctx, b.UserID, k.symbol); err != nil {
			s.fail(ctx, b, "cancel_all_contract", k.symbol, err)
		}
		pb := k.botOf(b)
		pb.quotedP, pb.nextQuote = 0, time.Time{}
	}
	k.running = false
	s.log.InfoContext(ctx, "simulated market: the bots left the perpetual", "symbol", k.symbol)
}
