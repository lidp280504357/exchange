package domain

import "github.com/shopspring/decimal"

// The price band must not lock the market (ASTRA design §4, the user's
// decision 2026-10-02). The trading service refuses a limit price more
// than the pair's band from its anchor, the pair's last trade. A ladder
// around a target beyond the band would be refused level by level, the
// book would empty and, with no trade, the anchor would never move. So the
// makers quote around the target pulled into the band (QuoteCenter):
// while the target is beyond it, the quotes stand near the band's edge on
// the target's side, the executors trade there, the anchor follows the
// trades and the quotes walk on, step by step, until the target is inside.

// BandReach is how far into the band the quotes' center may go, as a
// share of the band: the rest leaves room for the ladder's depth and for
// an anchor that moved since it was read.
const BandReach = 0.7

// Anchors is the band's anchor as recently read: the lowest and the
// highest of the last few seconds' reads (the trading service caches its
// own a second), 0 when there is none.
type Anchors struct{ Lo, Hi float64 }

// QuoteCenter is where the makers center their ladders: the target, pulled
// within BandReach of the band (a share; 0: none) around the anchors.
// walking is true when it was pulled.
func QuoteCenter(target float64, a Anchors, band float64) (center float64, walking bool) {
	if target <= 0 || band <= 0 || a.Lo <= 0 || a.Hi <= 0 {
		return target, false
	}
	lo, hi := a.Hi*(1-band*BandReach), a.Lo*(1+band*BandReach)
	if lo > hi {
		// The reads are farther apart than the band: the middle of them.
		return (a.Lo + a.Hi) / 2, true
	}
	switch {
	case target < lo:
		return lo, true
	case target > hi:
		return hi, true
	}
	return target, false
}

// bandSlack keeps the filter on the safe side of float rounding.
const bandSlack = 1e-9

// InBand keeps the prices the trading service accepts whichever of the
// anchors it has: within band (a share; 0: none) of both.
func InBand(prices []decimal.Decimal, a Anchors, band float64) []decimal.Decimal {
	if band <= 0 || a.Lo <= 0 || a.Hi <= 0 {
		return prices
	}
	lo, hi := a.Hi*(1-band)*(1+bandSlack), a.Lo*(1+band)*(1-bandSlack)
	out := make([]decimal.Decimal, 0, len(prices))
	for _, p := range prices {
		if f := p.InexactFloat64(); f >= lo && f <= hi {
			out = append(out, p)
		}
	}
	return out
}
