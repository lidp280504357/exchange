// Package domain holds perpetual contract trading (requirements §5.8,
// §11.7): a user's settings per contract, orders and what they reserve,
// positions booked at their entry cost, and what each fill does to them
// and to the user's FUTURES account in the ledger. Amounts are decimals in
// the settlement asset (USDT), quantities in the base asset (ADR-0008).
package domain

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Contract statuses that matter here (the pair state machine).
const (
	StatusTrading = "TRADING"
)

// RiskTier is a step of the risk limit ladder (§11.7).
type RiskTier struct {
	MaxNotional decimal.Decimal
	MaxLeverage int32
	MMR         decimal.Decimal
}

// Contract is what trading needs of a perpetual contract, its fee tier
// and its settlement asset.
type Contract struct {
	Symbol      string
	Base, Quote string
	TickSize    decimal.Decimal
	LotSize     decimal.Decimal
	MinQuantity decimal.Decimal
	MaxQuantity decimal.Decimal
	MinNotional decimal.Decimal
	// PriceBand bounds limit prices around the mark price and sets the
	// protection price of market orders.
	PriceBand            decimal.Decimal
	Tiers                []RiskTier
	FundingIntervalHours int32
	MakerFeeRate         decimal.Decimal
	TakerFeeRate         decimal.Decimal
	Status               string
	// QuoteDecimals is the precision of the settlement asset;
	// BaseDecimals that of the base asset (the engine rounds fees with it,
	// which are 0 for contracts).
	QuoteDecimals int32
	BaseDecimals  int32
	// Followed is true when the contract's index pair follows a reference
	// market, whose book HOUSE quotes (ADR-0015); the platform coin's
	// perpetual is not: its users and bots trade with each other.
	Followed bool
}

// ValidTiers checks a ladder as instrument-service does: 1 to 20 tiers,
// notional caps rising, leverage (1 to 125) not rising, maintenance
// margin rates not falling and below 1 / leverage.
func ValidTiers(tiers []RiskTier) error {
	if len(tiers) == 0 || len(tiers) > 20 {
		return apperr.Invalid("1 to 20 risk tiers are required")
	}
	one := decimal.NewFromInt(1)
	for i, t := range tiers {
		if !t.MaxNotional.IsPositive() || t.MaxLeverage < 1 || t.MaxLeverage > 125 || !t.MMR.IsPositive() ||
			!t.MMR.LessThan(one.Div(decimal.NewFromInt32(t.MaxLeverage))) {
			return apperr.Invalid(fmt.Sprintf("tier %d: max_notional above 0, max_leverage 1 to 125, mmr above 0 and below 1/max_leverage", i+1))
		}
		if i > 0 && (!t.MaxNotional.GreaterThan(tiers[i-1].MaxNotional) || t.MaxLeverage > tiers[i-1].MaxLeverage || t.MMR.LessThan(tiers[i-1].MMR)) {
			return apperr.Invalid(fmt.Sprintf("tier %d: max_notional must rise, max_leverage and mmr must not move the other way", i+1))
		}
	}
	return nil
}

// MaxLeverage is the leverage of the first tier.
func (c Contract) MaxLeverage() int32 {
	if len(c.Tiers) == 0 {
		return 1
	}
	return c.Tiers[0].MaxLeverage
}

// MaxNotional is the largest position notional allowed at leverage: the
// cap of the last tier whose leverage is at least it; zero when no tier
// allows it.
func (c Contract) MaxNotional(leverage int32) decimal.Decimal {
	out := decimal.Zero
	for _, t := range c.Tiers {
		if t.MaxLeverage >= leverage {
			out = t.MaxNotional
		}
	}
	return out
}

// MMR is the maintenance margin rate of a position of notional: its
// tier's, the last tier's beyond the ladder.
func (c Contract) MMR(notional decimal.Decimal) decimal.Decimal {
	if len(c.Tiers) == 0 {
		return decimal.Zero
	}
	return c.Tiers[c.tierOf(notional)].MMR
}

