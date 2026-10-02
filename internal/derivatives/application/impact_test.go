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

// A target mark price shows whom it would liquidate and what the
// insurance fund would bear, as the monitor would measure it: Bob's
// isolated 50x long of 0.5 at 60,000 has 600 of margin.
func TestATargetPriceShowsWhomItWouldLiquidate(t *testing.T) {
	r, alice, bob := liquidationSetup(t)
	ctx := context.Background()
	// At 58,900 his loss is 550: 50 left against 117.80 of maintenance.
	imp, err := r.svc.PriceImpact(ctx, perp.Symbol, d("58900"))
	if err != nil {
		t.Fatal(err)
	}
	if imp.Positions != 2 || imp.Liquidated != 1 || !imp.Notional.Equal(d("29450")) || imp.Accounts != 1 ||
		!imp.InsuranceCost.IsZero() || imp.Unmeasured != 0 || len(imp.Examples) != 1 {
		t.Fatalf("impact %+v", imp)
	}
	if e := imp.Examples[0]; e.UserID != bob || e.Cross || !e.MarginBalance.Equal(d("50")) || !e.MaintenanceBefore.Equal(d("120")) ||
		!e.MaintenanceAfter.Equal(d("117.8")) {
		t.Fatalf("example %+v", e)
	}
	// At 58,700 the loss of 650 is 50 beyond his margin: the fund's.
	if imp, err := r.svc.PriceImpact(ctx, perp.Symbol, d("58700")); err != nil || imp.Liquidated != 1 || !imp.InsuranceCost.Equal(d("50")) {
		t.Fatalf("beyond the margin %+v %v", imp, err)
	}
	// Up, Alice's cross short of 10,000 holds and Bob gains.
	if imp, err := r.svc.PriceImpact(ctx, perp.Symbol, d("62000")); err != nil || imp.Liquidated != 0 {
		t.Fatalf("up %+v %v", imp, err)
	}
	if list, err := r.svc.RiskPositions(ctx); err != nil || len(list) != 0 {
		t.Fatalf("nothing changed: %+v %v", list, err)
	}
	_ = alice
	if _, err := r.svc.PriceImpact(ctx, perp.Symbol, d("0")); apperr.From(err).Kind != apperr.KindInvalid {
		t.Fatalf("a zero target: %v", err)
	}
	if _, err := r.svc.PriceImpact(ctx, "NOPE-USDT-PERP", d("60000")); apperr.From(err).Kind != apperr.KindNotFound {
		t.Fatalf("an unknown contract: %v", err)
	}
}

// A cross account goes whole at the target: Alice's equity of 685 against
// her 50x short of 0.5 at 60,000.
func TestATargetPriceLiquidatesACrossAccountWhole(t *testing.T) {
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
	// At 61,100: 135 of equity against 122.20, alive; at 61,500: 65 short
	// of zero, liquidated and the fund's.
	if imp, err := r.svc.PriceImpact(ctx, perp.Symbol, d("61100")); err != nil || imp.Liquidated != 0 {
		t.Fatalf("61,100 %+v %v", imp, err)
	}
	imp, err := r.svc.PriceImpact(ctx, perp.Symbol, d("61500"))
	if err != nil {
		t.Fatal(err)
	}
	if imp.Liquidated != 1 || imp.Accounts != 1 || !imp.Notional.Equal(d("30750")) || !imp.InsuranceCost.Equal(d("65")) || len(imp.Examples) != 1 {
		t.Fatalf("impact %+v", imp)
	}
	if e := imp.Examples[0]; e.UserID != alice || !e.Cross || !e.MarginBalance.Equal(d("-65")) || !e.MaintenanceAfter.Equal(d("123")) {
		t.Fatalf("example %+v", e)
	}
	// Without a fresh mark nothing is measured.
	r.book.Set(perp.Symbol, d("60000"), time.Now().Add(-10*time.Minute))
	if imp, err := r.svc.PriceImpact(ctx, perp.Symbol, d("61500")); err != nil || imp.Unmeasured != 2 || imp.Liquidated != 0 {
		t.Fatalf("stale mark %+v %v", imp, err)
	}
}
