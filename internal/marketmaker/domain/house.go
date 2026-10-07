// Package domain works out HOUSE's virtual liquidity (ADR-0013,
// ADR-0015): the levels HOUSE offers, taken from the reference market's
// book, and how much it may still buy and sell of a pair or contract.
package domain

import (
	"github.com/shopspring/decimal"
)

// Level is a price and a quantity.
type Level struct {
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

// Spec is a pair or contract as the liquidity needs it.
type Spec struct {
	Symbol   string
	Base     string
	Quote    string
	TickSize decimal.Decimal
	LotSize  decimal.Decimal
	Contract bool
	// Settle is a contract's settlement asset: USDT for a linear one, the
	// base of a coin-margined one, whose quantities are whole contracts of
	// ContractSize USD (coin-margined design 2026-10-06 §2).
	Settle       string
	ContractSize decimal.Decimal
	// Halted: the pair is not TRADING; HOUSE does not quote it, but its
	// reference market still prices its base asset.
	Halted bool
}

// SettleAsset is a contract's settlement asset, USDT when not told.
func (s Spec) SettleAsset() string {
	if s.Settle == "" {
		return Valuation
	}
	return s.Settle
}

// UnitValue is what one unit of the spec's quantity is worth in USDT at
// price: the price itself, or a coin-margined contract's face value.
func (s Spec) UnitValue(price decimal.Decimal) decimal.Decimal {
	if s.ContractSize.IsPositive() {
		return s.ContractSize
	}
	return price
}

// Valuation is the asset HOUSE's limits and prices are in.
const Valuation = "USDT"

// Caps are HOUSE's limits, in USDT: what one level offers at most, what a
// pair's or contract's position may be worth, what all spot positions
// together may be worth, and the inventory of backed assets kept back
// (ADR-0013); and how many times its contract equity all its contract
// positions together may be worth.
type Caps struct {
	Level            decimal.Decimal
	Symbol           decimal.Decimal
	Total            decimal.Decimal
	Contract         decimal.Decimal
	Safety           decimal.Decimal
	ContractLeverage decimal.Decimal
}

// Levels turns one side of the reference market's book (best first, in
// the platform's units) into what HOUSE offers there: prices on the
// symbol's tick grid, rounded away from the other side (bids down, asks
// up) so HOUSE never gives more than the reference market, levels that
// meet on the grid merged, each worth at most levelCap in the quote asset
// (LevelCap), in whole lots. The best n are offered as they are; the rest
// of the book (as deep as market-data sends it) in at most deep levels
// more, each merging twice as many of the reference market's levels as
// the one before (2, 4, 8, ...; the last all that is left) at the worst
// price among them: a market order larger than the best n levels walks on
// as it would on the reference market, a little worse within a merged
// level, rather than stopping (review FI, C46: the best 20 levels of
// BTCUSDT held 2.6 BTC at times, and a 5 BTC market order filled half).
// The merged levels reach DeepWithin of the best price at most (review FT,
// C50): a thin book's 200th level can be far off, and a merged level's
// worst price would hand it to the whole level.
func Levels(ref []Level, bids bool, spec Spec, levelCap decimal.Decimal, n, deep int) []Level {
	var out []Level
	i := 0
	for ; i < len(ref); i++ {
		l := ref[i]
		p := onGrid(l.Price, spec.TickSize, !bids)
		if !p.IsPositive() || !l.Quantity.IsPositive() {
			continue
		}
		if k := len(out); k > 0 && out[k-1].Price.Equal(p) {
			out[k-1].Quantity = out[k-1].Quantity.Add(l.Quantity)
			continue
		}
		if len(out) == n {
			break
		}
		out = append(out, Level{Price: p, Quantity: l.Quantity})
	}
	rest := ref[i:]
	if len(ref) > 0 {
		reach := ref[0].Price.Mul(decimal.NewFromInt(1).Add(DeepWithin))
		if bids {
			reach = ref[0].Price.Mul(decimal.NewFromInt(1).Sub(DeepWithin))
		}
		within := 0
		for within < len(rest) && (bids && !rest[within].Price.LessThan(reach) || !bids && !rest[within].Price.GreaterThan(reach)) {
			within++
		}
		rest = rest[:within]
	}
	for size := 2; len(rest) > 0 && deep > 0; size, deep = size*2, deep-1 {
		take := len(rest)
		if deep > 1 {
			take = min(size, len(rest))
		}
		q := decimal.Zero
		for _, l := range rest[:take] {
			if l.Quantity.IsPositive() {
				q = q.Add(l.Quantity)
			}
		}
		p := onGrid(rest[take-1].Price, spec.TickSize, !bids)
		rest = rest[take:]
		switch k := len(out); {
		case !p.IsPositive() || !q.IsPositive():
		case k > 0 && out[k-1].Price.Equal(p): // a coarser grid than the reference market's
			out[k-1].Quantity = out[k-1].Quantity.Add(q)
		default:
			out = append(out, Level{Price: p, Quantity: q})
		}
	}
	kept := out[:0]
	for _, l := range out {
		q := l.Quantity
		if levelCap.IsPositive() {
			q = decimal.Min(q, levelCap.Div(spec.UnitValue(l.Price)))
		}
		if q = floor(q, spec.LotSize); q.IsPositive() {
			kept = append(kept, Level{Price: l.Price, Quantity: q})
		}
	}
	return kept
}

// DeepWithin is how far from the best reference price the merged levels
// reach: 1%.
var DeepWithin = decimal.RequireFromString("0.01")

// Fraction is levels with each quantity times part, in whole lots, the
// levels left empty dropped: what HOUSE offers while a price event is on
// the book (design 2026-10-07, general price control).
func Fraction(levels []Level, part decimal.Decimal, spec Spec) []Level {
	if !part.IsPositive() || part.GreaterThanOrEqual(decimal.NewFromInt(1)) {
		return levels
	}
	out := make([]Level, 0, len(levels))
	for _, l := range levels {
		if q := floor(l.Quantity.Mul(part), spec.LotSize); q.IsPositive() {
			out = append(out, Level{Price: l.Price, Quantity: q})
		}
	}
	return out
}

// Holdings is HOUSE's spot inventory: the available balance of each
// asset's MARKET_MAKER account (below zero for an internal asset HOUSE
// sold).
type Holdings map[string]decimal.Decimal

// SpotRooms works out how much of a pair's base asset HOUSE may still buy
// and sell (ADR-0013), given its holdings and the USDT value of one unit
// of each asset (prices, USDT itself 1). Each limit applies to the
// direction that grows what it limits:
//
//   - inventory: a backed asset keeps Safety's worth back (selling the
//     base; buying spends the quote, which on ETH-BTC is BTC), and what is
//     above it is shared by the books that spend it (shares: how many
//     HOUSE offers on): between two reads of its holdings, fills on all of
//     them cannot together spend more than it holds (review M1, ADR-0015).
//     Each book gets at least a lot of what is above it, so a thin share
//     (a little USDT among 87 books) does not empty every side quietly;
//     what the lots add beyond the share is within the safety;
//   - the base asset: HOUSE's holding of it may be worth at most Symbol
//     either way (every pair of that base shares it);
//   - in total: everything but USDT together may be worth at most Total.
//
// Both are in whole lots; zero without a price for the base or the quote.
func SpotRooms(spec Spec, h Holdings, prices map[string]decimal.Decimal, backed func(string) bool, shares func(string) int,
	caps Caps,
) (buy, sell decimal.Decimal) {
	share := func(asset string, room decimal.Decimal) decimal.Decimal {
		n := decimal.NewFromInt(int64(max(shares(asset), 1)))
		return decimal.Max(room.Div(n), decimal.Min(room, spec.LotSize))
	}
	p, qp := prices[spec.Base], prices[spec.Quote]
	if spec.Quote == Valuation {
		qp = decimal.NewFromInt(1)
	}
	if !p.IsPositive() || !qp.IsPositive() {
		return decimal.Zero, decimal.Zero
	}
	bal := h[spec.Base]
	exposure := decimal.Zero
	for asset, amount := range h {
		if asset != Valuation && prices[asset].IsPositive() {
			exposure = exposure.Add(amount.Abs().Mul(prices[asset]))
		}
	}
	totalRoom := positive(caps.Total.Sub(exposure)).Div(p)

	sell = decimal.Min(positive(bal.Add(caps.Symbol.Div(p))), positive(bal).Add(totalRoom))
	if backed(spec.Base) {
		sell = decimal.Min(sell, share(spec.Base, positive(bal.Sub(caps.Safety.Div(p)))))
	}
	buy = decimal.Min(positive(caps.Symbol.Div(p).Sub(bal)), positive(bal.Neg()).Add(totalRoom))
	if backed(spec.Quote) {
		buy = decimal.Min(buy, share(spec.Quote, positive(h[spec.Quote].Mul(qp).Sub(caps.Safety)).Div(p)))
	}
	return floor(buy, spec.LotSize), floor(sell, spec.LotSize)
}

// LevelCap is Caps.Level in a symbol's quote asset, for Levels: as it is
// for contracts and USDT pairs, converted at the quote's USDT price
// otherwise (20,000 USDT is about 0.24 BTC on ETH-BTC); false without that
// price.
func LevelCap(spec Spec, caps Caps, prices map[string]decimal.Decimal) (decimal.Decimal, bool) {
	if spec.Contract || spec.Quote == Valuation {
		return caps.Level, true
	}
	qp := prices[spec.Quote]
	if !qp.IsPositive() {
		return decimal.Zero, false
	}
	return caps.Level.Div(qp), true
}

// ContractAccount is HOUSE's FUTURES accounts as its rooms need them: its
// net position on each contract (long positive; contracts of a
// coin-margined one), and by settlement asset what its positions there
// are worth together at the mark prices, in USDT, and that account's
// equity (wallet balance and unrealized PnL), in the asset.
type ContractAccount struct {
	Positions map[string]decimal.Decimal
	Exposure  map[string]decimal.Decimal
	Equity    map[string]decimal.Decimal
}

// ContractRoom is how much, in USDT, HOUSE's contract positions settled
// in asset may still grow: up to ContractLeverage times that account's
// equity, valued at price (the asset's USDT price, 1 for USDT; zero room
// without one). HOUSE is never liquidated (ADR-0015), so this is what keeps
// its losses within what each account can pay (coin-margined design
// 2026-10-06 §2.3: a coin-margined contract's against the coin's
// account); with no equity left it only reduces positions.
func ContractRoom(a ContractAccount, asset string, price decimal.Decimal, caps Caps) decimal.Decimal {
	if !price.IsPositive() {
		return decimal.Zero
	}
	return positive(a.Equity[asset].Mul(price).Mul(caps.ContractLeverage).Sub(a.Exposure[asset]))
}

// ContractRooms works out how much of a contract HOUSE may still buy and
// sell: its net position (long positive) may be worth at most Contract
// either way, and what grows it beyond zero takes from room, the USDT
// that its contract positions of that settlement asset may still grow by
// (ContractRoom). A coin-margined contract's are in contracts, valued at
// their face value.
func ContractRooms(spec Spec, position, price, room decimal.Decimal, caps Caps) (buy, sell decimal.Decimal) {
	if !price.IsPositive() {
		return decimal.Zero, decimal.Zero
	}
	unit := spec.UnitValue(price)
	limit, grow := caps.Contract.Div(unit), room.Div(unit)
	buy = decimal.Min(positive(limit.Sub(position)), positive(position.Neg()).Add(grow))
	sell = decimal.Min(positive(limit.Add(position)), positive(position).Add(grow))
	return floor(buy, spec.LotSize), floor(sell, spec.LotSize)
}

func positive(d decimal.Decimal) decimal.Decimal { return decimal.Max(d, decimal.Zero) }

// floor rounds q down to whole steps.
func floor(q, step decimal.Decimal) decimal.Decimal {
	if !step.IsPositive() {
		return q
	}
	return q.Div(step).Floor().Mul(step)
}

// onGrid puts a price on the tick grid, up or down.
func onGrid(p, tick decimal.Decimal, up bool) decimal.Decimal {
	if !tick.IsPositive() {
		return p
	}
	steps := p.Div(tick)
	if up {
		return steps.Ceil().Mul(tick)
	}
	return steps.Floor().Mul(tick)
}
