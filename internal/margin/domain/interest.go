package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// RateDecimals is the precision of hourly rates: a floating rate is
// rounded to it, and so recorded with the interest it charged.
const RateDecimals = 12

// FloatingRate is the floating model's curve (design §4.1): from Base at
// an idle pool it rises linearly to KinkRate at the Kink use, then
// steeply to MaxRate at a pool lent out entirely.
type FloatingRate struct {
	Base     decimal.Decimal
	Kink     decimal.Decimal
	KinkRate decimal.Decimal
	MaxRate  decimal.Decimal
}

// DefaultFloating is design §4.1's curve: 0.0005% an hour idle, 0.0030%
// at 80% use, 0.0100% at 100%.
func DefaultFloating() FloatingRate {
	return FloatingRate{
		Base: decimal.RequireFromString("0.000005"), Kink: decimal.RequireFromString("0.8"),
		KinkRate: decimal.RequireFromString("0.00003"), MaxRate: decimal.RequireFromString("0.0001"),
	}
}

// Validate checks the curve: rates not below zero and rising, the kink
// strictly between 0 and 1.
func (f FloatingRate) Validate() error {
	switch {
	case f.Base.IsNegative():
		return apperr.Invalid("the floating base rate must not be negative")
	case !f.Kink.IsPositive() || !f.Kink.LessThan(decimal.NewFromInt(1)):
		return apperr.Invalid("the floating kink is a use strictly between 0 and 1")
	case f.KinkRate.LessThan(f.Base) || f.MaxRate.LessThan(f.KinkRate):
		return apperr.Invalid("the floating rates must rise: base <= kink rate <= max rate")
	}
	return nil
}

// Rate is the hourly rate at a pool use between 0 and 1 (clamped),
// rounded to RateDecimals.
func (f FloatingRate) Rate(use decimal.Decimal) decimal.Decimal {
	one := decimal.NewFromInt(1)
	use = decimal.Min(decimal.Max(use, decimal.Zero), one)
	var r decimal.Decimal
	if use.LessThanOrEqual(f.Kink) {
		r = f.Base.Add(f.KinkRate.Sub(f.Base).Mul(use).DivRound(f.Kink, RateDecimals+4))
	} else {
		r = f.KinkRate.Add(f.MaxRate.Sub(f.KinkRate).Mul(use.Sub(f.Kink)).DivRound(one.Sub(f.Kink), RateDecimals+4))
	}
	return r.Round(RateDecimals)
}

// Use is the share of a pool lent out, between 0 and 1: lent over the
// pool's cap (design §11: the cap stands in for the lenders). A pool
// without a cap counts as used up.
func Use(lent, poolCap decimal.Decimal) decimal.Decimal {
	if !poolCap.IsPositive() {
		return decimal.NewFromInt(1)
	}
	return decimal.Min(decimal.Max(lent, decimal.Zero).DivRound(poolCap, RateDecimals+4), decimal.NewFromInt(1))
}

// AssetTerms are an asset's margin terms (design §4.1).
type AssetTerms struct {
	Asset    string
	Decimals int32
	// Borrowable: margin accounts may borrow it. Collateral: it counts in
	// a margin account's total assets.
	Borrowable bool
	Collateral bool
	// Haircut is what the asset counts for as collateral, 0 < h <= 1.
	Haircut decimal.Decimal
	// PoolCap is what the platform lends of it at most, UserCap what one
	// user may owe of it (principal, all accounts together).
	PoolCap decimal.Decimal
	UserCap decimal.Decimal
	Model   InterestModel
	// FixedRate is the hourly rate of the FIXED model, Floating the curve
	// of the FLOATING one.
	FixedRate decimal.Decimal
	Floating  FloatingRate
}

// Validate checks the terms.
func (t AssetTerms) Validate() error {
	fail := func(format string, args ...any) error {
		return apperr.Invalid(fmt.Sprintf("%s: ", t.Asset) + fmt.Sprintf(format, args...))
	}
	switch {
	case t.Asset == "":
		return apperr.Invalid("the asset is required")
	case t.Decimals < 0 || t.Decimals > 18:
		return fail("decimals must be 0 to 18")
	case !t.Haircut.IsPositive() || t.Haircut.GreaterThan(decimal.NewFromInt(1)):
		return fail("the haircut must be above 0 and at most 1")
	case t.PoolCap.IsNegative() || t.UserCap.IsNegative():
		return fail("the caps must not be negative")
	case t.UserCap.GreaterThan(t.PoolCap):
		return fail("the user cap must not exceed the pool cap")
	case t.Model != InterestFixed && t.Model != InterestFloating:
		return fail("the interest model is FIXED or FLOATING")
	case t.Model == InterestFixed && t.FixedRate.IsNegative():
		return fail("the fixed rate must not be negative")
	}
	if t.Model == InterestFloating {
		if err := t.Floating.Validate(); err != nil {
			return fail("%s", apperr.From(err).Message)
		}
	}
	return nil
}

// HourlyRate is the asset's rate for an hour whose pool had lent out lent
// on the hour.
func (t AssetTerms) HourlyRate(lent decimal.Decimal) decimal.Decimal {
	if t.Model == InterestFloating {
		return t.Floating.Rate(Use(lent, t.PoolCap))
	}
	return t.FixedRate
}

// Hour is the hour t falls in (UTC): interest is charged per hour.
func Hour(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }

// Interest is an hour's interest on principal: principal x rate, rounded
// up to the asset's decimals (design §4.3; interest is not compounded, so
// principal never includes interest owed). Nothing is owed on nothing.
func Interest(principal, rate decimal.Decimal, decimals int32) decimal.Decimal {
	if !principal.IsPositive() || !rate.IsPositive() {
		return decimal.Zero
	}
	return principal.Mul(rate).RoundCeil(decimals)
}

// PrincipalAt is the principal a loan had at an earlier instant, from
// what it has now and what changed it since: the amounts borrowed after
// the instant are taken off, the principal repaid after it added back.
// The hourly charge uses the principal on the hour (design §4.3): an
// amount borrowed after the hour paid its first hour when it was
// borrowed, and an amount repaid after the hour was still owed on it.
func PrincipalAt(now, borrowedSince, repaidSince decimal.Decimal) decimal.Decimal {
	return decimal.Max(now.Sub(borrowedSince).Add(repaidSince), decimal.Zero)
}
