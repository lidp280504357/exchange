package application_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
)

// A cross account taken over cancels each of its orders once, however
// many of its positions the order belongs to (review C67 ②: an order was
// asked to cancel once per cross position of its settlement asset, here
// a hedge-mode long and short).
func TestATakeOverCancelsACrossOrderOnce(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "800")
	r.fund(bob, "10000")
	hedge, fifty := domain.Hedge, int32(50)
	if _, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{PositionMode: &hedge, Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	order := func(side domain.Side, ps domain.PositionSide, price, qty string) domain.Order {
		t.Helper()
		o, err := r.svc.Place(ctx, domain.Request{
			UserID: alice, Symbol: perp.Symbol, Side: side, PositionSide: ps, Type: domain.Limit, Price: d(price), Qty: d(qty),
		})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	r.trade(t, r.place(t, bob, domain.Buy, "60000", "0.5", false), order(domain.Sell, domain.SideShort, "60000", "0.5"), "60000")
	r.trade(t, r.place(t, bob, domain.Sell, "60000", "0.05", false), order(domain.Buy, domain.SideLong, "60000", "0.05"), "60000")
	resting := order(domain.Buy, domain.SideLong, "57500", "0.01")
	// Net short 0.45: the account is taken over as the mark rises.
	taken := func() bool {
		views, err := r.svc.Positions(ctx, alice, perp.Symbol)
		if err != nil || len(views) != 2 {
			t.Fatalf("alice's positions %+v %v", views, err)
		}
		return views[0].Liquidating && views[1].Liquidating
	}
	for mark := 60500; !taken(); mark += 100 {
		if mark > 64000 {
			t.Fatal("never taken over")
		}
		r.monitor(t, strconv.Itoa(mark))
	}
	if o, err := r.svc.Get(ctx, alice, resting.ID); err != nil || !o.CancelRequested {
		t.Fatalf("the resting order %+v %v", o, err)
	}
	if events := r.events(); events != nil {
		cancels := 0
		for _, e := range events {
			if c, ok := e.(*orderv1.CancelOrder); ok && c.GetOrderId() == resting.ID {
				cancels++
			}
		}
		if cancels != 1 {
			t.Fatalf("the resting order's cancel asked for %d times", cancels)
		}
	}
}

// The take-profits and stop-losses waiting on a position taken over end
// with it, CANCELED as LIQUIDATION (review C67 ④); before, they stayed
// ACTIVE until their trigger price was crossed. Another user's stay.
func TestATakeOverEndsThePositionsTakeProfitsAndStopLosses(t *testing.T) {
	r, alice, bob := liquidationSetup(t)
	ctx := context.Background()
	create := func(user, kind, trigger, by string) domain.Conditional {
		t.Helper()
		c, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
			UserID: user, Symbol: perp.Symbol, Kind: kind, TriggerPrice: d(trigger), TriggerBy: by,
		})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	tp := create(bob, domain.TakeProfit, "61000", domain.TriggerMark)
	sl := create(bob, domain.StopLoss, "58000", domain.TriggerLast)
	theirs := create(alice, domain.TakeProfit, "59500", domain.TriggerMark)
	r.book.Set(perp.Symbol, d("60000"), time.Now())
	r.monitor(t, "59000") // Bob's long taken over
	if p := r.position(t, bob); !p.Liquidating {
		t.Fatalf("bob %+v", p)
	}
	status := func(user, id string) domain.Conditional {
		t.Helper()
		list, _, err := r.svc.Conditionals(ctx, user, perp.Symbol, "", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range list {
			if c.ID == id {
				return c
			}
		}
		t.Fatalf("no conditional %s", id)
		return domain.Conditional{}
	}
	for _, id := range []string{tp.ID, sl.ID} {
		if c := status(bob, id); c.Status != domain.ConditionalCanceled || c.Reason != application.ReasonLiquidation {
			t.Fatalf("bob's %s after the take-over %+v", c.Kind, c)
		}
	}
	if c := status(alice, theirs.ID); c.Status != domain.ConditionalActive {
		t.Fatalf("alice's take-profit %+v", c)
	}
}
