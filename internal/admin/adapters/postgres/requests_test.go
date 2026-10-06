package postgres_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// TestRequestsOfOneAdminOneAtATime checks LockRequests and PendingOf
// (review BH ①②): while a transaction holds an ADMIN's welcome raises,
// another for the same ADMIN waits for it and then sees what it inserted;
// another ADMIN's are not held.
func TestRequestsOfOneAdminOneAtATime(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("admin-test", "t"))
	now := time.Now().UTC().Truncate(time.Microsecond)
	admin := func(name string) domain.Admin {
		a, err := domain.NewAdmin(uuid.Must(uuid.NewV7()).String(), name+"-"+uuid.NewString()[:8]+"@example.com", "Test", domain.RoleAdmin, now)
		if err != nil {
			t.Fatal(err)
		}
		a.PasswordHash, a.TOTPSealed = "h", []byte{1}
		if err := store.Read().Admins().Insert(ctx, a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	boss, other := admin("boss"), admin("other")
	raise := func(by string) domain.Approval {
		return domain.Approval{
			ID: uuid.Must(uuid.NewV7()).String(), Kind: domain.KindWelcomeCredit, Status: domain.ApprovalPending, RequestedBy: by, CreatedAt: now,
			Mode: domain.ModeTwoPerson, Reason: "a little more",
			Payload: map[string]string{ //nolint:gosec // a welcome credits list, not credentials
				"credits":          `[{"asset":"USDT","amount":"1"}]`,
				"expected_version": "1",
			},
		}
	}

	holding, release, first := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	// However the test ends, the first transaction finishes before the
	// pool closes (cleanups run last registered first).
	var released sync.Once
	letGo := func() { released.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	go func() {
		first <- store.Tx(ctx, func(r ports.Repos) error {
			if err := r.Approvals().LockRequests(ctx, domain.KindWelcomeCredit, boss.ID); err != nil {
				return err
			}
			if err := r.Approvals().Insert(ctx, raise(boss.ID)); err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	select {
	case <-holding:
	case err := <-first:
		t.Fatalf("the first transaction: %v", err)
	}

	// Another ADMIN's raises are not held.
	quick, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := store.Tx(quick, func(r ports.Repos) error {
		return r.Approvals().LockRequests(quick, domain.KindWelcomeCredit, other.ID)
	}); err != nil {
		t.Fatalf("another ADMIN's raises: %v", err)
	}

	// The same ADMIN's wait for the first, then see its request.
	second := make(chan int, 1)
	go func() {
		_ = store.Tx(ctx, func(r ports.Repos) error {
			if err := r.Approvals().LockRequests(ctx, domain.KindWelcomeCredit, boss.ID); err != nil {
				second <- -1
				return err
			}
			pending, err := r.Approvals().PendingOf(ctx, domain.KindWelcomeCredit, boss.ID)
			second <- len(pending)
			return err
		})
	}()
	select {
	case n := <-second:
		t.Fatalf("the second went on while the first held the lock (saw %d)", n)
	case <-time.After(500 * time.Millisecond):
	}
	letGo()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if n := <-second; n != 1 {
		t.Fatalf("after the first, the second sees %d pending", n)
	}
	if pending, err := store.Read().Approvals().PendingOf(ctx, domain.KindWelcomeCredit, other.ID); err != nil || len(pending) != 0 {
		t.Fatalf("the other ADMIN's %v %v", pending, err)
	}
}

// TestPendingOfKindSeesEveryRequester checks PendingOfKind (margin E5):
// the pending requests of a kind, whoever asked them, oldest first; a
// decided one and another kind's are not among them.
func TestPendingOfKindSeesEveryRequester(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("admin-test", "t"))
	now := time.Now().UTC().Truncate(time.Microsecond)
	ask := func(kind, status string, at time.Time) domain.Approval {
		a, err := domain.NewAdmin(uuid.Must(uuid.NewV7()).String(), "margin-"+uuid.NewString()[:8]+"@example.com", "Test", domain.RoleAdmin, now)
		if err != nil {
			t.Fatal(err)
		}
		a.PasswordHash, a.TOTPSealed = "h", []byte{1}
		if err := store.Read().Admins().Insert(ctx, a); err != nil {
			t.Fatal(err)
		}
		r := domain.Approval{
			ID: uuid.Must(uuid.NewV7()).String(), Kind: kind, Status: status, RequestedBy: a.ID, CreatedAt: at, Mode: domain.ModeTwoPerson,
			Escalation: domain.EscalationMarginRisk, Reason: "margin terms", Payload: map[string]string{"target": "asset:USDT"},
		}
		if status != domain.ApprovalPending {
			r.DecidedBy, r.DecidedAt = a.ID, at // withdrawn by its requester
		}
		if err := store.Read().Approvals().Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	later := ask(domain.KindMarginParams, domain.ApprovalPending, now.Add(time.Second))
	earlier := ask(domain.KindMarginParams, domain.ApprovalPending, now)
	decided := ask(domain.KindMarginParams, domain.ApprovalRejected, now)
	other := ask(domain.KindMarginLiquidate, domain.ApprovalPending, now)
	pending, err := store.Read().Approvals().PendingOfKind(ctx, domain.KindMarginParams)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range pending {
		switch a.ID {
		case earlier.ID, later.ID:
			ids = append(ids, a.ID)
		case decided.ID, other.ID:
			t.Fatalf("PendingOfKind returned %s (%s %s)", a.ID, a.Kind, a.Status)
		}
	}
	if len(ids) != 2 || ids[0] != earlier.ID || ids[1] != later.ID {
		t.Fatalf("pending of the kind, oldest first: %v, want %s then %s", ids, earlier.ID, later.ID)
	}
}
