package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	"github.com/lidp280504357/exchange/internal/auth/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/auth/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func setup(t *testing.T) (*postgres.Store, *pg.DB) {
	t.Helper()
	db := testenv.Postgres(t)
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(context.Background(), db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(context.Background(), db, migrations.Auth(), log); err != nil {
		t.Fatal(err)
	}
	return postgres.NewStore(db, event.NewFactory("auth-service", "test")), db
}

func TestChallengesAndTickets(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	c := &domain.Challenge{
		ID: uuid.NewString(), Scene: domain.SceneRegister, Channel: domain.ChannelEmail, Target: "a@example.com",
		DeviceID: "device-0001", CodeHash: []byte{1, 2, 3}, Status: domain.ChallengePending,
		ExpiresAt: now.Add(domain.CodeTTL), CreatedAt: now,
	}
	err := store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Challenges().Create(ctx, c); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.OtpRequested{ChallengeId: c.ID}, "user", c.ID)
	})
	if err != nil {
		t.Fatal(err)
	}

	var loaded *domain.Challenge
	err = store.Tx(ctx, func(r ports.Repos) error {
		var err error
		loaded, err = r.Challenges().GetForUpdate(ctx, c.ID)
		if err != nil {
			return err
		}
		loaded.Attempts, loaded.Status, loaded.VerifiedAt = 1, domain.ChallengeVerified, now
		if err := r.Challenges().Update(ctx, loaded); err != nil {
			return err
		}
		return r.Tickets().Create(ctx, domain.Ticket{Hash: []byte("h"), ChallengeID: c.ID, Scene: c.Scene, DeviceID: c.DeviceID, ExpiresAt: now.Add(time.Minute)})
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Read().Challenges().GetForUpdate(ctx, c.ID)
	if err != nil || again.Status != domain.ChallengeVerified || again.Attempts != 1 || !again.VerifiedAt.Equal(now) || again.UserID != "" {
		t.Fatalf("round trip: %+v %v", again, err)
	}
	if missing, err := store.Read().Challenges().GetForUpdate(ctx, uuid.NewString()); err != nil || missing != nil {
		t.Fatalf("unknown challenge: %v %v", missing, err)
	}

	tickets := store.Read().Tickets()
	if id, _ := tickets.Consume(ctx, []byte("h"), domain.SceneLogin, c.DeviceID, now); id != "" {
		t.Fatal("wrong scene must not redeem")
	}
	if id, _ := tickets.Consume(ctx, []byte("h"), c.Scene, "device-9999", now); id != "" {
		t.Fatal("wrong device must not redeem")
	}
	if id, _ := tickets.Consume(ctx, []byte("h"), c.Scene, c.DeviceID, now.Add(2*time.Minute)); id != "" {
		t.Fatal("expired ticket must not redeem")
	}
	if id, err := tickets.Consume(ctx, []byte("h"), c.Scene, c.DeviceID, now); err != nil || id != c.ID {
		t.Fatalf("redeem: %q %v", id, err)
	}
	if id, _ := tickets.Consume(ctx, []byte("h"), c.Scene, c.DeviceID, now); id != "" {
		t.Fatal("a ticket redeems once")
	}

	var queued int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE topic = 'auth.events'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("event queued with the challenge: %d %v", queued, err)
	}
}

func TestIdentities(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	for kind, value := range map[string]string{"EMAIL": "a@example.com", "PHONE": "+8613812341234"} {
		if _, err := db.Exec(ctx, `INSERT INTO identities (id, user_id, kind, value, verified_at) VALUES ($1, $2, $3, $4, now())`,
			uuid.NewString(), user, kind, value); err != nil {
			t.Fatal(err)
		}
	}
	id, err := store.Read().Identities().Find(ctx, "EMAIL", "a@example.com")
	if err != nil || id == nil || id.UserID != user {
		t.Fatalf("find: %+v %v", id, err)
	}
	if none, err := store.Read().Identities().Find(ctx, "EMAIL", "b@example.com"); err != nil || none != nil {
		t.Fatalf("absent: %+v %v", none, err)
	}
	all, err := store.Read().Identities().ByUser(ctx, user)
	if err != nil || len(all) != 2 || all[0].Kind != "EMAIL" || all[1].Kind != "PHONE" {
		t.Fatalf("by user: %+v %v", all, err)
	}
}
