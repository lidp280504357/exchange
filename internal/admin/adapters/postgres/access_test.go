package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/password"
	"github.com/skill/exchange/internal/platform/secretbox"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/internal/platform/totp"
	"github.com/skill/exchange/migrations"
)

// TestConsoleAccess: the console's access switches on Postgres (N1) -
// none until stored, stored once by Init, changed by Put under a lock -
// and an administrator's bound authenticator kept and cleared.
func TestConsoleAccess(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("admin-test", "t"))
	r := store.Read()
	if a, err := r.Access().Get(ctx); err != nil || a != nil {
		t.Fatalf("before any: %+v %v", a, err)
	}
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	first := domain.ConsoleAccess{RequireTOTP: false, UpdatedBy: "migration:admin.login_without_totp", UpdatedAt: at}
	if stored, err := r.Access().Init(ctx, first); err != nil || !stored {
		t.Fatalf("init %t %v", stored, err)
	}
	if stored, err := r.Access().Init(ctx, domain.ConsoleAccess{RequireTOTP: true, UpdatedBy: "another", UpdatedAt: at}); err != nil || stored {
		t.Fatalf("init again %t %v", stored, err)
	}
	if a, err := r.Access().Get(ctx); err != nil || a == nil || a.RequireTOTP || a.UpdatedBy != first.UpdatedBy || !a.UpdatedAt.Equal(at) {
		t.Fatalf("stored once %+v %v", a, err)
	}
	err := store.Tx(ctx, func(tx ports.Repos) error {
		a, err := tx.Access().GetForUpdate(ctx)
		if err != nil || a == nil {
			t.Fatalf("locked %+v %v", a, err)
		}
		return tx.Access().Put(ctx, domain.ConsoleAccess{RequireTOTP: true, UpdatedBy: "boss@example.com", UpdatedAt: at.Add(time.Minute)})
	})
	if err != nil {
		t.Fatal(err)
	}
	if a, err := r.Access().Get(ctx); err != nil || !a.RequireTOTP || a.UpdatedBy != "boss@example.com" || !a.UpdatedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("changed %+v %v", a, err)
	}

	// An administrator's authenticator, bound and then not.
	box, err := secretbox.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	hasher := password.NewHasher(1, password.Cost{MemoryKiB: 64, Iterations: 1})
	a, err := application.NewAdmin(ctx, store, hasher, box, "boss@example.com", "Boss", domain.RoleAdmin, "a long password", totp.NewSecret(),
		"test", false, at)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Admins().Get(ctx, a.ID); err != nil || got.TOTPBound() {
		t.Fatalf("a new one is not bound %+v %v", got, err)
	}
	a.TOTPConfirmedAt = at.Add(2 * time.Minute)
	if err := r.Admins().Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Admins().Get(ctx, a.ID); err != nil || !got.TOTPConfirmedAt.Equal(a.TOTPConfirmedAt) {
		t.Fatalf("bound %+v %v", got, err)
	}
	a.TOTPConfirmedAt = time.Time{}
	if err := r.Admins().Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Admins().Get(ctx, a.ID); err != nil || got.TOTPBound() {
		t.Fatalf("cleared %+v %v", got, err)
	}
}
