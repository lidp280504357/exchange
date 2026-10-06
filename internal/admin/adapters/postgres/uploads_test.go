package postgres_test

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// TestAppUploads checks app_uploads (admin 00017, the App download page's
// uploads): parts received once each and in order, a completion's hold,
// the open count and the expired ones, and what the table refuses.
func TestAppUploads(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), log); err != nil {
		t.Fatal(err)
	}
	uploads := postgres.NewUploads(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	sha := strings.Repeat("ab", 32)
	u, err := domain.NewAppUpload(uuid.Must(uuid.NewV7()).String(), domain.AppAndroid, domain.AppKindApp, "Astras.apk", 3*domain.AppPartSize-5, sha,
		"boss@example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := uploads.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := uploads.Get(ctx, u.ID)
	if err != nil || got == nil || got.Name != "Astras.apk" || got.Size != u.Size || got.PartSize != domain.AppPartSize || len(got.Received) != 0 ||
		!got.ExpiresAt.Equal(now.Add(24*time.Hour)) || got.StartedBy != "boss@example.com" {
		t.Fatalf("read %+v %v", got, err)
	}
	for _, n := range []int{3, 1, 3} {
		if got, err = uploads.Received(ctx, u.ID, n); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(got.Received, []int{1, 3}) || !slices.Equal(got.Missing(), []int{2}) {
		t.Fatalf("received %v", got.Received)
	}
	if none, err := uploads.Received(ctx, uuid.NewString(), 1); err != nil || none != nil {
		t.Fatalf("an unknown upload: %+v %v", none, err)
	}

	// A completion holds it; another waits until it lets go or its hold
	// runs out.
	if ok, err := uploads.Claim(ctx, u.ID, now, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("claimed: %v %v", ok, err)
	}
	if ok, _ := uploads.Claim(ctx, u.ID, now.Add(time.Second), now.Add(time.Minute)); ok {
		t.Fatal("claimed twice")
	}
	if busy, err := uploads.Busy(ctx, u.ID, now); err != nil || !busy {
		t.Fatalf("busy: %v %v", busy, err)
	}
	if ok, _ := uploads.Claim(ctx, u.ID, now.Add(2*time.Minute), now.Add(3*time.Minute)); !ok {
		t.Fatal("a hold that ran out still holds")
	}
	if err := uploads.Release(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if busy, _ := uploads.Busy(ctx, u.ID, now); busy {
		t.Fatal("released, still busy")
	}

	if n, err := uploads.Open(ctx, now); err != nil || n < 1 {
		t.Fatalf("open %d %v", n, err)
	}
	if list, err := uploads.Expired(ctx, now.Add(25*time.Hour)); err != nil || !slices.ContainsFunc(list, func(x domain.AppUpload) bool { return x.ID == u.ID }) {
		t.Fatalf("expired %+v %v", list, err)
	}
	if err := uploads.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if gone, _ := uploads.Get(ctx, u.ID); gone != nil {
		t.Fatalf("deleted: %+v", gone)
	}

	// The table refuses what the domain would not make.
	bad := u
	bad.ID, bad.Kind = uuid.NewString(), domain.AppKindMobileconfig
	if err := uploads.Create(ctx, bad); err == nil {
		t.Fatal("a configuration profile on Android stored")
	}
	bad.Platform, bad.Size = domain.AppIOS, domain.AppMaxMobileconfig+1
	if err := uploads.Create(ctx, bad); err == nil {
		t.Fatal("a configuration profile over 1 MiB stored")
	}
	bad.Kind, bad.Size, bad.SHA256 = domain.AppKindApp, 10, "AB"
	if err := uploads.Create(ctx, bad); err == nil {
		t.Fatal("a bad SHA-256 stored")
	}
}
