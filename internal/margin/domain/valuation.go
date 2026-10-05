package domain

import (
	"slices"

	"github.com/shopspring/decimal"
)

// ValueAsset is the asset every value is in (design §2).
const ValueAsset = "USDT"

// valueDecimals is the precision of values in USDT.
const valueDecimals = 8

// Price is an asset's value in USDT: the platform's last price of its
// USDT pair, or the reference market's ticker for the pairs that follow
// it (the source of the contracts' index prices). Fresh is false for a
// price older than the source's limit; it still counts at its last value
// (decision of 2026-10-06 01:15), only reported as stale.
type Price struct {
	Value decimal.Decimal
	Fresh bool
}

// Prices maps assets to their prices; USDT is always 1.
type Prices map[string]Price

// Of returns the price of asset; ok is false without one.
func (p Prices) Of(asset string) (Price, bool) {
	if asset == ValueAsset {
		return Price{Value: decimal.NewFromInt(1), Fresh: true}, true
	}
	pr, ok := p[asset]
	return pr, ok && pr.Value.IsPositive()
}

// Valuation is a margin account valued in USDT (design §2).
type Valuation struct {
	// TotalAsset is what the collateral is worth after the haircuts;
	// TotalLiability what the debts are worth, principal and interest.
	TotalAsset     decimal.Decimal
	TotalLiability decimal.Decimal
	// Unpriced lists the collateral held and the assets owed that never
	// had a price: held they count for nothing, owed they are left out of
	// the liabilities, so the valuation is not Complete — nothing that
	// adds risk relies on it, and no liquidation (MARGIN_PRICE_UNAVAILABLE).
	Unpriced []string
	// Stale is set when a price used is not fresh.
	Stale bool
	// Owes is set when the account owes anything, priced or not.
	Owes bool
}

// Complete reports whether every asset that counts had a price.
func (v Valuation) Complete() bool { return len(v.Unpriced) == 0 }

// Net is the net assets: total assets less total liabilities.
func (v Valuation) Net() decimal.Decimal { return v.TotalAsset.Sub(v.TotalLiability) }

// Level is the margin level; ok is false without liabilities.
func (v Valuation) Level() (decimal.Decimal, bool) { return Level(v.TotalAsset, v.TotalLiability) }

// HasDebt reports whether the account owes anything (review CK ③: an
// unpriced asset held is no debt).
func (v Valuation) HasDebt() bool { return v.Owes || v.TotalLiability.IsPositive() }

// Value values a margin account's holdings: each asset held counts at
// its price times its haircut if it is collateral (otherwise nothing);
// each debt counts at its price in full. Without a price a held asset
// counts for nothing and a debt is left out; either leaves the valuation
// incomplete (design §2: the most cautious way).
func Value(holdings []Holding, terms map[string]AssetTerms, prices Prices) Valuation {
	v := Valuation{TotalAsset: decimal.Zero, TotalLiability: decimal.Zero}
	unpriced := func(asset string) {
		if !slices.Contains(v.Unpriced, asset) {
			v.Unpriced = append(v.Unpriced, asset)
		}
	}
	for _, h := range holdings {
		price, priced := prices.Of(h.Asset)
		if total := h.Total(); total.IsPositive() {
			if t, ok := terms[h.Asset]; ok && t.Collateral {
				if !priced {
					unpriced(h.Asset)
				} else {
					v.TotalAsset = v.TotalAsset.Add(total.Mul(price.Value).Mul(t.Haircut))
					v.Stale = v.Stale || !price.Fresh
				}
			}
		}
		if debt := h.Debt(); debt.IsPositive() {
			v.Owes = true
			if !priced {
				unpriced(h.Asset)
				continue
			}
			v.TotalLiability = v.TotalLiability.Add(debt.Mul(price.Value))
			v.Stale = v.Stale || !price.Fresh
		}
	}
	slices.Sort(v.Unpriced)
	v.TotalAsset, v.TotalLiability = v.TotalAsset.Round(valueDecimals), v.TotalLiability.RoundCeil(valueDecimals)
	return v
}

// Zone is where a margin level stands against an account's terms.
type Zone int

// Zones: safe at or above the warning level, warned under it, liquidated
// at or under the liquidation level (design §4.4, §4.5).
const (
	ZoneSafe Zone = iota
	ZoneWarn
	ZoneLiquidate
)

func (z Zone) String() string {
	switch z {
	case ZoneWarn:
		return "WARN"
	case ZoneLiquidate:
		return "LIQUIDATE"
	}
	return "SAFE"
}

// ZoneOf places a valuation: without debts it is safe.
func (t Terms) ZoneOf(v Valuation) Zone {
	level, ok := v.Level()
	if !ok {
		return ZoneSafe
	}
	return t.Zone(level)
}

// Zone places a margin level.
func (t Terms) Zone(level decimal.Decimal) Zone {
	switch {
	case level.LessThanOrEqual(t.LiquidationLevel):
		return ZoneLiquidate
	case level.LessThan(t.WarnLevel):
		return ZoneWarn
	}
	return ZoneSafe
}
