package application_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

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
