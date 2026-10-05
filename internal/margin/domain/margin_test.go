package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDefaultTerms(t *testing.T) {
	cases := []struct {
		account         AccountType
		leverage        int
		warn, liquidate string
	}{
		{AccountCross, 3, "1.3", "1.1"},
		{AccountCross, 5, "1.2", "1.1"},
		{AccountIsolated, 3, "1.25", "1.15"},
		{AccountIsolated, 5, "1.2", "1.1"},
		{AccountIsolated, 10, "1.1", "1.05"},
	}
	for _, c := range cases {
		got := DefaultTerms(c.account, c.leverage)
		if got.Leverage != c.leverage || got.WarnLevel.String() != c.warn || got.LiquidationLevel.String() != c.liquidate || got.LiquidationFee.String() != "0.02" {
			t.Errorf("%s %dx: %+v, want warn %s, liquidate %s, fee 0.02", c.account, c.leverage, got, c.warn, c.liquidate)
		}
	}
}

func TestLevel(t *testing.T) {
	if l, ok := Level(decimal.RequireFromString("1300"), decimal.RequireFromString("1000")); !ok || l.String() != "1.3" {
		t.Errorf("1300 / 1000 = %s (%v), want 1.3", l, ok)
	}
	if _, ok := Level(decimal.RequireFromString("1300"), decimal.Zero); ok {
		t.Error("no liabilities gave a level")
	}
}
