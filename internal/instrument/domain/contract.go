package domain

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Contract types.
const (
	ContractPerpetual = "PERPETUAL"
)

var contractRE = regexp.MustCompile(`^([A-Z0-9]{2,10})-([A-Z0-9]{2,10})-PERP$`)

// RiskTier is one step of a contract's risk limit ladder (requirements
// §11.7): positions up to MaxNotional may use leverage up to MaxLeverage
// and need maintenance margin at MMR, which includes the liquidation fee.
type RiskTier struct {
	MaxNotional decimal.Decimal `json:"max_notional"`
	MaxLeverage int32           `json:"max_leverage"`
	MMR         decimal.Decimal `json:"mmr"`
}

// Contract is a linear perpetual future settled in its quote asset
// (requirements §5.8, §11.7), e.g. BTC-USDT-PERP. Quantities are in the
// base asset; prices, margin, fees and PnL in the quote asset.
type Contract struct {
	Symbol     string `json:"symbol"`
	Type       string `json:"type"`
	BaseAsset  string `json:"base_asset"`
	QuoteAsset string `json:"quote_asset"`
	// IndexSymbol names the spot market whose reference prices make the
	// index, e.g. BTC-USDT.
	IndexSymbol string          `json:"index_symbol"`
	TickSize    decimal.Decimal `json:"tick_size"`
	LotSize     decimal.Decimal `json:"lot_size"`
	MinQuantity decimal.Decimal `json:"min_quantity"`
	MaxQuantity decimal.Decimal `json:"max_quantity"`
	MinNotional decimal.Decimal `json:"min_notional"`
	// PriceBand bounds a limit price around the mark price, as a fraction.
	PriceBand decimal.Decimal `json:"price_band"`
	RiskTiers []RiskTier      `json:"risk_tiers"`
	// FundingIntervalHours is 8 (00:00, 08:00, 16:00 UTC), 4 or 1.
	FundingIntervalHours int32 `json:"funding_interval_hours"`
	// InterestRate per funding interval, e.g. 0.0001; FundingCap bounds the
	// rate either way, e.g. 0.0075.
	InterestRate decimal.Decimal `json:"interest_rate"`
	FundingCap   decimal.Decimal `json:"funding_cap"`
	// ImpactNotional is the notional the premium index prices on each side
	// of the book, e.g. 10000.
	ImpactNotional decimal.Decimal `json:"impact_notional"`
	FeeTier        string          `json:"fee_tier"`
	Status         string          `json:"status"`
	Version        int64           `json:"version,omitempty"`
}

// MaxLeverage is the leverage of the first risk tier.
func (c Contract) MaxLeverage() int32 {
	if len(c.RiskTiers) == 0 {
		return 0
	}
	return c.RiskTiers[0].MaxLeverage
}

// Tier returns the risk tier a position of notional falls in, and false
// above the last tier.
func (c Contract) Tier(notional decimal.Decimal) (RiskTier, bool) {
	for _, t := range c.RiskTiers {
		if notional.LessThanOrEqual(t.MaxNotional) {
			return t, true
		}
	}
	return RiskTier{}, false
}

var one = decimal.NewFromInt(1)

// Validate checks the contract against its assets.
func (c Contract) Validate(base, quote Asset) error {
	m := contractRE.FindStringSubmatch(c.Symbol)
	switch {
	case m == nil:
		return apperr.Invalid(fmt.Sprintf("contract %q: use BASE-QUOTE-PERP", c.Symbol))
	case c.Type != ContractPerpetual:
		return apperr.Invalid(fmt.Sprintf("contract %s: type must be %s", c.Symbol, ContractPerpetual))
	case m[1] != c.BaseAsset || m[2] != c.QuoteAsset || base.Code != c.BaseAsset || quote.Code != c.QuoteAsset:
		return apperr.Invalid(fmt.Sprintf("contract %s: the symbol must name its base and quote assets", c.Symbol))
	case c.IndexSymbol != c.BaseAsset+"-"+c.QuoteAsset:
		return apperr.Invalid(fmt.Sprintf("contract %s: index_symbol must be %s-%s", c.Symbol, c.BaseAsset, c.QuoteAsset))
	case base.Hidden || quote.Hidden:
		return apperr.Invalid(fmt.Sprintf("contract %s: a hidden test asset has no contracts (ADR-0017)", c.Symbol))
	case !base.TradingEnabled || !quote.TradingEnabled:
		return apperr.Invalid(fmt.Sprintf("contract %s: both assets must be enabled for trading", c.Symbol))
	case !c.TickSize.IsPositive() || !FitsScale(c.TickSize, quote.Decimals):
		return apperr.Invalid(fmt.Sprintf("contract %s: tick_size must be positive with at most %d decimals", c.Symbol, quote.Decimals))
	case !c.LotSize.IsPositive() || !FitsScale(c.LotSize, base.Decimals):
		return apperr.Invalid(fmt.Sprintf("contract %s: lot_size must be positive with at most %d decimals", c.Symbol, base.Decimals))
	case !FitsScale(c.TickSize.Mul(c.LotSize), quote.Decimals):
		return apperr.Invalid(fmt.Sprintf("contract %s: tick_size x lot_size (%s) must have at most %d decimals",
			c.Symbol, c.TickSize.Mul(c.LotSize), quote.Decimals))
	case !IsMultipleOf(c.MinQuantity, c.LotSize) || !c.MinQuantity.IsPositive():
		return apperr.Invalid(fmt.Sprintf("contract %s: min_quantity must be a positive multiple of lot_size", c.Symbol))
	case !IsMultipleOf(c.MaxQuantity, c.LotSize) || !c.MaxQuantity.GreaterThan(c.MinQuantity):
		return apperr.Invalid(fmt.Sprintf("contract %s: max_quantity must be a multiple of lot_size above min_quantity", c.Symbol))
	case c.MinNotional.IsNegative() || !FitsScale(c.MinNotional, quote.Decimals):
		return apperr.Invalid(fmt.Sprintf("contract %s: min_notional must be at least 0 with at most %d decimals", c.Symbol, quote.Decimals))
	case !c.PriceBand.IsPositive() || c.PriceBand.GreaterThan(one):
		return apperr.Invalid(fmt.Sprintf("contract %s: price_band must be above 0 and at most 1", c.Symbol))
	case !slices.Contains([]int32{1, 4, 8}, c.FundingIntervalHours):
		return apperr.Invalid(fmt.Sprintf("contract %s: funding_interval_hours must be 1, 4 or 8", c.Symbol))
	case c.InterestRate.IsNegative() || c.InterestRate.GreaterThan(decimal.RequireFromString("0.01")):
		return apperr.Invalid(fmt.Sprintf("contract %s: interest_rate must be between 0 and 0.01", c.Symbol))
	case !c.FundingCap.IsPositive() || c.FundingCap.GreaterThan(decimal.RequireFromString("0.05")):
		return apperr.Invalid(fmt.Sprintf("contract %s: funding_cap must be above 0 and at most 0.05", c.Symbol))
	case !c.ImpactNotional.IsPositive():
		return apperr.Invalid(fmt.Sprintf("contract %s: impact_notional must be positive", c.Symbol))
	case !tierRE.MatchString(c.FeeTier):
		return apperr.Invalid(fmt.Sprintf("contract %s: unknown fee tier %q", c.Symbol, c.FeeTier))
	case !ValidPairStatus(c.Status):
		return apperr.Invalid(fmt.Sprintf("contract %s: unknown status %q", c.Symbol, c.Status))
	}
	return c.validateTiers()
}

