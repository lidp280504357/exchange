package domain

import "testing"

// The clearance fee is what the liquidation left of the cross account, at
// most its equity at the take-over and what is available now (C68).
func TestTheClearanceFee(t *testing.T) {
	l := CrossLiquidation{Equity: d("900"), Balance: d("2780"), Flows: d("-1882.02")}
	if !l.Left().Equal(d("897.98")) {
		t.Fatalf("left %s", l.Left())
	}
	for _, c := range []struct{ available, want string }{
		{"897.98", "897.98"},  // the account as the liquidation left it
		{"1397.98", "897.98"}, // 500 came in meanwhile: not taken
		{"600", "600"},        // 297.98 went out meanwhile: what is there
		{"0", "0"},
	} {
		if got := l.ClearanceFee(d(c.available), 6); !got.Equal(d(c.want)) {
			t.Fatalf("available %s: fee %s, want %s", c.available, got, c.want)
		}
	}
	// At most the equity at the take-over (a fill better than the mark).
	l.Flows = d("-1700")
	if got := l.ClearanceFee(d("2000"), 6); !got.Equal(d("900")) {
		t.Fatalf("capped at the equity: %s", got)
	}
	// Nothing left (the insurance fund paid the rest): no fee.
	l.Flows = d("-2900")
	if got := l.ClearanceFee(d("10"), 6); !got.IsZero() {
		t.Fatalf("nothing left: %s", got)
	}
	// Down to the asset's decimals.
	l.Flows = d("-1882.0212345678")
	if got := l.ClearanceFee(d("1000"), 6); !got.Equal(d("897.978765")) {
		t.Fatalf("decimals: %s", got)
	}
}

// A fill's flow: the realized result, the fund's part of a loss, less the
// fee charged.
func TestAFillsFlow(t *testing.T) {
	f := Fill{RealizedPnL: d("-1758.16"), Insurance: d("0"), Fee: d("124.07")}
	if !f.Flow().Equal(d("-1882.23")) {
		t.Fatalf("flow %s", f.Flow())
	}
	f = Fill{RealizedPnL: d("-100"), Insurance: d("30"), Fee: d("0")}
	if !f.Flow().Equal(d("-70")) {
		t.Fatalf("a loss the fund paid part of: %s", f.Flow())
	}
}
