package domain

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

func p(s string) *decimal.Decimal {
	v := d(s)
	return &v
}

// A change applies to the caps in force: within the bounds, and moving
// each cap at most ten times up or down; a level cap going to or from
// zero ("whole") is not a step (review FL, C47).
func TestACapsChangeKeepsToTheBounds(t *testing.T) {
	cur := Caps{Level: d("20000"), Symbol: d("100000"), Total: d("1000000"), Contract: d("100000"), Safety: d("1000"), ContractLeverage: d("10")}
	if err := cur.Validate(); err != nil {
		t.Fatal(err)
	}
	change := func(patch CapsPatch) (Caps, error) {
		return CapsChange{Patch: patch, Version: 1, Actor: "a", Reason: "r"}.Next(cur)
	}
	next, err := change(CapsPatch{Level: p("200000"), Total: p("100000")})
	if err != nil || !next.Level.Equal(d("200000")) || !next.Total.Equal(d("100000")) || !next.Symbol.Equal(d("100000")) {
		t.Fatalf("ten times up and down: %+v %v", next, err)
	}
	if next, err = change(CapsPatch{Level: p("0")}); err != nil || !next.Level.IsZero() {
		t.Fatalf("levels whole: %+v %v", next, err)
	}
	for name, patch := range map[string]CapsPatch{
		"a level below zero":      {Level: p("-1")},
		"no symbol cap":           {Symbol: p("0")},
		"no total cap":            {Total: p("0")},
		"no contract cap":         {Contract: p("0")},
		"no safety":               {Safety: p("0")},
		"no contract leverage":    {ContractLeverage: p("0.5")},
		"leverage past 125":       {ContractLeverage: p("125.1")},
		"an amount past 10^15":    {Symbol: p("1000000000000001")},
		"more than ten times up":  {Symbol: p("1000000.01")},
		"more than ten times off": {Contract: p("9999.99")},
	} {
		if _, err := change(patch); err == nil {
			t.Errorf("%s: allowed", name)
		} else if code := apperr.From(err).Code; code != "COMMON_INVALID_ARGUMENT" && code != "HOUSE_CAPS_STEP" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := change(CapsPatch{Symbol: p("1000000.01")}); apperr.From(err).Code != "HOUSE_CAPS_STEP" {
		t.Fatalf("the step's code: %v", err)
	}
	if err := (CapsChange{Version: 1, Actor: "a", Reason: "r"}).Validate(); err == nil {
		t.Fatal("a change of nothing allowed")
	}
}
