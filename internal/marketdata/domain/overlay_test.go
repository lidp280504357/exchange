package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func bookOf(pq ...string) []Level {
	var out []Level
	for i := 0; i+1 < len(pq); i += 2 {
		out = append(out, Level{Price: decimal.RequireFromString(pq[i]), Quantity: decimal.RequireFromString(pq[i+1])})
	}
	return out
}

func shown(ls []Level) string {
	s := ""
	for _, l := range ls {
		s += l.Price.String() + "x" + l.Quantity.String() + " "
	}
	return s
}

// A factor scales the prices onto the tick, bids down and asks up,
// merging the levels that meet; a trade's price rounds to the nearest;
// a factor of 1 changes nothing.
func TestScalingByAnOverlay(t *testing.T) {
	f, cent := decimal.RequireFromString("1.16"), decimal.RequireFromString("0.01")
	if got := shown(ScaleLevels(bookOf("86000.01", "1", "86000.00", "2", "85999.99", "3"), f, true, cent)); got != "99760.01x1 99760x2 99759.98x3 " {
		t.Fatalf("bids %s", got)
	}
	if got := shown(ScaleLevels(bookOf("86000.01", "1", "86000.02", "2"), f, false, cent)); got != "99760.02x1 99760.03x2 " {
		t.Fatalf("asks %s", got)
	}
	// 0.1 tick: 100.1 and 100.2 times 0.5 meet at 50.1 for the asks.
	half, dime := decimal.RequireFromString("0.5"), decimal.RequireFromString("0.1")
	if got := shown(ScaleLevels(bookOf("100.1", "1", "100.2", "2", "100.4", "3"), half, false, dime)); got != "50.1x3 50.2x3 " {
		t.Fatalf("merged %s", got)
	}
	if got := ScalePrice(decimal.RequireFromString("86000.01"), f, cent).String(); got != "99760.01" {
		t.Fatalf("trade %s", got)
	}
	same := bookOf("1", "1")
	if got := ScaleLevels(same, decimal.NewFromInt(1), true, cent); &got[0] != &same[0] {
		t.Fatal("a factor of 1 copied the book")
	}
}

// The reference market sends its prices with eight decimals
// (84221.69000000): scaled, they go onto the pair's tick, not to the eighth
// decimal; without a known tick, to the price's own decimals with its
// trailing zeros dropped.
func TestScaledPricesGoOntoTheTick(t *testing.T) {
	f := decimal.RequireFromString("1.0144")
	sent := decimal.RequireFromString("84221.69000000")
	if got := ScalePrice(sent, f, decimal.RequireFromString("0.01")).String(); got != "85434.48" {
		t.Fatalf("on the tick %s", got)
	}
	if got := ScalePrice(sent, f, decimal.RequireFromString("0.1")).String(); got != "85434.5" {
		t.Fatalf("on a 0.1 tick %s", got)
	}
	if got := ScalePrice(sent, f, decimal.Zero).String(); got != "85434.48" {
		t.Fatalf("no tick known %s", got)
	}
	bids := ScaleLevels(bookOf("84221.69000000", "1"), f, true, decimal.RequireFromString("0.01"))
	asks := ScaleLevels(bookOf("84221.70000000", "1"), f, false, decimal.RequireFromString("0.01"))
	if bids[0].Price.String() != "85434.48" || asks[0].Price.String() != "85434.5" {
		t.Fatalf("book %s %s", bids[0].Price, asks[0].Price)
	}
}
