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

	big := domain.Caps{Level: d("500000000"), Symbol: d("500000000"), Total: d("500000000"), Contract: d("500000000"), Safety: d("1000"), ContractLeverage: d("10")}
	if _, err := store.Change(ctx, domain.CapsChange{Caps: big, Version: 2, Actor: "a", Reason: "r"}, at); apperr.From(err).Code != "HOUSE_CAPS_VERSION" {
		t.Fatalf("a stale version: %v", err)
	}
	s, err = store.Change(ctx, domain.CapsChange{Caps: big, Version: 1, Actor: "admin:a", Approver: "admin:b", ApprovalID: "ap1", Reason: "the user's decision"},
		at.Add(time.Minute))
	if err != nil || s.Version != 2 || !s.Caps.Level.Equal(d("500000000")) || s.UpdatedBy != "admin:a" {
		t.Fatalf("changed %+v %v", s, err)
	}
	if got, ok, err := store.Caps(ctx); err != nil || !ok || got.Version != 2 || !got.Caps.Total.Equal(d("500000000")) {
		t.Fatalf("read %+v %v %v", got, ok, err)
	}
	list, err := store.Changes(ctx, 10)
	if err != nil || len(list) != 2 || list[0].Version != 2 || list[0].Previous == nil || !list[0].Previous.Level.Equal(d("20000")) ||
		list[0].Approver != "admin:b" || list[0].ApprovalID != "ap1" || list[1].Previous != nil || list[1].Actor != "environment" {
		t.Fatalf("changes %+v %v", list, err)
	}
}
