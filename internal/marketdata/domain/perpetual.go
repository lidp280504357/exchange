package domain

import (
	"errors"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

// Perpetual contract prices (requirements §11.7): the index is a weighted
// median of spot prices from independent sources; the mark price follows
// the contract's own book around the index; the funding rate comes from
// the premium of the book's impact prices over the index.
const (
	// PriceDecimals is the precision of index, mark and impact prices.
	PriceDecimals = 8
	// RateDecimals is the precision of funding rates.
	RateDecimals = 8
	// BasisSeconds is the span of the basis EMA, sampled every second.
	BasisSeconds = 30
	// ratioDecimals bounds the precision of basis and premium ratios; the
	// EMA's would otherwise grow with every sample.
	ratioDecimals = 12
)

var (
	// MaxIndexDeviation drops a source more than 3% away from the median.
	MaxIndexDeviation = decimal.RequireFromString("0.03")
	// MaxBasis bounds the mark price to index x (1 +- 1%).
	MaxBasis = decimal.RequireFromString("0.01")
	// InterestBound bounds (interest - premium) in the funding rate.
	InterestBound = decimal.RequireFromString("0.0005")

	one        = decimal.NewFromInt(1)
	two        = decimal.NewFromInt(2)
	basisAlpha = two.Div(decimal.NewFromInt(BasisSeconds + 1))
)

// ErrTooFewSources means fewer index sources are usable than required.
var ErrTooFewSources = errors.New("too few index sources")

// SourcePrice is one source's latest spot price for an index.
type SourcePrice struct {
	Source string
	Price  decimal.Decimal
	Weight int32
}

// IndexComponent is a source and whether the index used it.
type IndexComponent struct {
	SourcePrice
	Included bool
}

// Index returns the weighted median of prices after dropping those more
// than MaxIndexDeviation away from the weighted median of all of them,
// rounded to PriceDecimals, with the part each source played. A source
// without a positive price and weight is left out. ErrTooFewSources when
// fewer than minSources (at least 1) remain.
func Index(prices []SourcePrice, minSources int) (decimal.Decimal, []IndexComponent, error) {
	minSources = max(minSources, 1)
	comps := make([]IndexComponent, len(prices))
	var usable []SourcePrice
	for i, p := range prices {
		comps[i] = IndexComponent{SourcePrice: p}
		if p.Price.IsPositive() && p.Weight > 0 {
			usable = append(usable, p)
		}
	}
	if len(usable) < minSources {
		return decimal.Zero, comps, ErrTooFewSources
	}
	median := weightedMedian(usable)
	limit := median.Mul(MaxIndexDeviation)
	var kept []SourcePrice
	for _, p := range usable {
		if p.Price.Sub(median).Abs().LessThanOrEqual(limit) {
			kept = append(kept, p)
		}
	}
	if len(kept) < minSources {
		return decimal.Zero, comps, ErrTooFewSources
	}
	for i := range comps {
		comps[i].Included = slices.ContainsFunc(kept, func(p SourcePrice) bool { return p.Source == comps[i].Source })
	}
	return weightedMedian(kept).Round(PriceDecimals), comps, nil
}

// weightedMedian is the price at which half the weight lies on either
// side; when the halves split exactly between two prices, their average,
// so equal weights give the ordinary median.
func weightedMedian(prices []SourcePrice) decimal.Decimal {
	sorted := slices.Clone(prices)
	slices.SortStableFunc(sorted, func(a, b SourcePrice) int { return a.Price.Cmp(b.Price) })
	var total int64
	for _, p := range sorted {
		total += int64(p.Weight)
	}
	var cum int64
	for i, p := range sorted {
		cum += int64(p.Weight)
		switch {
		case 2*cum == total && i+1 < len(sorted):
			return p.Price.Add(sorted[i+1].Price).Div(two)
		case 2*cum >= total:
			return p.Price
		}
	}
	return sorted[len(sorted)-1].Price
}

// Basis is the EMA over BasisSeconds one-second samples of the contract's
// mid price relative to its index. It starts at 0: the mark price starts
// at the index.
type Basis struct {
	EMA decimal.Decimal
}

// Add takes one sample.
func (b *Basis) Add(sample decimal.Decimal) {
	b.EMA = b.EMA.Add(basisAlpha.Mul(sample.Sub(b.EMA))).Round(ratioDecimals)
}

// BasisSample is (mid - index) / index for a book with both sides, and 0
// for a book without: an empty book pulls the mark price to the index.
func BasisSample(index, bid, ask decimal.Decimal) decimal.Decimal {
	if !index.IsPositive() || !bid.IsPositive() || !ask.IsPositive() {
		return decimal.Zero
	}
	mid := bid.Add(ask).Div(two)
	return mid.Sub(index).DivRound(index, ratioDecimals)
}

// Mark is index x (1 + basis), the basis bounded to +-MaxBasis, rounded to
// PriceDecimals.
func Mark(index, basis decimal.Decimal) decimal.Decimal {
	b := decimal.Min(decimal.Max(basis, MaxBasis.Neg()), MaxBasis)
	return index.Mul(one.Add(b)).Round(PriceDecimals)
}

// Level is a price and the quantity resting at it.
type Level struct {
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

// ImpactPrice is the average price of trading notional (in the quote
// asset) against levels, best first; false when they hold less.
func ImpactPrice(levels []Level, notional decimal.Decimal) (decimal.Decimal, bool) {
	if !notional.IsPositive() {
		return decimal.Zero, false
	}
	left, qty := notional, decimal.Zero
	for _, l := range levels {
		if !l.Price.IsPositive() || !l.Quantity.IsPositive() {
			continue
		}
		value := l.Price.Mul(l.Quantity)
		if value.GreaterThanOrEqual(left) {
			qty = qty.Add(left.Div(l.Price))
			return notional.DivRound(qty, PriceDecimals), true
		}
		qty, left = qty.Add(l.Quantity), left.Sub(value)
	}
	return decimal.Zero, false
}

// Premium is (max(0, impact bid - index) - max(0, index - impact ask)) /
// index; a side too thin for the impact notional (ok false) adds nothing.
func Premium(index, impactBid, impactAsk decimal.Decimal, bidOK, askOK bool) decimal.Decimal {
	if !index.IsPositive() {
		return decimal.Zero
	}
	p := decimal.Zero
	if bidOK && impactBid.GreaterThan(index) {
		p = p.Add(impactBid.Sub(index))
	}
	if askOK && impactAsk.LessThan(index) {
		p = p.Sub(index.Sub(impactAsk))
	}
	return p.DivRound(index, ratioDecimals)
}

// AveragePremium is the mean of a period's samples, 0 without any.
func AveragePremium(sum decimal.Decimal, samples int64) decimal.Decimal {
	if samples <= 0 {
		return decimal.Zero
	}
	return sum.DivRound(decimal.NewFromInt(samples), ratioDecimals)
}

// FundingRate is clamp(premium + clamp(interest - premium, +-0.05%),
// +-limit), rounded to RateDecimals (§11.7), premium being the period's
// average premium index and limit the contract's funding cap. A positive
// rate means longs pay shorts.
func FundingRate(premium, interest, limit decimal.Decimal) decimal.Decimal {
	r := premium.Add(decimal.Min(decimal.Max(interest.Sub(premium), InterestBound.Neg()), InterestBound))
	return decimal.Min(decimal.Max(r, limit.Neg()), limit).Round(RateDecimals)
}

// NextFunding returns the end of the funding period that contains t, for
// periods of hours aligned to 00:00 UTC: a time on a boundary starts the
// next period.
func NextFunding(t time.Time, hours int32) time.Time {
	d := time.Duration(max(hours, 1)) * time.Hour
	return t.UTC().Truncate(d).Add(d)
}
