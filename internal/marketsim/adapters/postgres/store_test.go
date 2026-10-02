package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/marketsim/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func TestBotsSettingsAndState(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, db, migrations.MarketSim(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db)

	// Bots: once per user, a label belongs to one.
	a, b := uuid.NewString(), uuid.NewString()
	if err := store.AddBot(ctx, ports.Bot{UserID: a, Role: domain.RoleMaker, Label: "bot-02", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddBot(ctx, ports.Bot{UserID: a, Role: domain.RoleTaker, Label: "bot-09", Enabled: true}); err != nil {
		t.Fatalf("the same user again: %v", err)
	}
	if err := store.AddBot(ctx, ports.Bot{UserID: b, Role: domain.RoleTaker, Label: "bot-02", Enabled: true}); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("a label in use: %v", err)
	}
	if err := store.AddBot(ctx, ports.Bot{UserID: b, Role: domain.RoleTaker, Label: "bot-01", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	bots, err := store.Bots(ctx)
	if err != nil || len(bots) != 2 || bots[0].Label != "bot-01" || bots[1].UserID != a || bots[1].Role != domain.RoleMaker {
		t.Fatalf("bots %+v %v", bots, err)
	}

	// Settings: none, then versions.
	if _, _, ok, err := store.Settings(ctx); ok || err != nil {
		t.Fatalf("settings before any: %v %v", ok, err)
	}
	p := domain.DefaultParams()
	if v, err := store.SaveSettings(ctx, p, "market-sim"); err != nil || v != 1 {
		t.Fatalf("first settings: %d %v", v, err)
	}
	p.Levels = 5
	if v, err := store.SaveSettings(ctx, p, "ops"); err != nil || v != 2 {
		t.Fatalf("second settings: %d %v", v, err)
	}
	got, v, ok, err := store.Settings(ctx)
	if err != nil || !ok || v != 2 || got.Levels != 5 || got.Spread != p.Spread {
		t.Fatalf("settings %+v v%d %v %v", got, v, ok, err)
	}

	// State: the random source and the minute come back as saved.
	m := domain.NewModel(domain.DefaultParams(), domain.State{}, 9)
	at := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	m.Step(at, 60000, 3000)
	m.Step(at.Add(time.Second), 60100, 3001)
	if err := store.SaveState(ctx, m.Snapshot()); err != nil {
		t.Fatal(err)
	}
	st, ok, err := store.State(ctx)
	if err != nil || !ok || st.P != m.State.P || len(st.Minute) != 2 || len(st.RNG) == 0 || !st.At.Equal(m.State.At) {
		t.Fatalf("state %+v %v %v", st, ok, err)
	}
	next := domain.NewModel(domain.DefaultParams(), st, 1)
	want, _ := m.Step(at.Add(2*time.Second), 60100, 3001)
	if got, _ := next.Step(at.Add(2*time.Second), 60100, 3001); got != want {
		t.Fatalf("restored: %v, want %v", got, want)
	}
}
