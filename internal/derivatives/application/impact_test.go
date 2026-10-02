package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/derivatives/application"
	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func ladder(tiers ...domain.RiskTier) []domain.RiskTier { return tiers }

func TestATierChangeShowsWhomItWouldLiquidate(t *testing.T) {
	r, alice, bob := liquidationSetup(t)
	ctx := context.Background()
	// Bob's isolated 50x long of 30,000 has 600 of margin and needs 120 of
	// maintenance; at 2.01% it would need 603, and no tier would allow 50x.
	tighter := ladder(
		domain.RiskTier{MaxNotional: d("50000"), MaxLeverage: 40, MMR: d("0.0201")},
		domain.RiskTier{MaxNotional: d("250000"), MaxLeverage: 20, MMR: d("0.03")},
	)
	imp, err := r.svc.TierImpact(ctx, perp.Symbol, tighter)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Positions != 2 || imp.Liquidated != 1 || !imp.Notional.Equal(d("30000")) || imp.Accounts != 1 || imp.OverLimit != 1 ||
		imp.Warned != 0 || imp.Unmeasured != 0 || len(imp.Examples) != 1 {
		t.Fatalf("impact %+v", imp)
	}
	if e := imp.Examples[0]; e.UserID != bob || e.Cross || !e.MarginBalance.Equal(d("600")) || !e.MaintenanceBefore.Equal(d("120")) ||
		!e.MaintenanceAfter.Equal(d("603")) {
		t.Fatalf("example %+v", e)
	}
	// At 1.9% Bob is only warned; the ladder is not changed either way.
	warned, err := r.svc.TierImpact(ctx, perp.Symbol, ladder(domain.RiskTier{MaxNotional: d("250000"), MaxLeverage: 50, MMR: d("0.019")}))
	if err != nil || warned.Liquidated != 0 || warned.Warned != 1 || warned.OverLimit != 0 {
		t.Fatalf("warned %+v %v", warned, err)
	}
	if list, err := r.svc.RiskPositions(ctx); err != nil || len(list) != 0 {
		t.Fatalf("nothing changed: %+v %v", list, err)
	}
	_ = alice
	for _, bad := range [][]domain.RiskTier{
		nil,
		ladder(domain.RiskTier{MaxNotional: d("50000"), MaxLeverage: 50, MMR: d("0.02")}), // at 1/leverage
		ladder(domain.RiskTier{MaxNotional: d("50000"), MaxLeverage: 20, MMR: d("0.01")}, domain.RiskTier{MaxNotional: d("40000"), MaxLeverage: 10, MMR: d("0.02")}),
	} {
		if _, err := r.svc.TierImpact(ctx, perp.Symbol, bad); apperr.From(err).Kind != apperr.KindInvalid {
			t.Fatalf("ladder %+v: %v", bad, err)
		}
	}
	if _, err := r.svc.TierImpact(ctx, "NOPE-USDT-PERP", tighter); apperr.From(err).Kind != apperr.KindNotFound {
		t.Fatalf("an unknown contract: %v", err)
	}
}

func TestATierChangeLiquidatesACrossAccountWhole(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "700")
	r.fund(bob, "10000")
	fifty := int32(50)
	if _, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	long := r.place(t, bob, domain.Buy, "60000", "0.5", false)
	short := r.place(t, alice, domain.Sell, "60000", "0.5", false)
	r.trade(t, long, short, "60000")
	// Alice's cross equity is 685 (85 available and 600 of margin): 2.4%
	// of her 30,000 short asks 720 of maintenance.
	imp, err := r.svc.TierImpact(ctx, perp.Symbol, ladder(
		domain.RiskTier{MaxNotional: d("50000"), MaxLeverage: 40, MMR: d("0.024")},
		domain.RiskTier{MaxNotional: d("250000"), MaxLeverage: 20, MMR: d("0.03")},
	))
	if err != nil {
		t.Fatal(err)
	}
	if imp.Liquidated != 1 || imp.Accounts != 1 || !imp.Notional.Equal(d("30000")) || imp.OverLimit != 1 || len(imp.Examples) != 1 {
		t.Fatalf("impact %+v", imp)
	}
	if e := imp.Examples[0]; e.UserID != alice || !e.Cross || !e.MarginBalance.Equal(d("685")) || !e.MaintenanceAfter.Equal(d("720")) {
		t.Fatalf("example %+v", e)
	}
	// Without a fresh mark nothing is measured.
	r.book.Set(perp.Symbol, d("60000"), time.Now().Add(-10*time.Minute))
	if imp, err := r.svc.TierImpact(ctx, perp.Symbol, perp.Tiers); err != nil || imp.Unmeasured != 2 || imp.Liquidated != 0 {
		t.Fatalf("stale mark %+v %v", imp, err)
	}
}
