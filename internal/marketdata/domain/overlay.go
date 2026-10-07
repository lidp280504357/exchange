package domain

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Price overlays (design 2026-10-07, general price control): a price event
// multiplies a followed pair's reference prices by a factor. A price keeps
// the precision the reference market sent it with.

var factorOne = decimal.NewFromInt(1)

// IndexPairOf is the spot pair a perpetual's index follows (instrument's
// check): BTC-USDT for BTC-USDT-PERP, BTC-USDT for the coin-margined
// BTC-USD-PERP; a pair is its own.
func IndexPairOf(symbol string) string {
	pair, ok := strings.CutSuffix(symbol, "-PERP")
	if !ok {
		return symbol
	}
	if base, ok := strings.CutSuffix(pair, "-USD"); ok {
		return base + "-USDT"
	}
	return pair
}

// places is how many decimal places p was sent with.
func places(p decimal.Decimal) int32 {
	if e := p.Exponent(); e < 0 {
		return -e
	}
	return 0
}

// ScalePrice is p times f at p's precision, rounded half away from zero (a
// trade's price).
func ScalePrice(p, f decimal.Decimal) decimal.Decimal {
	if f.Equal(factorOne) {
		return p
	}
	return p.Mul(f).Round(places(p))
}

// ScaleLevels is one side of a book (best first) with its prices times f
// at their precision, rounded away from the other side (bids down, asks
// up: never better than the scaled reference market), the levels that
// meet merged, quantities as they are.
func ScaleLevels(levels []Level, f decimal.Decimal, bids bool) []Level {
	if f.Equal(factorOne) || len(levels) == 0 {
		return levels
	}
	out := make([]Level, 0, len(levels))
	for _, l := range levels {
		p := l.Price.Mul(f)
		if bids {
			p = p.RoundFloor(places(l.Price))
		} else {
			p = p.RoundCeil(places(l.Price))
		}
		if !p.IsPositive() {
			continue
		}
		if k := len(out); k > 0 && out[k-1].Price.Equal(p) {
			out[k-1].Quantity = out[k-1].Quantity.Add(l.Quantity)
			continue
		}
		out = append(out, Level{Price: p, Quantity: l.Quantity})
	}
	return out
}
