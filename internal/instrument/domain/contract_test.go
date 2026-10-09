package domain

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
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
		"leverage cap":   func(c *Contract) { c.RiskTiers[0].MaxLeverage = LeverageCap + 1 },
	} {
		bad := perp()
		bad.RiskTiers = append([]RiskTier(nil), bad.RiskTiers...)
		c(&bad)
		if err := bad.Validate(btc, usdt); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Binance's BTCUSDT goes to 150x on its first bracket (B171).
	binance := perp()
	binance.RiskTiers = []RiskTier{{MaxNotional: d("300000"), MaxLeverage: LeverageCap, MMR: d("0.004")}, {MaxNotional: d("800000"), MaxLeverage: 100, MMR: d("0.005")}}
	if err := binance.Validate(btc, usdt); err != nil || binance.MaxLeverage() != 150 {
		t.Fatalf("150x first tier: %v, max %d", err, binance.MaxLeverage())
	}
	disabled := btc
	disabled.TradingEnabled = false
	if err := perp().Validate(disabled, usdt); err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("disabled asset: %v", err)
	}
}

// ValidateTiers is the check gen-contracts.go makes on Binance's brackets
// before it writes them (B174 ④): Binance's 12 brackets of BTCUSDT pass;
// a ladder apply would refuse is refused with its contract and tier.
func TestValidateTiers(t *testing.T) {
	type row struct {
		notional string
		lev      int32
		mmr      string
	}
	ladder := func(rows ...row) []RiskTier {
		out := make([]RiskTier, len(rows))
		for i, r := range rows {
			out[i] = RiskTier{MaxNotional: d(r.notional), MaxLeverage: r.lev, MMR: d(r.mmr)}
		}
		return out
	}
	btc := ladder(row{"300000", 150, "0.004"}, row{"800000", 100, "0.005"}, row{"3000000", 75, "0.0065"}, row{"12000000", 50, "0.01"},
		row{"70000000", 25, "0.02"}, row{"100000000", 20, "0.025"}, row{"230000000", 10, "0.05"}, row{"480000000", 5, "0.1"},
		row{"600000000", 4, "0.125"}, row{"800000000", 3, "0.15"}, row{"1200000000", 2, "0.25"}, row{"1800000000", 1, "0.5"})
	if err := ValidateTiers("BTC-USDT-PERP", btc); err != nil {
		t.Fatalf("Binance's BTCUSDT: %v", err)
	}
	many := make([]RiskTier, 21)
	for i := range many {
		many[i] = RiskTier{MaxNotional: decimal.NewFromInt(int64(i+1) * 1000), MaxLeverage: 1, MMR: d("0.5")}
	}
	for _, c := range []struct {
		name  string
		tiers []RiskTier
		want  string
	}{
		{"none", nil, "1 to 20"},
		{"21", many, "1 to 20"},
		{"a cap of 0", ladder(row{"0", 20, "0.01"}), "tier 1: max_notional must be positive"},
		{"151x", ladder(row{"300000", 151, "0.004"}), "tier 1: max_leverage must be 1 to 150"},
		{"0x", ladder(row{"300000", 0, "0.004"}), "tier 1: max_leverage must be 1 to 150"},
		{"liquidated on opening", ladder(row{"300000", 150, "0.007"}), "tier 1: mmr must be above 0 and below"},
		{"a cap that does not rise", ladder(row{"300000", 150, "0.004"}, row{"300000", 100, "0.005"}), "tier 2: max_notional must rise"},
		{"leverage that rises", ladder(row{"300000", 100, "0.004"}, row{"800000", 125, "0.005"}), "tier 2: max_leverage must not rise"},
		{"a rate that falls", ladder(row{"300000", 100, "0.005"}, row{"800000", 75, "0.004"}), "tier 2: mmr must not fall"},
	} {
		err := ValidateTiers("XYZ-USDT-PERP", c.tiers)
		if err == nil || !strings.Contains(err.Error(), "contract XYZ-USDT-PERP: ") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
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

// inversePerp is BTC-USD-PERP, coin-margined as Binance's BTCUSD_PERP
// (design 2026-10-06 §2.1): 100 USD a contract, settled in BTC.
func inversePerp() Contract {
	c := perp()
	c.Symbol, c.QuoteAsset, c.MarginType, c.SettleAsset, c.ContractSize = "BTC-USD-PERP", CoinQuote, MarginCoin, "BTC", d("100")
	c.LotSize, c.MinQuantity, c.MaxQuantity, c.MinNotional, c.ImpactNotional = d("1"), d("1"), d("60000"), d("100"), d("100")
	c.ReferenceSymbol = "BTCUSD_PERP"
	c.RiskTiers = []RiskTier{
		{MaxNotional: d("5"), MaxLeverage: 125, MMR: d("0.004")},
		{MaxNotional: d("10"), MaxLeverage: 100, MMR: d("0.005")},
	}
	return c
}

func TestContractMarginTypes(t *testing.T) {
	linear := perp().WithDefaults()
	if linear.MarginType != MarginUSDT || linear.SettleAsset != "USDT" || linear.Inverse() || linear.PriceAsset() != "USDT" {
		t.Fatalf("a contract without a margin type is linear, settled in its quote asset: %+v", linear)
	}
	inverse := inversePerp()
	if err := inverse.Validate(btc, usdt); err != nil {
		t.Fatalf("valid inverse contract: %v", err)
	}
	if !inverse.Inverse() || inverse.PriceAsset() != "USDT" {
		t.Fatalf("an inverse contract's prices follow USDT: %+v", inverse)
	}
	unsettled := inverse
	unsettled.SettleAsset = ""
	if got := unsettled.WithDefaults().SettleAsset; got != "BTC" {
		t.Fatalf("an inverse contract settles in its base asset by default, got %q", got)
	}
	for name, change := range map[string]func(*Contract){
		"inverse settles in quote":   func(c *Contract) { c.SettleAsset = "USDT" },
		"inverse without face value": func(c *Contract) { c.ContractSize = d("0") },
		"inverse quoted in USDT":     func(c *Contract) { c.Symbol, c.QuoteAsset = "BTC-USDT-PERP", "USDT" },
		"inverse fractional lots":    func(c *Contract) { c.LotSize, c.MinQuantity = d("0.5"), d("0.5") },
		"inverse index not USDT":     func(c *Contract) { c.IndexSymbol = "BTC-USD" },
		"face value too fine":        func(c *Contract) { c.ContractSize = d("0.0000001") },
		"unknown margin type":        func(c *Contract) { c.MarginType = "USDC" },
		"reference symbol":           func(c *Contract) { c.ReferenceSymbol = "btcusd-perp" },
	} {
		bad := inversePerp()
		change(&bad)
		if err := bad.Validate(btc, usdt); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, change := range map[string]func(*Contract){
		"linear with face value":      func(c *Contract) { c.ContractSize = d("100") },
		"linear settles in base":      func(c *Contract) { c.SettleAsset = "BTC" },
		"linear quoted in USD":        func(c *Contract) { c.Symbol, c.QuoteAsset = "BTC-USD-PERP", CoinQuote },
		"linear with a long referent": func(c *Contract) { c.ReferenceSymbol = strings.Repeat("A", 21) },
	} {
		bad := perp()
		change(&bad)
		if err := bad.Validate(btc, usdt); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestContractKindNeverChanges(t *testing.T) {
	a, b := inversePerp(), inversePerp()
	b.ReferenceSymbol, b.MaxQuantity = "", d("1000")
	if !a.SameKind(b) || a.SameConfig(b) {
		t.Fatal("the reference symbol and the limits change; the kind stays")
	}
	b.ContractSize = d("10")
	if a.SameKind(b) {
		t.Fatal("the face value is the contract's kind")
	}
	c := perp().WithDefaults()
	if c.SameKind(a) {
		t.Fatal("linear and inverse are different kinds")
	}
}
