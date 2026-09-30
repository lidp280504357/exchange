package domain

import (
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Trading pair statuses (appendix B).
const (
	StatusPrepare    = "PREPARE"
	StatusTrading    = "TRADING"
	StatusHalt       = "HALT"
	StatusCancelOnly = "CANCEL_ONLY"
	StatusDelisted   = "DELISTED"
)

// pairTransitions: PREPARE -> TRADING <-> HALT; TRADING/HALT -> CANCEL_ONLY
// -> DELISTED.
var pairTransitions = map[string][]string{
	StatusPrepare:    {StatusTrading},
	StatusTrading:    {StatusHalt, StatusCancelOnly},
	StatusHalt:       {StatusTrading, StatusCancelOnly},
	StatusCancelOnly: {StatusDelisted},
}

// CheckPairTransition reports whether a pair may move between statuses.
func CheckPairTransition(from, to string) error {
	if slices.Contains(pairTransitions[from], to) {
		return nil
	}
	return ErrStatusTransition.WithDetail("from", from).WithDetail("to", to)
}

// ValidPairStatus reports whether s is a known status.
func ValidPairStatus(s string) bool {
	switch s {
	case StatusPrepare, StatusTrading, StatusHalt, StatusCancelOnly, StatusDelisted:
		return true
	}
	return false
}

var symbolRE = regexp.MustCompile(`^([A-Z0-9]{2,10})-([A-Z0-9]{2,10})$`)

// TradingPair is a spot market (§5.5, §11.2).
type TradingPair struct {
	Symbol      string          `json:"symbol"`
	BaseAsset   string          `json:"base_asset"`
	QuoteAsset  string          `json:"quote_asset"`
	TickSize    decimal.Decimal `json:"tick_size"`
	LotSize     decimal.Decimal `json:"lot_size"`
	MinQuantity decimal.Decimal `json:"min_quantity"`
	MaxQuantity decimal.Decimal `json:"max_quantity"`
	MinNotional decimal.Decimal `json:"min_notional"`
	PriceBand   decimal.Decimal `json:"price_band"`
	FeeTier     string          `json:"fee_tier"`
	Status      string          `json:"status"`
	// ReferenceSymbol is the reference market shown for the pair
	// (ADR-0010), e.g. BTCUSDT; empty: the pair shows its own data.
	// Platform prices are reference prices x ReferenceMultiplier (1 unless
	// a low-priced coin trades per 1000, ADR-0014).
	ReferenceSymbol     string          `json:"reference_symbol,omitempty"`
	ReferenceMultiplier decimal.Decimal `json:"reference_multiplier"`
	// ListedAt is when the pair was listed; set on creation unless given.
	ListedAt time.Time `json:"listed_at,omitzero"`
	Version  int64     `json:"version,omitempty"`
}

var referenceRE = regexp.MustCompile(`^[A-Z0-9]{2,20}$`)

// ValidMultiplier reports whether m is a power of ten from 1 to 10^9, so
// that converting prices and quantities stays exact.
func ValidMultiplier(m decimal.Decimal) bool {
	for p, ten := decimal.NewFromInt(1), decimal.NewFromInt(10); p.LessThanOrEqual(decimal.NewFromInt(1_000_000_000)); p = p.Mul(ten) {
		if m.Equal(p) {
			return true
		}
	}
	return false
}

// Validate checks the pair against its assets: steps fit the assets'
// precision, quantities are multiples of the lot size, and the price band
// is a fraction.
func (p TradingPair) Validate(base, quote Asset) error {
	m := symbolRE.FindStringSubmatch(p.Symbol)
	switch {
	case m == nil:
		return apperr.Invalid(fmt.Sprintf("symbol %q: use BASE-QUOTE", p.Symbol))
	case m[1] != p.BaseAsset || m[2] != p.QuoteAsset || base.Code != p.BaseAsset || quote.Code != p.QuoteAsset:
		return apperr.Invalid(fmt.Sprintf("pair %s: the symbol must name its base and quote assets", p.Symbol))
	case p.BaseAsset == p.QuoteAsset:
		return apperr.Invalid(fmt.Sprintf("pair %s: base and quote must differ", p.Symbol))
	case !base.TradingEnabled || !quote.TradingEnabled:
		return apperr.Invalid(fmt.Sprintf("pair %s: both assets must be enabled for trading", p.Symbol))
	case !p.TickSize.IsPositive() || !FitsScale(p.TickSize, quote.Decimals):
		return apperr.Invalid(fmt.Sprintf("pair %s: tick_size must be positive with at most %d decimals", p.Symbol, quote.Decimals))
	case !p.LotSize.IsPositive() || !FitsScale(p.LotSize, base.Decimals):
		return apperr.Invalid(fmt.Sprintf("pair %s: lot_size must be positive with at most %d decimals", p.Symbol, base.Decimals))
	case !FitsScale(p.TickSize.Mul(p.LotSize), quote.Decimals):
		// Then every price x quantity is exact in the quote asset: freezes,
		// trade values and what is left to release need no rounding.
		return apperr.Invalid(fmt.Sprintf("pair %s: tick_size x lot_size (%s) must have at most %d decimals",
			p.Symbol, p.TickSize.Mul(p.LotSize), quote.Decimals))
	case !IsMultipleOf(p.MinQuantity, p.LotSize) || !p.MinQuantity.IsPositive():
		return apperr.Invalid(fmt.Sprintf("pair %s: min_quantity must be a positive multiple of lot_size", p.Symbol))
	case !IsMultipleOf(p.MaxQuantity, p.LotSize) || !p.MaxQuantity.GreaterThan(p.MinQuantity):
		return apperr.Invalid(fmt.Sprintf("pair %s: max_quantity must be a multiple of lot_size above min_quantity", p.Symbol))
	case p.MinNotional.IsNegative() || !FitsScale(p.MinNotional, quote.Decimals):
		return apperr.Invalid(fmt.Sprintf("pair %s: min_notional must be at least 0 with at most %d decimals", p.Symbol, quote.Decimals))
	case !p.PriceBand.IsPositive() || p.PriceBand.GreaterThan(decimal.NewFromInt(1)):
		return apperr.Invalid(fmt.Sprintf("pair %s: price_band must be above 0 and at most 1", p.Symbol))
	case !tierRE.MatchString(p.FeeTier):
		return apperr.Invalid(fmt.Sprintf("pair %s: unknown fee tier %q", p.Symbol, p.FeeTier))
	case !ValidPairStatus(p.Status):
		return apperr.Invalid(fmt.Sprintf("pair %s: unknown status %q", p.Symbol, p.Status))
	case p.ReferenceSymbol != "" && !referenceRE.MatchString(p.ReferenceSymbol):
		return apperr.Invalid(fmt.Sprintf("pair %s: reference_symbol %q: use 2-20 upper-case letters or digits", p.Symbol, p.ReferenceSymbol))
	case !p.ReferenceMultiplier.IsZero() && !ValidMultiplier(p.ReferenceMultiplier): // zero stands for 1
		return apperr.Invalid(fmt.Sprintf("pair %s: reference_multiplier must be a power of ten from 1 to 1000000000", p.Symbol))
	}
	return nil
}

// SameConfig reports whether the static configuration equals other,
// ignoring the status (changed only through status transitions) and the
// version.
func (p TradingPair) SameConfig(other TradingPair) bool {
	return p.Symbol == other.Symbol && p.BaseAsset == other.BaseAsset && p.QuoteAsset == other.QuoteAsset &&
		p.TickSize.Equal(other.TickSize) && p.LotSize.Equal(other.LotSize) &&
		p.MinQuantity.Equal(other.MinQuantity) && p.MaxQuantity.Equal(other.MaxQuantity) &&
		p.MinNotional.Equal(other.MinNotional) && p.PriceBand.Equal(other.PriceBand) && p.FeeTier == other.FeeTier &&
		p.ReferenceSymbol == other.ReferenceSymbol && p.ReferenceMultiplier.Equal(other.ReferenceMultiplier) &&
		p.ListedAt.Equal(other.ListedAt)
}

// ValidReason checks the free-text reason of a change.
func ValidReason(reason string) error {
	if !reasonRE.MatchString(reason) {
		return apperr.Invalid("a reason of 3 to 200 characters is required")
	}
	return nil
}
