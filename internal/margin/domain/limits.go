package domain

import (
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Limit names the bound that decides how much may be borrowed.
type Limit string

// The bounds of max-borrowable (contract limited_by): the account's
// leverage, the margin level that must stay at or above the warning level
// after the loan, the pool's remaining amount, the user's cap.
const (
	LimitLeverage Limit = "LEVERAGE"
	LimitLevel    Limit = "LEVEL"
	LimitPool     Limit = "POOL"
	LimitUserCap  Limit = "USER_CAP"
)

// workDecimals is the precision of the intermediate quotients below; the
// results are rounded down to the asset's decimals.
const workDecimals = 18

// BorrowRoom is what MaxBorrow needs to know.
type BorrowRoom struct {
	// Valuation is the account now; Terms its leverage and levels.
	Valuation Valuation
	Terms     Terms
	// Asset is the asset to borrow and Price its value in USDT.
	Asset AssetTerms
	Price decimal.Decimal
	// PoolLeft is the pool's cap less what it has lent; UserOwed the
	// principal the user owes of the asset in all accounts.
	PoolLeft decimal.Decimal
	UserOwed decimal.Decimal
}

// MaxBorrow is how much of an asset an account may borrow now (design
// §2), rounded down to the asset's decimals, and the bound that decides
// it. The leverage bound keeps the liabilities within the net assets x
// (leverage - 1) after the loan, the borrowed amount counting as
// collateral at its haircut; the level bound keeps the margin level at or
// above the warning level after it, which a loan may not go under
// (MARGIN_LEVEL_TOO_LOW): at 10x isolated (warning at 1.15) it binds
// first, the leverage's own bound leaving the level at 10/9. An asset
// that cannot be borrowed has no room.
func MaxBorrow(r BorrowRoom) (decimal.Decimal, Limit) {
	if !r.Asset.Borrowable || !r.Price.IsPositive() {
		return decimal.Zero, LimitLeverage
	}
	haircut := decimal.Zero
	if r.Asset.Collateral {
		haircut = r.Asset.Haircut
	}
	one := decimal.NewFromInt(1)
	lev := decimal.NewFromInt(int64(r.Terms.Leverage)).Sub(one)
	// D + x·p <= (N - (1 - h)·x·p)·(L - 1)  ⇔  x <= (N·(L-1) - D) / (p·(1 + (1-h)·(L-1)))
	byLeverage := r.Valuation.Net().Mul(lev).Sub(r.Valuation.TotalLiability).
		DivRound(r.Price.Mul(one.Add(one.Sub(haircut).Mul(lev))), workDecimals)
	// (A + h·x·p) / (D + x·p) >= w  ⇔  x <= (A - w·D) / (p·(w - h))
	w := r.Terms.WarnLevel
	byWarning := r.Valuation.TotalAsset.Sub(w.Mul(r.Valuation.TotalLiability)).
		DivRound(r.Price.Mul(w.Sub(haircut)), workDecimals)
	amount, limit := byLeverage, LimitLeverage
	if byWarning.LessThan(amount) {
		amount, limit = byWarning, LimitLevel
	}
	if r.PoolLeft.LessThan(amount) {
		amount, limit = r.PoolLeft, LimitPool
	}
	if userLeft := r.Asset.UserCap.Sub(r.UserOwed); userLeft.LessThan(amount) {
		amount, limit = userLeft, LimitUserCap
	}
	return decimal.Max(amount, decimal.Zero).RoundFloor(r.Asset.Decimals), limit
}

// MaxTransferOut is how much of an asset may move from the account back
// to SPOT now (design §3.2): what is free of open orders, less the part
// of the asset's own debt its holdings cover (borrowed funds stay), and,
// while the account owes anything, no more than keeps the margin level at
// or above the warning level. price is the asset's value in USDT and
// haircut what it counts for as collateral (0 if it does not count).
func MaxTransferOut(h Holding, v Valuation, terms Terms, price, haircut decimal.Decimal, decimals int32) decimal.Decimal {
	amount := decimal.Min(h.Free, h.Total().Sub(h.Debt()))
	if v.HasDebt() && haircut.IsPositive() && price.IsPositive() {
		// (A - h·x·p) / D >= w  ⇔  x <= (A - w·D) / (h·p)
		byLevel := v.TotalAsset.Sub(terms.WarnLevel.Mul(v.TotalLiability)).DivRound(haircut.Mul(price), workDecimals)
		amount = decimal.Min(amount, byLevel)
	}
	return decimal.Max(amount, decimal.Zero).RoundFloor(decimals)
}

// Repayment is how a repayment splits: interest first, then principal
// (design §4.3).
type Repayment struct {
	Interest  decimal.Decimal
	Principal decimal.Decimal
}

// Total is the whole amount repaid.
func (r Repayment) Total() decimal.Decimal { return r.Interest.Add(r.Principal) }

// ErrRepayTooMuch refuses a repayment above the debt.
var ErrRepayTooMuch = apperr.New(apperr.KindUnprocessable, "MARGIN_REPAY_EXCEEDS_DEBT", "more than the debt of the asset")

// Repay splits amount over a debt, interest first; it may not exceed the
// debt.
func Repay(amount decimal.Decimal, h Holding) (Repayment, error) {
	if !amount.IsPositive() {
		return Repayment{}, apperr.Invalid("the amount must be positive")
	}
	if amount.GreaterThan(h.Debt()) {
		return Repayment{}, ErrRepayTooMuch.WithDetail("debt", h.Debt().String())
	}
	toInterest := decimal.Min(amount, h.Interest)
	return Repayment{Interest: toInterest, Principal: amount.Sub(toInterest)}, nil
}

// RepayAll is the repayment of ALL: the whole debt of the asset, as far
// as the free balance goes.
func RepayAll(h Holding) Repayment {
	amount := decimal.Min(h.Free, h.Debt())
	if !amount.IsPositive() {
		return Repayment{Interest: decimal.Zero, Principal: decimal.Zero}
	}
	toInterest := decimal.Min(amount, h.Interest)
	return Repayment{Interest: toInterest, Principal: amount.Sub(toInterest)}
}

// LiquidationPrice estimates the base price (in the quote) at which an
// isolated account reaches its liquidation level, everything else
// unchanged: with b and q held, db and dq owed, haircuts hb and hq,
// (hb·b·p + hq·q) / (db·p + dq) = L gives p = (L·dq - hq·q) / (hb·b - L·db).
// ok is false without debts or when no positive price solves it.
func LiquidationPrice(base, quote Holding, baseHaircut, quoteHaircut, level decimal.Decimal, tickDecimals int32) (decimal.Decimal, bool) {
	if !base.Debt().IsPositive() && !quote.Debt().IsPositive() {
		return decimal.Zero, false
	}
	num := level.Mul(quote.Debt()).Sub(quoteHaircut.Mul(quote.Total()))
	den := baseHaircut.Mul(base.Total()).Sub(level.Mul(base.Debt()))
	if den.IsZero() {
		return decimal.Zero, false
	}
	p := num.DivRound(den, workDecimals)
	if !p.IsPositive() {
		return decimal.Zero, false
	}
	return p.Round(tickDecimals), true
}
