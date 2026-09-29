package domain

import (
	"testing"
	"time"
)

func TestMarginStatesAndLiquidationOrders(t *testing.T) {
	// An isolated long of 1 at 60000 with 1200 of margin (50x): its
	// maintenance margin at 58900 is 58900 x 0.01 = 589 (tier 2).
	long := Position{
		UserID: "u1", Symbol: btcPerp.Symbol, Side: SideBoth, Qty: d("1"), EntryCost: d("60000"), Margin: d("1200"),
		MarginMode: Isolated, Leverage: 50,
	}
	for _, c := range []struct {
		mark string
		want MarginState
	}{
		{"60000", MarginHealthy},
		{"59400", MarginWarning},   // balance 600 <= 1.2 x 594
		{"59390", MarginLiquidate}, // 590 <= 593.9
	} {
		mark := d(c.mark)
		if got := State(long.MarginBalance(mark), long.MaintenanceMargin(btcPerp, mark)); got != c.want {
			t.Errorf("at %s: %v, want %v", c.mark, got, c.want)
		}
	}
	// Bankruptcy at (60000 − 1200) / 1 = 58800; the liquidation sell goes
	// 0.5% below it.
	o := LiquidationOrder("l1", btcPerp, long, long.BankruptcyPrice(), time.Now())
	if o.Side != Sell || !o.Price.Equal(d("58506")) || o.TimeInForce != IOC || o.Kind != KindLiquidation || o.Reserving() || !o.ReduceOnly {
		t.Fatalf("liquidation order %+v", o)
	}
	short := Position{UserID: "u2", Side: SideShort, Qty: d("-1"), EntryCost: d("60000"), Margin: d("1200"), MarginMode: Isolated}
	if o := LiquidationOrder("l2", btcPerp, short, short.BankruptcyPrice(), time.Now()); o.Side != Buy || !o.Price.Equal(d("61506")) || o.ReduceOnly {
		t.Fatalf("liquidation buy %+v", o)
	}
	if got := ADLPrice(btcPerp, long, d("59000")); !got.Equal(d("58800")) {
		t.Fatalf("ADL price %s", got)
	}
}

func TestTheADLQueue(t *testing.T) {
	liquidated := Position{UserID: "u0", Qty: d("-2"), EntryCost: d("120000"), Margin: d("2400")}
	mark := d("61000")
	queue := ADLQueue(liquidated, []Position{
		{UserID: "a", Qty: d("1"), EntryCost: d("59000"), Margin: d("5900")},  // +2000, 10x
		{UserID: "b", Qty: d("1"), EntryCost: d("60000"), Margin: d("1200")},  // +1000, 50x
		{UserID: "c", Qty: d("1"), EntryCost: d("62000"), Margin: d("6200")},  // at a loss
		{UserID: "d", Qty: d("-1"), EntryCost: d("60000"), Margin: d("6000")}, // same side
		{UserID: "u0", Qty: d("1"), EntryCost: d("60000"), Margin: d("6000")}, // the same user
		{UserID: "e", Qty: d("1"), EntryCost: d("50000"), Margin: d("5000"), Liquidating: true},
	}, mark)
	var order []string
	for _, p := range queue {
		order = append(order, p.UserID)
	}
	// b: 1000/60000 x 61000/2200 = 0.462; a: 2000/59000 x 61000/7900 = 0.262; c negative.
	if len(order) != 3 || order[0] != "b" || order[1] != "a" || order[2] != "c" {
		t.Fatalf("queue %v", order)
	}
}
