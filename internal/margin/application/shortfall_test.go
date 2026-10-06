package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

// TestLiquidationShortfall checks a liquidation whose insurance fund
// lacks the asset to cover (review CY (a)): SHORTFALL, the debt still
// owed, until the fund holds it and the next attempt goes on.
func TestLiquidationShortfall(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	cross := domain.Cross()
	r.svc.Trading = &trading{ledger: r.ledger, prices: r.prices}
	m := &application.Monitor{Svc: r.svc}
	u := uuid.Must(uuid.NewV7()).String()
	r.ledger.fund(u, "USDT", d("1000"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: u, IdemKey: "t", Direction: domain.DirectionIn, Account: cross, Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: u, IdemKey: "b", Account: cross, Asset: "BTC", Amount: d("0.06")}); err != nil {
		t.Fatal(err)
	}
	// The 0.06 BTC sold for 1800 USDT; then BTC doubles: 2800 against 3600.
	r.ledger.mu.Lock()
	r.ledger.margin[u][cross]["BTC"].free = decimal.Zero
	r.ledger.margin[u][cross]["USDT"].free = d("2800")
	r.ledger.mu.Unlock()
	r.prices.setBTC("60000", true)
	l, err := r.svc.StartLiquidation(ctx, u, cross, ports.TriggerManual, uuid.Must(uuid.NewV7()).String(), "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	pass := func(n int, step time.Duration) {
		t.Helper()
		for range n {
			r.at(r.now().Add(step))
			if _, err := m.Pass(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	pass(6, 3*time.Second)
	got, _, err := r.store.Read().Liquidations().Get(ctx, l.ID)
	if err != nil || got.Status != ports.LiquidationShortfall || got.Step != application.StepCover {
		t.Fatalf("short of BTC %+v %v", got, err)
	}
	if b := r.ledger.owed(u, cross, "BTC"); !b.borrowed.IsPositive() {
		t.Fatalf("the debt left owed %+v", b)
	}
	// The fund gets BTC; a minute on, the cover goes through.
	r.ledger.mu.Lock()
	r.ledger.insurance["BTC"] = d("1")
	r.ledger.mu.Unlock()
	pass(2, time.Minute)
	got, _, err = r.store.Read().Liquidations().Get(ctx, l.ID)
	if err != nil || got.Status != ports.LiquidationCompleted || !got.InsuranceCovered.IsPositive() || !got.Fee.IsZero() {
		t.Fatalf("covered %+v %v", got, err)
	}
	if b := r.ledger.owed(u, cross, "BTC"); !b.borrowed.IsZero() || !b.interest.IsZero() {
		t.Fatalf("nothing owed after %+v", b)
	}
}
