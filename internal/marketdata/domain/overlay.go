package domain

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Price overlays (design 2026-10-07, general price control): a price event
// multiplies a followed pair's reference prices by a factor, the result on
// the pair's tick (the reference market sends 84221.69000000: its own
// precision would put the scaled price off the tick).

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

// places is how many decimal places p has, its trailing zeros dropped.
func places(p decimal.Decimal) int32 {
	s := p.String()
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return int32(len(s) - i - 1) //nolint:gosec // a decimal string's length
	}
	return 0
}

// The ways a scaled price goes onto the tick.
type rounding int

const (
	roundHalf rounding = iota // a trade's, a last price
	roundDown                 // a bid
	roundUp                   // an ask
)

// onTick is x on the tick (when known; else at like's precision) the way
// r says.
func onTick(x, tick, like decimal.Decimal, r rounding) decimal.Decimal {
	if tick.IsPositive() {
		n := x.Div(tick)
		switch r {
		case roundDown:
			n = n.Floor()
		case roundUp:
			n = n.Ceil()
		default:
			n = n.Round(0)
		}
		return n.Mul(tick)
	}
	switch places := places(like); r {
	case roundDown:
		return x.RoundFloor(places)
	case roundUp:
		return x.RoundCeil(places)
	default:
		return x.Round(places)
	}
}

// ScalePrice is p times f on the tick (zero: at p's precision), rounded
// half away from zero (a trade's price).
func ScalePrice(p, f, tick decimal.Decimal) decimal.Decimal {
	if f.Equal(factorOne) {
		return p
	}
	return onTick(p.Mul(f), tick, p, roundHalf)
}

// MergeOverlaid lays the 1m candles a price event touched over candles of
// interval i (oldest first, as the reference market has them; review GD
// ③): a touched minute widens its candle's high and low, and gives it its
// open when it is the candle's first minute and its close when its last.
// The volumes stay the reference market's.
func MergeOverlaid(candles []Candle, i Interval, touched []Candle) []Candle {
	if len(touched) == 0 {
		return candles
	}
	k := 0
	for n := range candles {
		c := &candles[n]
		end := i.Next(c.OpenTime)
		for k < len(touched) && touched[k].OpenTime.Before(c.OpenTime) {
			k++
		}
		for j := k; j < len(touched) && touched[j].OpenTime.Before(end); j++ {
			m := touched[j]
			c.High, c.Low = decimal.Max(c.High, m.High), decimal.Min(c.Low, m.Low)
			if m.OpenTime.Equal(c.OpenTime) {
				c.Open = m.Open
			}
			if Minute1.Next(m.OpenTime).Equal(end) {
				c.Close = m.Close
			}
		}
	}
	return candles
}

// ScaleLevels is one side of a book (best first) with its prices times f
// on the tick (zero: at their precision), rounded away from the other side
// (bids down, asks up: never better than the scaled reference market),
// the levels that meet merged, quantities as they are.
func ScaleLevels(levels []Level, f decimal.Decimal, bids bool, tick decimal.Decimal) []Level {
	if f.Equal(factorOne) || len(levels) == 0 {
		return levels
	}
	way := roundUp
	if bids {
		way = roundDown
	}
	out := make([]Level, 0, len(levels))
	for _, l := range levels {
		p := onTick(l.Price.Mul(f), tick, l.Price, way)
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
