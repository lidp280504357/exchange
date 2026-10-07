package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
)

// A deleveraged counterparty's closing order beyond what ADL left of its
// position is canceled (review C62): filled, it would open the other way.
func TestADLCancelsTheClosingOrdersItLeavesNoRoomFor(t *testing.T) {
	r, alice, bob := liquidationSetup(t)
	ctx := context.Background()
	// Alice, short 0.5, rests a reduce-only buy of all of it.
	closing := r.place(t, alice, domain.Buy, "57500", "0.5", true)
	r.monitor(t, "59000") // Bob's long taken over
	for range domain.MaxLiquidationAttempts {
		r.monitor(t, "59000")
		liq, ok := r.liquidationOrder(t, bob)
		if !ok {
			t.Fatal("no liquidation order")
		}
		r.seq++
		if err := r.svc.OnUpdate(ctx, domain.Update{
			OrderID: liq.ID, Seq: r.seq, Status: domain.StatusCanceled, Filled: decimal.Zero, FilledQuote: decimal.Zero, Reason: "IOC",
		}); err != nil {
			t.Fatal(err)
		}
	}
	r.monitor(t, "59000") // deleveraged against Alice's short
	if p := r.position(t, alice); !p.Flat() {
		t.Fatalf("alice %+v", p)
	}
	if o, err := r.svc.Get(ctx, alice, closing.ID); err != nil || !o.CancelRequested {
		t.Fatalf("alice's reduce-only buy after ADL %+v %v", o, err)
	}
	r.reconcile(t)
}

// ADL cancels the counterparty's closing orders on the side it takes from
// before it does, even one that would still fit after (review C65 ②, as
// Binance cancels the orders of a position it closes).
func TestADLCancelsTheCounterpartysClosingOrdersFirst(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob, carol := "01928f3a-0000-7000-8000-00000000a11c", "01928f3a-0000-7000-8000-000000000b0b", "01928f3a-0000-7000-8000-0000000ca201"
	r.fund(alice, "10000")
	r.fund(bob, "1000")
	r.fund(carol, "10000")
	fifty, isolated := int32(50), domain.Isolated
	if _, err := r.svc.UpdateSettings(ctx, bob, perp.Symbol, application.SettingsChange{MarginMode: &isolated, Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	r.trade(t, r.place(t, bob, domain.Buy, "60000", "0.5", false), r.place(t, alice, domain.Sell, "60000", "0.5", false), "60000")
	r.trade(t, r.place(t, carol, domain.Buy, "60000", "0.3", false), r.place(t, alice, domain.Sell, "60000", "0.3", false), "60000")
	// Alice is short 0.8: a reduce-only buy of 0.2 would still fit once ADL
	// takes 0.5 of it.
	closing := r.place(t, alice, domain.Buy, "57500", "0.2", true)
	r.monitor(t, "59000") // Bob's long taken over
	for range domain.MaxLiquidationAttempts {
		r.monitor(t, "59000")
		liq, ok := r.liquidationOrder(t, bob)
		if !ok {
			t.Fatal("no liquidation order")
		}
		r.seq++
		if err := r.svc.OnUpdate(ctx, domain.Update{
			OrderID: liq.ID, Seq: r.seq, Status: domain.StatusCanceled, Filled: decimal.Zero, FilledQuote: decimal.Zero, Reason: "IOC",
		}); err != nil {
			t.Fatal(err)
		}
	}
	r.monitor(t, "59000") // deleveraged against Alice's short
	if p := r.position(t, alice); !p.Qty.Equal(d("-0.3")) {
		t.Fatalf("alice %+v", p)
	}
	if o, err := r.svc.Get(ctx, alice, closing.ID); err != nil || !o.CancelRequested {
		t.Fatalf("alice's reduce-only buy, canceled before ADL %+v %v", o, err)
	}
	r.reconcile(t)
}

// A take-over cancels the order a take-profit placed on the position too,
// not only the user's own (review C65 ②).
func TestATakeOverCancelsATakeProfitsOrder(t *testing.T) {
	r, _, bob := liquidationSetup(t)
	ctx := context.Background()
	if _, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: bob, Symbol: perp.Symbol, Kind: domain.TakeProfit, TriggerPrice: d("60100"), OrderType: domain.Limit,
		Price: d("60500"), Qty: d("0.5"),
	}); err != nil {
		t.Fatal(err)
	}
	r.book.Set(perp.Symbol, d("60100"), time.Now())
	if n, err := r.svc.Trigger(ctx); err != nil || n != 1 {
		t.Fatalf("trigger: %d %v", n, err)
	}
	list, _, err := r.svc.Conditionals(ctx, bob, perp.Symbol, "", "", 10)
	if err != nil || len(list) != 1 || list[0].OrderID == "" {
		t.Fatalf("the take-profit %+v %v", list, err)
	}
	r.monitor(t, "59000") // taken over
	if o, err := r.svc.Get(ctx, bob, list[0].OrderID); err != nil || o.Kind != domain.KindTakeProfit || !o.CancelRequested {
		t.Fatalf("the take-profit's resting order after the take-over %+v %v", o, err)
	}
}

// An order the other way filling first leaves a reduce-only order no room:
// it is canceled, the older one that still fits stays.
func TestAFillTheOtherWayTrimsTheClosingOrders(t *testing.T) {
	r := setup(t)
	alice, bob := "01928f3a-0000-7000-8000-00000000a11c", "01928f3a-0000-7000-8000-000000000b0b"
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	r.trade(t, r.place(t, alice, domain.Buy, "60000", "0.5", false), r.place(t, bob, domain.Sell, "60000", "0.5", false), "60000")
	keeps := r.place(t, alice, domain.Sell, "61000", "0.1", true)
	goes := r.place(t, alice, domain.Sell, "61100", "0.3", true)
	// A plain sell of 0.35 closes most of the long: 0.15 left.
	r.trade(t, r.place(t, bob, domain.Buy, "60000", "0.35", false), r.place(t, alice, domain.Sell, "60000", "0.35", false), "60000")
	ctx := context.Background()
	if o, err := r.svc.Get(ctx, alice, keeps.ID); err != nil || o.CancelRequested {
		t.Fatalf("the 0.1 that fits %+v %v", o, err)
	}
	if o, err := r.svc.Get(ctx, alice, goes.ID); err != nil || !o.CancelRequested {
		t.Fatalf("the 0.3 beyond the 0.15 left %+v %v", o, err)
	}
}
