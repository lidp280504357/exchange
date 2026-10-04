package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestValidWelcomeCredits(t *testing.T) {
	one := decimal.NewFromInt(1)
	if err := ValidWelcomeCredits(nil); err != nil {
		t.Fatalf("none: %v", err)
	}
	if err := ValidWelcomeCredits([]AssetAmount{{Asset: "USDT", Amount: decimal.NewFromInt(10000)}, {Asset: "BTC", Amount: decimal.RequireFromString("0.1")}}); err != nil {
		t.Fatalf("two: %v", err)
	}
	many := make([]AssetAmount, 0, MaxWelcomeCredits+1)
	for _, a := range []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10", "A11"} {
		many = append(many, AssetAmount{Asset: a, Amount: one})
	}
	for name, list := range map[string][]AssetAmount{
		"lower case": {{Asset: "usdt", Amount: one}},
		"empty code": {{Asset: "", Amount: one}},
		"zero":       {{Asset: "USDT", Amount: decimal.Zero}},
		"negative":   {{Asset: "USDT", Amount: one.Neg()}},
		"twice":      {{Asset: "USDT", Amount: one}, {Asset: "USDT", Amount: one}},
		"eleven":     many,
	} {
		if err := ValidWelcomeCredits(list); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestValidSettingChange(t *testing.T) {
	if err := ValidSettingChange("admin:a@example.com", "going live"); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"", "going live"}, {"admin:a@example.com", "no"}, {"admin:a@example.com", "   "}} {
		if err := ValidSettingChange(c[0], c[1]); err == nil {
			t.Fatalf("accepted %q", c)
		}
	}
}
