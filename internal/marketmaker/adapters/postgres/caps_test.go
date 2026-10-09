package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketmaker/adapters/postgres"
	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// HOUSE's runtime caps (review C45): seeded once, changed on the stored
// version only, every change kept with what it replaced.
func TestHouseCaps(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, db, migrations.MarketMaker(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db)
	at := time.Date(2026, 10, 7, 5, 0, 0, 0, time.UTC)
	if _, ok, err := store.Caps(ctx); err != nil || ok {
		t.Fatalf("none stored yet: %v %v", ok, err)
	}
	env := domain.Caps{Level: d("20000"), Symbol: d("2000000"), Total: d("20000000"), Contract: d("5000000"), Safety: d("1000"), ContractLeverage: d("10")}
	s, err := store.Seed(ctx, env, "environment", at)
	if err != nil || s.Version != 1 || !s.Caps.Contract.Equal(d("5000000")) || s.UpdatedBy != "environment" {
		t.Fatalf("seeded %+v %v", s, err)
	}
	other := env
	other.Level = d("1")
	if s, err := store.Seed(ctx, other, "environment", at.Add(time.Hour)); err != nil || s.Version != 1 || !s.Caps.Level.Equal(d("20000")) {
		t.Fatalf("seeded again %+v %v", s, err)
	}

	ten := domain.CapsPatch{Level: ptr("200000"), Symbol: ptr("20000000"), Total: ptr("200000000"), Contract: ptr("50000000")}
	if _, err := store.Change(ctx, domain.CapsChange{Patch: ten, Version: 2, Actor: "a", Reason: "r"}, at); apperr.From(err).Code != "HOUSE_CAPS_VERSION" {
		t.Fatalf("a stale version: %v", err)
	}
	// Applied to the locked row (review FL, C47): more than ten times is
	// refused there, and nothing changes.
	if _, err := store.Change(ctx, domain.CapsChange{Patch: domain.CapsPatch{Level: ptr("500000000")}, Version: 1, Actor: "a", Reason: "r"}, at); apperr.From(err).Code != "HOUSE_CAPS_STEP" {
		t.Fatalf("a step too large: %v", err)
	}
	s, err = store.Change(ctx, domain.CapsChange{
		Patch: ten, Version: 1, Actor: "admin:a", Approver: "admin:b", ApprovalID: "ap1", Reason: "the user's decision", SignedBy: "admin",
	}, at.Add(time.Minute))
	if err != nil || s.Version != 2 || !s.Caps.Level.Equal(d("200000")) || !s.Caps.Safety.Equal(d("1000")) || s.UpdatedBy != "admin:a" {
		t.Fatalf("changed %+v %v", s, err)
	}
	// A second change of another cap keeps the first's.
	s, err = store.Change(ctx, domain.CapsChange{Patch: domain.CapsPatch{Safety: ptr("2000")}, Version: 2, Actor: "ops:x", Reason: "r", SignedBy: "ops"},
		at.Add(2*time.Minute))
	if err != nil || s.Version != 3 || !s.Caps.Level.Equal(d("200000")) || !s.Caps.Safety.Equal(d("2000")) {
		t.Fatalf("changed again %+v %v", s, err)
	}
	if got, ok, err := store.Caps(ctx); err != nil || !ok || got.Version != 3 || !got.Caps.Total.Equal(d("200000000")) {
		t.Fatalf("read %+v %v %v", got, ok, err)
	}
	// The table keeps to the bounds too (review FP, C48).
	for _, set := range []string{"contract_leverage = 0", "contract_leverage = 1001", "symbol = 0", "total = 2e15", "level = -1"} {
		if _, err := db.Exec(ctx, "UPDATE house_caps SET "+set); err == nil {
			t.Errorf("the table took %s", set)
		}
	}
	list, err := store.Changes(ctx, 10)
	if err != nil || len(list) != 3 || list[1].Version != 2 || list[1].Previous == nil || !list[1].Previous.Level.Equal(d("20000")) ||
		list[1].Approver != "admin:b" || list[1].ApprovalID != "ap1" || list[1].SignedBy != "admin" || list[0].SignedBy != "ops" ||
		!list[0].Previous.Safety.Equal(d("1000")) || list[2].Previous != nil || list[2].Actor != "environment" || list[2].SignedBy != "" {
		t.Fatalf("changes %+v %v", list, err)
	}
}

func ptr(s string) *decimal.Decimal {
	v := d(s)
	return &v
}
