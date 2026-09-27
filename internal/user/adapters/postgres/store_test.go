package postgres_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/internal/user/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/user/domain"
	"github.com/lidp280504357/exchange/migrations"
)

func TestCreateIsIdempotent(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, db, migrations.Users(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db)

	u, err := domain.NewUser(uuid.NewString(), "sg", "", "")
	if err != nil {
		t.Fatal(err)
	}
	consents := []domain.Consent{{Document: domain.DocumentTerms, Version: "v1"}, {Document: domain.DocumentRiskDisclosure, Version: "v1"}}
	created, err := store.Create(ctx, u, consents)
	if err != nil || created.ID != u.ID || created.Status != domain.StatusActive || created.Region != "SG" ||
		created.Language != domain.DefaultLanguage || created.Timezone != domain.DefaultTimezone || created.Version != 1 {
		t.Fatalf("create: %+v %v", created, err)
	}
	// A retry with other values keeps the first profile.
	u.Region = "JP"
	again, err := store.Create(ctx, u, consents)
	if err != nil || again.Region != "SG" || !again.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("retry: %+v %v", again, err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM consents WHERE user_id = $1`, u.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("consents: %d %v", n, err)
	}

	got, err := store.Get(ctx, u.ID)
	if err != nil || got.ID != u.ID {
		t.Fatalf("get: %+v %v", got, err)
	}
	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		if _, err := store.Get(ctx, id); err != domain.ErrUserNotFound { //nolint:errorlint // sentinel returned as is
			t.Fatalf("get %s: %v", id, err)
		}
	}
}
