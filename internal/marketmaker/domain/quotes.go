// Package domain holds the platform market maker's quoting rules
// (requirements §11.10): levels on both sides around the reference price,
// sized per level, one side stopped when the inventory leans too far or
// reaches its ceiling.
package domain

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Sides.
const (
	Buy  = "BUY"
	Sell = "SELL"
)

// Params are one pair's quoting parameters.
type Params struct {
	Symbol string `json:"symbol"`
	// Spread is the gap between the best bid and the best ask as a fraction
	// of the reference: 0.002 quotes 0.1% below and above it.
	Spread decimal.Decimal `json:"spread"`
	// Step is the distance between levels as a fraction of the reference.
	Step decimal.Decimal `json:"step"`
	// Levels is the number of orders per side.
	Levels int `json:"levels"`
	// Quantity is each order's size in the base asset.
	Quantity decimal.Decimal `json:"quantity"`
	// MaxBase is the inventory ceiling: holding this much base, no bids.
	MaxBase decimal.Decimal `json:"max_base"`
	// MaxSkew is the largest share of the inventory's value one asset may
	// have: above it the side that adds more of it stops.
	MaxSkew decimal.Decimal `json:"max_skew"`
	// Requote is how far, as a fraction, the reference must move before
	// the quotes follow; smaller moves keep them.
	Requote decimal.Decimal `json:"requote"`
}

// Defaults are §11.10's: a 0.2% spread, five levels 0.1% apart, and
// quotes that follow a 0.05% move.
func Defaults(symbol string) Params {
	return Params{
		Symbol: symbol, Spread: decimal.RequireFromString("0.002"), Step: decimal.RequireFromString("0.001"), Levels: 5,
		Quantity: decimal.RequireFromString("0.002"), MaxBase: decimal.RequireFromString("5"),
		MaxSkew: decimal.RequireFromString("0.8"), Requote: decimal.RequireFromString("0.0005"),
	}
}

// Validate checks the parameters.
func (p Params) Validate() error {
	one := decimal.NewFromInt(1)
	switch {
	case p.Symbol == "":
		return errors.New("market maker: a symbol is required")
	case !p.Spread.IsPositive() || p.Spread.GreaterThanOrEqual(decimal.RequireFromString("0.2")):
		return fmt.Errorf("market maker %s: spread must be in (0, 0.2)", p.Symbol)
	case p.Step.IsNegative() || p.Levels < 1 || p.Levels > 50:
		return fmt.Errorf("market maker %s: step must not be negative and levels in 1..50", p.Symbol)
	case !p.Quantity.IsPositive() || !p.MaxBase.IsPositive():
		return fmt.Errorf("market maker %s: quantity and max_base must be positive", p.Symbol)
	case p.MaxSkew.LessThanOrEqual(decimal.RequireFromString("0.5")) || p.MaxSkew.GreaterThan(one):
		return fmt.Errorf("market maker %s: max_skew must be in (0.5, 1]", p.Symbol)
	case p.Requote.IsNegative():
		return fmt.Errorf("market maker %s: requote must not be negative", p.Symbol)
	}
	return nil
}

// Pair is what quoting needs of a trading pair.
type Pair struct {
	TickSize decimal.Decimal
	LotSize  decimal.Decimal
}

// Quote is one order to keep on the book.
type Quote struct {
	Side     string
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

// Key identifies a quote by side and price.
func (q Quote) Key() string { return q.Side + "@" + q.Price.String() }

// Plan returns the quotes for a reference price and the inventory held
// (available plus frozen): bids best first, then asks best first. Prices
// round away from the reference to the tick, sizes down to the lot.
func Plan(p Params, pair Pair, ref, base, quote decimal.Decimal) []Quote {
	if !ref.IsPositive() {
		return nil
	}
	qty := p.Quantity.Div(pair.LotSize).Floor().Mul(pair.LotSize)
	if !qty.IsPositive() {
		return nil
	}
	value := base.Mul(ref)
	total := value.Add(quote)
	bids, asks := true, true
	if total.IsPositive() {
		share := value.Div(total)
		bids = share.LessThan(p.MaxSkew)
		asks = share.GreaterThan(decimal.NewFromInt(1).Sub(p.MaxSkew))
	}
	if base.GreaterThanOrEqual(p.MaxBase) {
		bids = false
	}
	one, half := decimal.NewFromInt(1), p.Spread.Div(decimal.NewFromInt(2))
	var out []Quote
	for _, side := range []string{Buy, Sell} {
		if side == Buy && !bids || side == Sell && !asks {
			continue
		}
		for i := range p.Levels {
			off := half.Add(p.Step.Mul(decimal.NewFromInt(int64(i))))
			var price decimal.Decimal
			if side == Buy {
				price = ref.Mul(one.Sub(off)).Div(pair.TickSize).Floor().Mul(pair.TickSize)
			} else {
				price = ref.Mul(one.Add(off)).Div(pair.TickSize).Ceil().Mul(pair.TickSize)
			}
			if price.IsPositive() {
				out = append(out, Quote{Side: side, Price: price, Quantity: qty})
			}
		}
	}
	return out
}

// Moved reports whether the reference moved from last by at least the
// requote fraction (always when there was none).
func Moved(p Params, last, ref decimal.Decimal) bool {
	if !last.IsPositive() {
		return true
	}
	return ref.Sub(last).Abs().Div(last).GreaterThanOrEqual(p.Requote)
}
