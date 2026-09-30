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

// Caps are HOUSE's limits, in the quote asset (USDT): what one level
// offers at most, what a pair's or contract's position may be worth, what
// all spot positions together may be worth, and the inventory of backed
// assets kept back (ADR-0013).
type Caps struct {
	Level    decimal.Decimal
	Symbol   decimal.Decimal
	Total    decimal.Decimal
	Contract decimal.Decimal
	Safety   decimal.Decimal
}

// Levels turns one side of the reference market's book (best first, in
// the platform's units) into what HOUSE offers there: prices on the
// symbol's tick grid, rounded away from the other side (bids down, asks
// up) so HOUSE never gives more than the reference market, levels that
// meet on the grid merged, each worth at most levelCap, in whole lots,
// the best n.
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
// of each asset (prices). Each limit applies to the direction that grows
// what it limits:
//
//   - inventory: a backed asset keeps Safety's worth back (selling the
//     base; buying spends the quote);
//   - the pair: the base position may be worth at most Symbol either way;
//   - in total: all positions together may be worth at most Total.
//
// Both are in whole lots; zero without a price.
func SpotRooms(spec Spec, h Holdings, prices map[string]decimal.Decimal, backed func(string) bool, caps Caps) (buy, sell decimal.Decimal) {
	p := prices[spec.Base]
	if !p.IsPositive() {
		return decimal.Zero, decimal.Zero
	}
	bal := h[spec.Base]
	exposure := decimal.Zero
	for asset, amount := range h {
		if asset != spec.Quote && prices[asset].IsPositive() {
			exposure = exposure.Add(amount.Abs().Mul(prices[asset]))
		}
	}
	totalRoom := positive(caps.Total.Sub(exposure)).Div(p)

	sell = decimal.Min(positive(bal.Add(caps.Symbol.Div(p))), positive(bal).Add(totalRoom))
	if backed(spec.Base) {
		sell = decimal.Min(sell, positive(bal.Sub(caps.Safety.Div(p))))
	}
	buy = decimal.Min(positive(caps.Symbol.Div(p).Sub(bal)), positive(bal.Neg()).Add(totalRoom))
	if backed(spec.Quote) {
		buy = decimal.Min(buy, positive(h[spec.Quote].Sub(caps.Safety)).Div(p))
	}
	return floor(buy, spec.LotSize), floor(sell, spec.LotSize)
}

// ContractRooms works out how much of a contract HOUSE may still buy and
// sell: its net position (long positive) may be worth at most Contract
// either way. Its margin is its own business: HOUSE is never liquidated
// (ADR-0015).
func ContractRooms(spec Spec, position, price decimal.Decimal, caps Caps) (buy, sell decimal.Decimal) {
	if !price.IsPositive() {
		return decimal.Zero, decimal.Zero
	}
	limit := caps.Contract.Div(price)
	return floor(positive(limit.Sub(position)), spec.LotSize), floor(positive(limit.Add(position)), spec.LotSize)
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