// validateTiers checks the ladder: notional caps rise, leverage does not,
// maintenance margin rises and stays below the initial margin of its
// leverage (1 / leverage), or a position would be liquidated on opening.
func (c Contract) validateTiers() error {
	if len(c.RiskTiers) == 0 || len(c.RiskTiers) > 20 {
		return apperr.Invalid(fmt.Sprintf("contract %s: 1 to 20 risk tiers are required", c.Symbol))
	}
	for i, t := range c.RiskTiers {
		switch {
		case !t.MaxNotional.IsPositive():
			return apperr.Invalid(fmt.Sprintf("contract %s: tier %d: max_notional must be positive", c.Symbol, i+1))
		case t.MaxLeverage < 1 || t.MaxLeverage > 125:
			return apperr.Invalid(fmt.Sprintf("contract %s: tier %d: max_leverage must be 1 to 125", c.Symbol, i+1))
		case !t.MMR.IsPositive() || !t.MMR.LessThan(one.Div(decimal.NewFromInt32(t.MaxLeverage))):
			return apperr.Invalid(fmt.Sprintf("contract %s: tier %d: mmr must be above 0 and below 1/max_leverage", c.Symbol, i+1))
		}
		if i == 0 {
			continue
		}
		prev := c.RiskTiers[i-1]
		switch {
		case !t.MaxNotional.GreaterThan(prev.MaxNotional):
			return apperr.Invalid(fmt.Sprintf("contract %s: tier %d: max_notional must rise", c.Symbol, i+1))
		case t.MaxLeverage > prev.MaxLeverage:
			return apperr.Invalid(fmt.Sprintf("contract %s: tier %d: max_leverage must not rise", c.Symbol, i+1))
		case t.MMR.LessThan(prev.MMR):
			return apperr.Invalid(fmt.Sprintf("contract %s: tier %d: mmr must not fall", c.Symbol, i+1))
		}
	}
	return nil
}

// SameConfig reports whether the static configuration equals other,
// ignoring the status and the version.
func (c Contract) SameConfig(other Contract) bool {
	tiers := slices.EqualFunc(c.RiskTiers, other.RiskTiers, func(a, b RiskTier) bool {
		return a.MaxNotional.Equal(b.MaxNotional) && a.MaxLeverage == b.MaxLeverage && a.MMR.Equal(b.MMR)
	})
	return tiers && c.Symbol == other.Symbol && c.Type == other.Type && c.BaseAsset == other.BaseAsset &&
		c.QuoteAsset == other.QuoteAsset && c.IndexSymbol == other.IndexSymbol && c.TickSize.Equal(other.TickSize) &&
		c.LotSize.Equal(other.LotSize) && c.MinQuantity.Equal(other.MinQuantity) && c.MaxQuantity.Equal(other.MaxQuantity) &&
		c.MinNotional.Equal(other.MinNotional) && c.PriceBand.Equal(other.PriceBand) &&
		c.FundingIntervalHours == other.FundingIntervalHours && c.InterestRate.Equal(other.InterestRate) &&
		c.FundingCap.Equal(other.FundingCap) && c.ImpactNotional.Equal(other.ImpactNotional) && c.FeeTier == other.FeeTier
}
