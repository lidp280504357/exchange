// Package domain holds margin trading's rules (design 2026-10-06): the
// accounts, the side effects of orders on them, the interest models, an
// account's terms and its margin level. Users borrow from HOUSE; the
// ledger records what they owe (batch E1).
package domain

import (
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// AccountType is a margin account's kind: one cross account per user, its
// assets and debts covering each other, or an isolated account per pair
// with only that pair's two assets.
type AccountType string

// The account types.
const (
	AccountCross    AccountType = "MARGIN_CROSS"
	AccountIsolated AccountType = "MARGIN_ISOLATED"
)

// SideEffect is what an order on a margin account does besides trading:
// AUTO_BORROW borrows what the free balance lacks, AUTO_REPAY repays the
// debt with what its fills bring.
type SideEffect string

// The side effects.
const (
	SideEffectNone       SideEffect = "NONE"
	SideEffectAutoBorrow SideEffect = "AUTO_BORROW"
	SideEffectAutoRepay  SideEffect = "AUTO_REPAY"
)

// InterestModel sets an asset's hourly rate: FIXED by the operators,
// FLOATING from the use of the asset's pool on the hour (design §4.1).
type InterestModel string

// The interest models.
const (
	InterestFixed    InterestModel = "FIXED"
	InterestFloating InterestModel = "FLOATING"
)

// Terms are an account's leverage, its warning and liquidation levels
// and the liquidation fee (a share of the value traded).
type Terms struct {
	Leverage         int
	WarnLevel        decimal.Decimal
	LiquidationLevel decimal.Decimal
	LiquidationFee   decimal.Decimal
}

// DefaultTerms are design §4.4's: the cross account at 3x warns at 1.30
// and liquidates at 1.10; an isolated account by its pair's leverage
// (3x: 1.25 and 1.15, 5x: 1.20 and 1.10, 10x: 1.15 and 1.05). The fee is
// 2% everywhere. Operators change them in the console (two people).
func DefaultTerms(t AccountType, leverage int) Terms {
	fee := decimal.RequireFromString("0.02")
	terms := func(warn, liquidate string) Terms {
		return Terms{Leverage: leverage, WarnLevel: decimal.RequireFromString(warn), LiquidationLevel: decimal.RequireFromString(liquidate), LiquidationFee: fee}
	}
	if t == AccountCross {
		return terms("1.30", "1.10")
	}
	switch {
	case leverage >= 10:
		return terms("1.15", "1.05")
	case leverage >= 5:
		return terms("1.20", "1.10")
	default:
		return terms("1.25", "1.15")
	}
}

// Level is an account's margin level, its total assets over its total
// liabilities (both in USDT, assets after their haircut); ok is false
// without liabilities, where the level is unbounded (shown as 999).
func Level(assets, liabilities decimal.Decimal) (level decimal.Decimal, ok bool) {
	if !liabilities.IsPositive() {
		return decimal.Zero, false
	}
	return assets.DivRound(liabilities, 8), true
}

// Errors of the margin endpoints and of orders on margin accounts
// (design §5.2).
var (
	ErrDisabled      = apperr.New(apperr.KindForbidden, "MARGIN_DISABLED", "margin trading is not open")
	ErrNotBorrowable = apperr.New(apperr.KindUnprocessable, "MARGIN_ASSET_NOT_BORROWABLE", "the asset cannot be borrowed or used as margin in this account")
	ErrLimit         = apperr.New(apperr.KindUnprocessable, "MARGIN_LIMIT", "more than the account may borrow")
	ErrPoolEmpty     = apperr.New(apperr.KindUnprocessable, "MARGIN_POOL_EMPTY", "the platform's pool of the asset is used up")
	ErrLevelTooLow   = apperr.New(apperr.KindUnprocessable, "MARGIN_LEVEL_TOO_LOW", "the margin level would fall under the warning level")
	ErrFrozen        = apperr.New(apperr.KindConflict, "MARGIN_FROZEN", "the margin account is being liquidated or is frozen")
)
