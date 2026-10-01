package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func TestAdminCloseCancelsTheClosingOrdersFirst(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	long := r.place(t, alice, domain.Buy, "60000", "0.2", false)
	short := r.place(t, bob, domain.Sell, "60000", "0.2", false)
	r.trade(t, long, short, "60000")
	resting := r.place(t, alice, domain.Sell, "61000", "0.1", true)

	if _, err := r.svc.AdminClose(ctx, alice, perp.Symbol, "SIDEWAYS", "c1"); apperr.From(err).Code != apperr.CodeInvalidArgument {
		t.Fatalf("a bad side: %v", err)
	}
	if _, err := r.svc.AdminClose(ctx, alice, perp.Symbol, domain.SideBoth, "c1"); apperr.From(err).Code != "DERIV_CLOSE_PENDING" {
		t.Fatalf("a closing order rests on it: %v", err)
	}
	o, err := r.svc.Get(ctx, alice, resting.ID)
	if err != nil || !o.CancelRequested {
		t.Fatalf("its cancel is asked for: %+v %v", o, err)
	}
	if _, err := r.svc.AdminClose(ctx, alice, perp.Symbol, domain.SideBoth, "c1"); apperr.From(err).Code != "DERIV_CLOSE_PENDING" {
		t.Fatalf("until the engine confirms: %v", err)
	}
	r.seq++
	if err := r.svc.OnUpdate(ctx, domain.Update{OrderID: resting.ID, Seq: r.seq, Status: domain.StatusCanceled, Filled: d("0"), FilledQuote: d("0")}); err != nil {
		t.Fatal(err)
	}

	closing, err := r.svc.AdminClose(ctx, alice, perp.Symbol, domain.SideBoth, "c1")
	if err != nil || closing.Kind != domain.KindAdmin || closing.Side != domain.Sell || !closing.ReduceOnly || closing.Type != domain.Market ||
		!closing.Qty.Equal(d("0.2")) || closing.ClientOrderID != "c1" {
		t.Fatalf("the close %+v %v", closing, err)
	}
	again, err := r.svc.AdminClose(ctx, alice, perp.Symbol, domain.SideBoth, "c1")
	if err != nil || again.ID != closing.ID {
		t.Fatalf("repeated: %+v %v", again, err)
	}
	other, err := r.svc.AdminClose(ctx, bob, perp.Symbol, domain.SideBoth, "c2")
	if err != nil || other.Side != domain.Buy || !other.Qty.Equal(d("0.2")) {
		t.Fatalf("the short's close %+v %v", other, err)
	}
	if _, err := r.svc.AdminClose(ctx, uuid.NewString(), perp.Symbol, domain.SideBoth, ""); apperr.From(err).Code != "DERIV_NO_POSITION" {
		t.Fatalf("nothing to close: %v", err)
	}

	// The engine fills the two closes against each other: both flat.
	r.trade(t, closing, other, "60500")
	if p := r.position(t, alice); !p.Qty.IsZero() {
		t.Fatalf("alice still holds %s", p.Qty)
	}
	r.reconcile(t)
}