// tierOf is the index of the tier of notional, the last beyond the ladder.
func (c Contract) tierOf(notional decimal.Decimal) int {
	for i, t := range c.Tiers {
		if notional.LessThanOrEqual(t.MaxNotional) {
			return i
		}
	}
	return max(len(c.Tiers)-1, 0)
}

// maintenanceAmount is what tier i takes off notional x its rate: each
// step up the ladder charged only on the notional above the step, as
// Binance's cumulative maintenance amount does.
func (c Contract) maintenanceAmount(i int) decimal.Decimal {
	cum := decimal.Zero
	for j := 1; j <= i && j < len(c.Tiers); j++ {
		cum = cum.Add(c.Tiers[j-1].MaxNotional.Mul(c.Tiers[j].MMR.Sub(c.Tiers[j-1].MMR)))
	}
	return cum
}

// Maintenance is the maintenance margin of a position of notional
// (§11.7): notional x its tier's rate less the tier's maintenance amount.
// It rises continuously across a tier boundary instead of jumping, so a
// position does not go from healthy to liquidated when its notional
// crosses into the next tier.
func (c Contract) Maintenance(notional decimal.Decimal) decimal.Decimal {
	if len(c.Tiers) == 0 {
		return decimal.Zero
	}
	i := c.tierOf(notional)
	return notional.Mul(c.Tiers[i].MMR).Sub(c.maintenanceAmount(i))
}

// Errors of contract trading (appendix C).
var (
	ErrDisabled           = apperr.New(apperr.KindForbidden, "DERIV_DISABLED", "contract trading is not open")
	ErrNotTrading         = apperr.New(apperr.KindUnprocessable, "INSTRUMENT_NOT_TRADING", "the contract does not accept new orders")
	ErrReduceOnlyMode     = apperr.New(apperr.KindUnprocessable, "DERIV_REDUCE_ONLY_MODE", "the contract only accepts orders that reduce a position")
	ErrMarkUnavailable    = apperr.New(apperr.KindUnavailable, "DERIV_MARK_PRICE_UNAVAILABLE", "the contract has no current mark price")
	ErrInsufficientMargin = apperr.New(apperr.KindUnprocessable, "DERIV_INSUFFICIENT_MARGIN", "not enough margin")
	ErrLeverageExceeded   = apperr.New(apperr.KindInvalid, "DERIV_LEVERAGE_EXCEEDED", "the leverage is above what the contract allows")
	ErrRiskLimitExceeded  = apperr.New(apperr.KindUnprocessable, "DERIV_RISK_LIMIT_EXCEEDED",
		"the position would exceed the risk limit of its leverage")
	ErrReduceOnlyRejected = apperr.New(apperr.KindUnprocessable, "DERIV_REDUCE_ONLY_REJECTED",
		"a reduce-only order must not exceed the position it reduces")
	ErrSettingsLocked = apperr.New(apperr.KindConflict, "DERIV_SETTINGS_LOCKED",
		"close the contract's positions and cancel its orders first")
	ErrNoPosition  = apperr.New(apperr.KindUnprocessable, "DERIV_NO_POSITION", "there is no such position")
	ErrLiquidating = apperr.New(apperr.KindConflict, "DERIV_POSITION_LIQUIDATING",
		"the position is being liquidated")
	ErrMarginTooLarge = apperr.New(apperr.KindUnprocessable, "DERIV_MARGIN_REDUCE_TOO_LARGE",
		"the position would keep less than its initial margin")
	ErrClosePending = apperr.New(apperr.KindConflict, "DERIV_CLOSE_PENDING",
		"the position's closing orders are being canceled; close it again in a moment")
)

// ceil rounds up to decimals, floor down.
func ceil(d decimal.Decimal, decimals int32) decimal.Decimal  { return d.RoundCeil(decimals) }
func floor(d decimal.Decimal, decimals int32) decimal.Decimal { return d.RoundFloor(decimals) }

func floorTo(v, step decimal.Decimal) decimal.Decimal { return v.Div(step).Floor().Mul(step) }
func ceilTo(v, step decimal.Decimal) decimal.Decimal  { return v.Div(step).Ceil().Mul(step) }
