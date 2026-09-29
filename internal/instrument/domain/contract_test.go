package domain

import (
	"strings"
	"testing"
)

func perp() Contract {
	return Contract{
		Symbol: "BTC-USDT-PERP", Type: ContractPerpetual, BaseAsset: "BTC", QuoteAsset: "USDT", IndexSymbol: "BTC-USDT",
		TickSize: d("0.1"), LotSize: d("0.001"), MinQuantity: d("0.001"), MaxQuantity: d("100"), MinNotional: d("5"),
		PriceBand: d("0.05"), FundingIntervalHours: 8, InterestRate: d("0.0001"), FundingCap: d("0.0075"),
		ImpactNotional: d("10000"), FeeTier: "perp", Status: StatusPrepare,
		RiskTiers: []RiskTier{
			{MaxNotional: d("50000"), MaxLeverage: 50, MMR: d("0.004")},
			{MaxNotional: d("250000"), MaxLeverage: 20, MMR: d("0.01")},
		},
	}
}

func TestContractValidate(t *testing.T) {
	if err := perp().Validate(btc, usdt); err != nil {
		t.Fatalf("valid contract: %v", err)
	}
	for name, c := range map[string]func(*Contract){
		"symbol":         func(c *Contract) { c.Symbol = "BTC-USDT" },
		"type":           func(c *Contract) { c.Type = "DELIVERY" },
		"index":          func(c *Contract) { c.IndexSymbol = "ETH-USDT" },
		"tick scale":     func(c *Contract) { c.TickSize = d("0.0000001") },
		"lot scale":      func(c *Contract) { c.LotSize = d("0.000000001") },
		"min multiple":   func(c *Contract) { c.MinQuantity = d("0.0015") },
		"max quantity":   func(c *Contract) { c.MaxQuantity = d("0.001") },
		"band":           func(c *Contract) { c.PriceBand = d("1.5") },
		"interval":       func(c *Contract) { c.FundingIntervalHours = 6 },
		"interest":       func(c *Contract) { c.InterestRate = d("0.02") },
		"cap":            func(c *Contract) { c.FundingCap = d("0") },
		"impact":         func(c *Contract) { c.ImpactNotional = d("0") },
		"no tiers":       func(c *Contract) { c.RiskTiers = nil },
		"mmr too high":   func(c *Contract) { c.RiskTiers[0].MMR = d("0.02") }, // not below 1/50
		"leverage rises": func(c *Contract) { c.RiskTiers[1].MaxLeverage = 75 },
		"cap falls":      func(c *Contract) { c.RiskTiers[1].MaxNotional = d("10000") },
		"mmr falls":      func(c *Contract) { c.RiskTiers[1].MMR = d("0.001") },
		"leverage":       func(c *Contract) { c.RiskTiers[0].MaxLeverage = 200 },
	} {
		bad := perp()
		bad.RiskTiers = append([]RiskTier(nil), bad.RiskTiers...)
		c(&bad)
		if err := bad.Validate(btc, usdt); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	disabled := btc
	disabled.TradingEnabled = false
	if err := perp().Validate(disabled, usdt); err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("disabled asset: %v", err)
	}
}

func TestContractTiers(t *testing.T) {
	c := perp()
	if c.MaxLeverage() != 50 {
		t.Fatalf("max leverage %d", c.MaxLeverage())
	}
	for notional, want := range map[string]int32{"1": 50, "50000": 50, "50000.01": 20, "250000": 20} {
		tier, ok := c.Tier(d(notional))
		if !ok || tier.MaxLeverage != want {
			t.Errorf("notional %s: tier %+v %v, want leverage %d", notional, tier, ok, want)
		}
	}
	if _, ok := c.Tier(d("250000.01")); ok {
		t.Fatal("a notional above the last tier fits no tier")
	}
}

func TestContractSameConfig(t *testing.T) {
	a, b := perp(), perp()
	b.Status, b.Version = StatusTrading, 7
	if !a.SameConfig(b) {
		t.Fatal("status and version are not configuration")
	}
	b.RiskTiers = []RiskTier{a.RiskTiers[0], {MaxNotional: d("250000"), MaxLeverage: 20, MMR: d("0.011")}}
	if a.SameConfig(b) {
		t.Fatal("a changed tier is a change")
	}
}
