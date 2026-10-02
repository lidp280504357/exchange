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
// (LevelCap), in whole lots, the best n.
func Levels(ref []Level, bids bool, spec Spec, levelCap decimal.Decimal, n int) []Level {
	var out []Level
	for _, l := range ref {
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
	kept := out[:0]
	for _, l := range out {
		q := l.Quantity
		if levelCap.IsPositive() {
			q = decimal.Min(q, levelCap.Div(l.Price))
		}
		if q = floor(q, spec.LotSize); q.IsPositive() {
			kept = append(kept, Level{Price: l.Price, Quantity: q})
		}
	}
	return kept
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
//     them cannot together spend more than it holds (review M1, ADR-0015);
//   - the base asset: HOUSE's holding of it may be worth at most Symbol
//     either way (every pair of that base shares it);
//   - in total: everything but USDT together may be worth at most Total.
//
// Both are in whole lots; zero without a price for the base or the quote.
func SpotRooms(spec Spec, h Holdings, prices map[string]decimal.Decimal, backed func(string) bool, shares func(string) int,
	caps Caps,
) (buy, sell decimal.Decimal) {
	share := func(asset string) decimal.Decimal { return decimal.NewFromInt(int64(max(shares(asset), 1))) }
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
		sell = decimal.Min(sell, positive(bal.Sub(caps.Safety.Div(p))).Div(share(spec.Base)))
	}
	buy = decimal.Min(positive(caps.Symbol.Div(p).Sub(bal)), positive(bal.Neg()).Add(totalRoom))
	if backed(spec.Quote) {
		buy = decimal.Min(buy, positive(h[spec.Quote].Mul(qp).Sub(caps.Safety)).Div(p).Div(share(spec.Quote)))
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

// ContractAccount is HOUSE's FUTURES account as its rooms need it: its net
// position on each contract (long positive), what its positions are worth
// together at the mark prices, and its equity (wallet balance and
// unrealized PnL).
type ContractAccount struct {
	Positions map[string]decimal.Decimal
	Exposure  decimal.Decimal
	Equity    decimal.Decimal
}

// ContractRoom is how much, in USDT, HOUSE's contract positions together
// may still grow: up to ContractLeverage times its equity. HOUSE is never
// liquidated (ADR-0015), so this is what keeps its losses within what it
// can pay; with no equity left it only reduces positions.
func ContractRoom(a ContractAccount, caps Caps) decimal.Decimal {
	return positive(a.Equity.Mul(caps.ContractLeverage).Sub(a.Exposure))
}

// ContractRooms works out how much of a contract HOUSE may still buy and
// sell: its net position (long positive) may be worth at most Contract
// either way, and what grows it beyond zero takes from room, the USDT
// that all its contract positions may still grow by (ContractRoom).
func ContractRooms(spec Spec, position, price, room decimal.Decimal, caps Caps) (buy, sell decimal.Decimal) {
	if !price.IsPositive() {
		return decimal.Zero, decimal.Zero
	}
	limit, grow := caps.Contract.Div(price), room.Div(price)
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
