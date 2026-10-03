package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func TestAdminCloseCancelsTheUsersOrdersFirst(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	long := r.place(t, alice, domain.Buy, "60000", "0.2", false)
	short := r.place(t, bob, domain.Sell, "60000", "0.2", false)
	r.trade(t, long, short, "60000")
	resting := r.place(t, alice, domain.Sell, "61000", "0.1", true)
	// An opening buy too: filled after the close, it would open the long again (C5.5 ⑧).
	opening := r.place(t, alice, domain.Buy, "59000", "0.1", false)

	if _, err := r.svc.AdminClose(ctx, alice, perp.Symbol, "SIDEWAYS", "c1"); apperr.From(err).Code != apperr.CodeInvalidArgument {
		t.Fatalf("a bad side: %v", err)
	}
	if _, err := r.svc.AdminClose(ctx, alice, perp.Symbol, domain.SideBoth, "c1"); apperr.From(err).Code != "DERIV_CLOSE_PENDING" {
		t.Fatalf("orders rest on it: %v", err)
	}
	for _, id := range []string{resting.ID, opening.ID} {
		o, err := r.svc.Get(ctx, alice, id)
		if err != nil || !o.CancelRequested {
			t.Fatalf("its cancel is asked for: %+v %v", o, err)
		}
	}
	if _, err := r.svc.AdminClose(ctx, alice, perp.Symbol, domain.SideBoth, "c1"); apperr.From(err).Code != "DERIV_CLOSE_PENDING" {
		t.Fatalf("until the engine confirms: %v", err)
	}
	for _, id := range []string{resting.ID, opening.ID} {
		r.seq++
		if err := r.svc.OnUpdate(ctx, domain.Update{OrderID: id, Seq: r.seq, Status: domain.StatusCanceled, Filled: d("0"), FilledQuote: d("0")}); err != nil {
			t.Fatal(err)
		}
	}
	// HOUSE's positions are not closed from the console.
	r.svc.HouseUser = bob
	if _, err := r.svc.AdminClose(ctx, bob, perp.Symbol, domain.SideBoth, "c3"); apperr.From(err).Code != "DERIV_HOUSE_NOT_CLOSED" {
		t.Fatalf("HOUSE's position: %v", err)
	}
	r.svc.HouseUser = ""

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

func TestCrossMarginBeforeADebit(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	if m, err := r.svc.CrossMargin(ctx, alice, "USDT", d("1")); err != nil || m.Positions != 0 || m.StateAfter != domain.MarginHealthy {
		t.Fatalf("no position: %+v %v", m, err)
	}
	long := r.place(t, alice, domain.Buy, "60000", "0.2", false)
	short := r.place(t, bob, domain.Sell, "60000", "0.2", false)
	r.trade(t, long, short, "60000")
	r.book.Set(perp.Symbol, d("60000"), time.Now())
	m, err := r.svc.CrossMargin(ctx, alice, "USDT", decimal.Zero)
	if err != nil || m.Positions != 1 || m.Unmeasured || !m.Maintenance.IsPositive() || m.State != domain.MarginHealthy ||
		!m.EquityAfter.Equal(m.Equity) {
		t.Fatalf("a cross long: %+v %v", m, err)
	}
	// A debit that takes the equity to the maintenance margin liquidates it (C5.5 ⑧).
	if after, err := r.svc.CrossMargin(ctx, alice, "USDT", m.Equity.Sub(m.Maintenance)); err != nil || after.StateAfter != domain.MarginLiquidate {
		t.Fatalf("debited to the maintenance margin: %+v %v", after, err)
	}
	if _, err := r.svc.CrossMargin(ctx, alice, "USDT", d("-1")); apperr.From(err).Code != apperr.CodeInvalidArgument {
		t.Fatalf("a negative debit: %v", err)
	}
	r.book.Set(perp.Symbol, d("60000"), time.Now().Add(-time.Hour))
	if m, err := r.svc.CrossMargin(ctx, alice, "USDT", d("1")); err != nil || !m.Unmeasured {
		t.Fatalf("a stale mark: %+v %v", m, err)
	}
}
