package domain

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// MaxLeverage bounds the leverage operators may set.
const MaxLeverage = 10

// Validate checks an account's terms: a leverage of 2 to MaxLeverage, a
// liquidation level above 1 and under the warning level, a fee from 0 to
// 10%.
func (t Terms) Validate() error {
	one := decimal.NewFromInt(1)
	switch {
	case t.Leverage < 2 || t.Leverage > MaxLeverage:
		return apperr.Invalid(fmt.Sprintf("the leverage is 2 to %d", MaxLeverage))
	case !t.LiquidationLevel.GreaterThan(one):
		return apperr.Invalid("the liquidation level must be above 1")
	case !t.WarnLevel.GreaterThan(t.LiquidationLevel):
		return apperr.Invalid("the warning level must be above the liquidation level")
	case t.LiquidationFee.IsNegative() || t.LiquidationFee.GreaterThan(decimal.RequireFromString("0.1")):
		return apperr.Invalid("the liquidation fee is 0 to 0.1 of the value traded")
	}
	return nil
}

// Pair is a pair's isolated margin terms (design §4.2).
type Pair struct {
	Symbol string
	Base   string
	Quote  string
	// Isolated: the pair takes isolated accounts.
	Isolated bool
	Terms    Terms
}

// Has reports whether asset is one of the pair's two.
func (p Pair) Has(asset string) bool { return asset == p.Base || asset == p.Quote }

// MayHold reports whether an account may hold or borrow asset: an
// isolated account only its pair's two, the cross account any asset of
// the margin list that counts as collateral or can be borrowed.
func MayHold(a Account, p *Pair, t AssetTerms, listed bool) bool {
	if !listed || (!t.Collateral && !t.Borrowable) {
		return false
	}
	if a.IsCross() {
		return true
	}
	return p != nil && p.Isolated && p.Symbol == a.Symbol && p.Has(t.Asset)
}
