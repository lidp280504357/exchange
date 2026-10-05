package domain

import (
	"slices"
	"testing"

	"github.com/shopspring/decimal"
)

var testTerms = map[string]AssetTerms{
	"USDT": {Asset: "USDT", Decimals: 6, Borrowable: true, Collateral: true, Haircut: d("1"), PoolCap: d("2000000"), UserCap: d("200000")},
	"BTC":  {Asset: "BTC", Decimals: 8, Borrowable: true, Collateral: true, Haircut: d("0.95"), PoolCap: d("20"), UserCap: d("2")},
	"ETH":  {Asset: "ETH", Decimals: 8, Borrowable: true, Collateral: true, Haircut: d("0.95"), PoolCap: d("400"), UserCap: d("40")},
	"DOGE": {Asset: "DOGE", Decimals: 8, Borrowable: true, Collateral: true, Haircut: d("0.9"), PoolCap: d("5000000"), UserCap: d("500000")},
	"XYZ":  {Asset: "XYZ", Decimals: 8, Borrowable: true, Collateral: false, Haircut: d("0.7"), PoolCap: d("100"), UserCap: d("10")},
}

func fresh(v string) Price { return Price{Value: d(v), Fresh: true} }

func TestValueWithHaircutsAndMissingPrices(t *testing.T) {
	holdings := []Holding{
		{Asset: "USDT", Free: d("900"), Locked: d("100"), Borrowed: d("2000"), Interest: d("0.5")},
		{Asset: "BTC", Free: d("0.1")},
		{Asset: "ETH", Borrowed: d("1")},
		{Asset: "XYZ", Free: d("5")}, // not collateral: worth nothing, needs no price
	}
	prices := Prices{"BTC": fresh("30000"), "ETH": fresh("2000")}
	v := Value(holdings, testTerms, prices)
	// 1000 x 1 + 0.1 x 30000 x 0.95 = 3850; 2000.5 + 1 x 2000 = 4000.5.
	if !v.TotalAsset.Equal(d("3850")) || !v.TotalLiability.Equal(d("4000.5")) || !v.Complete() || v.Stale {
		t.Fatalf("valuation %+v", v)
	}
	if level, ok := v.Level(); !ok || !level.Equal(d("0.96237970")) {
		t.Errorf("level %s %v", level, ok)
	}
	if !v.Net().Equal(d("-150.5")) {
		t.Errorf("net %s", v.Net())
	}

	// Collateral without a price counts for nothing, a debt without one is
	// left out: either leaves the valuation incomplete.
	v = Value(append(holdings, Holding{Asset: "DOGE", Free: d("100")}, Holding{Asset: "DOGE", Borrowed: d("3")}), testTerms, prices)
	if v.Complete() || !slices.Equal(v.Unpriced, []string{"DOGE"}) || !v.TotalAsset.Equal(d("3850")) ||
		!v.TotalLiability.Equal(d("4000.5")) || !v.HasDebt() {
		t.Fatalf("unpriced: %+v", v)
	}
	// A stale price still counts, at its last value.
	prices["BTC"] = Price{Value: d("30000")}
	if v = Value(holdings, testTerms, prices); !v.Complete() || !v.Stale || !v.TotalAsset.Equal(d("3850")) {
		t.Fatalf("stale: %+v", v)
	}
	// Collateral without a price is no debt (review CK ③).
	if v = Value([]Holding{{Asset: "DOGE", Free: d("1")}}, testTerms, prices); v.HasDebt() || v.Complete() {
		t.Fatalf("unpriced collateral: %+v", v)
	}
	// Without debts there is no level.
	if v = Value(holdings[1:2], testTerms, prices); v.HasDebt() {
		t.Fatalf("no debt: %+v", v)
	} else if _, ok := v.Level(); ok {
		t.Fatal("a level without debts")
	}
}

func TestZones(t *testing.T) {
	cross := DefaultTerms(AccountCross, 3)
	for _, c := range []struct {
		level string
		want  Zone
	}{
		{"1.5", ZoneSafe},
		{"1.3", ZoneSafe}, // warned only under the warning level
		{"1.29999999", ZoneWarn},
		{"1.1", ZoneLiquidate}, // liquidated at the liquidation level
		{"0.8", ZoneLiquidate},
	} {
		if got := cross.Zone(d(c.level)); got != c.want {
			t.Errorf("level %s: %s, want %s", c.level, got, c.want)
		}
	}
	if z := cross.ZoneOf(Valuation{TotalAsset: d("5"), TotalLiability: decimal.Zero}); z != ZoneSafe {
		t.Errorf("no debt: %s", z)
	}
	if z := cross.ZoneOf(Valuation{TotalAsset: d("120"), TotalLiability: d("100")}); z != ZoneWarn {
		t.Errorf("1.2: %s", z)
	}
}

func TestTermsValidate(t *testing.T) {
	for _, lev := range []int{3, 5, 10} {
		if err := DefaultTerms(AccountIsolated, lev).Validate(); err != nil {
			t.Errorf("isolated %dx: %v", lev, err)
		}
	}
	if err := DefaultTerms(AccountCross, 3).Validate(); err != nil {
		t.Error(err)
	}
	for name, terms := range map[string]Terms{
		"1x":            {Leverage: 1, WarnLevel: d("1.3"), LiquidationLevel: d("1.1"), LiquidationFee: d("0.02")},
		"11x":           {Leverage: 11, WarnLevel: d("1.3"), LiquidationLevel: d("1.1"), LiquidationFee: d("0.02")},
		"level 1":       {Leverage: 3, WarnLevel: d("1.3"), LiquidationLevel: d("1"), LiquidationFee: d("0.02")},
		"warn <= liq":   {Leverage: 3, WarnLevel: d("1.1"), LiquidationLevel: d("1.1"), LiquidationFee: d("0.02")},
		"negative fee":  {Leverage: 3, WarnLevel: d("1.3"), LiquidationLevel: d("1.1"), LiquidationFee: d("-0.01")},
		"fee above 10%": {Leverage: 3, WarnLevel: d("1.3"), LiquidationLevel: d("1.1"), LiquidationFee: d("0.11")},
	} {
		if terms.Validate() == nil {
			t.Errorf("%s passed", name)
		}
	}
}
