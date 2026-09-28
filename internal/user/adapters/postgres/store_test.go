package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/internal/user/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/user/domain"
	"github.com/lidp280504357/exchange/internal/user/ports"
	"github.com/lidp280504357/exchange/migrations"
)

func setup(t *testing.T) (*postgres.Store, *pg.DB) {
	t.Helper()
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Users(), log); err != nil {
		t.Fatal(err)
	}
	return postgres.NewStore(db, event.NewFactory("user-service", "test")), db
}

func TestCreateIsIdempotent(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	u, err := domain.NewUser(uuid.NewString(), "sg", "", "")
	if err != nil {
		t.Fatal(err)
	}
	consents := []domain.Consent{{Document: domain.DocumentTerms, Version: "v1"}, {Document: domain.DocumentRiskDisclosure, Version: "v1"}}
	users := store.Read().Users()
	if created, err := users.Create(ctx, u, consents); err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	u.Region = "JP"
	if created, err := users.Create(ctx, u, consents); err != nil || created {
		t.Fatalf("retry: %v %v", created, err)
	}
	got, err := users.Get(ctx, u.ID)
	if err != nil || got.Region != "SG" || got.Status != domain.StatusActive || got.Language != domain.DefaultLanguage ||
		got.Timezone != domain.DefaultTimezone || got.Version != 1 {
		t.Fatalf("get: %+v %v", got, err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM consents WHERE user_id = $1`, u.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("consents: %d %v", n, err)
	}
	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		if _, err := users.Get(ctx, id); err != domain.ErrUserNotFound { //nolint:errorlint // sentinel returned as is
			t.Fatalf("get %s: %v", id, err)
		}
	}
}

func TestUpdateStatusHistoryAndEvents(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	u, _ := domain.NewUser(uuid.NewString(), "SG", "en", "Asia/Singapore")
	if _, err := store.Read().Users().Create(ctx, u, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	err := store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Users().GetForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		cur.Status, cur.AntiPhishingCode = domain.StatusFrozen, "Blue42"
		updated, err := r.Users().Update(ctx, cur)
		if err != nil {
			return err
		}
		if updated.Version != 2 || updated.Status != domain.StatusFrozen || updated.AntiPhishingCode != "Blue42" {
			t.Fatalf("updated: %+v", updated)
		}
		if err := r.Users().AddStatusChange(ctx, domain.StatusChange{
			UserID: u.ID, From: domain.StatusActive, To: domain.StatusFrozen, Reason: "SUSPICIOUS_LOGIN", Actor: "cli:ops", At: now,
		}); err != nil {
			return err
		}
		return r.Emit(ctx, event.TopicUser, &userv1.UserStatusChanged{UserId: u.ID, ToStatus: domain.StatusFrozen}, "user", u.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.Read().Users().StatusHistory(ctx, u.ID, 10)
	if err != nil || len(history) != 1 || history[0].To != domain.StatusFrozen || history[0].Reason != "SUSPICIOUS_LOGIN" || !history[0].At.Equal(now) {
		t.Fatalf("history: %+v %v", history, err)
	}
	var topic string
	if err := db.QueryRow(ctx, `SELECT topic FROM outbox ORDER BY id DESC LIMIT 1`).Scan(&topic); err != nil || topic != event.TopicUser {
		t.Fatalf("outbox: %s %v", topic, err)
	}
}
