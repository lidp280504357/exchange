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
	if cs, err := users.Consents(ctx, u.ID); err != nil || len(cs) != 2 || cs[0].Version != "v1" || cs[0].AcceptedAt.IsZero() {
		t.Fatalf("consents read back: %+v %v", cs, err)
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

func TestFavoritesRoundTrip(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	u, err := domain.NewUser(uuid.NewString(), "sg", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read().Users().Create(ctx, u, nil); err != nil {
		t.Fatal(err)
	}
	fav := store.Read().Favorites()
	if list, at, err := fav.Get(ctx, u.ID); err != nil || len(list) != 0 || !at.IsZero() {
		t.Fatalf("empty: %v %v %v", list, at, err)
	}
	if _, err := fav.Set(ctx, u.ID, []string{"ETH-USDT", "BTC-USDT"}); err != nil {
		t.Fatal(err)
	}
	at, err := fav.Set(ctx, u.ID, []string{"BTC-USDT-PERP", "ETH-USDT"})
	if err != nil {
		t.Fatal(err)
	}
	list, got, err := fav.Get(ctx, u.ID)
	if err != nil || len(list) != 2 || list[0] != "BTC-USDT-PERP" || !got.Equal(at) {
		t.Fatalf("stored %v at %v %v", list, got, err)
	}
	if _, err := fav.Set(ctx, uuid.NewString(), []string{"BTC-USDT"}); err == nil {
		t.Fatal("favorites of an unknown user")
	}
}

func TestListAndCountUsers(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	users := store.Read().Users()
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)
	var ids []string
	for i := range 3 {
		u, err := domain.NewUser(uuid.NewString(), "sg", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := users.Create(ctx, u, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE users SET created_at = $2 WHERE id = $1`, u.ID, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	page, err := users.List(ctx, ports.UserFilter{Region: "SG", CreatedFrom: base, Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != ids[2] || page[1].ID != ids[1] {
		t.Fatalf("first page %+v %v", page, err)
	}
	rest, err := users.List(ctx, ports.UserFilter{Region: "SG", CreatedFrom: base, AfterTime: page[1].CreatedAt, AfterID: page[1].ID, Limit: 2})
	if err != nil || len(rest) != 1 || rest[0].ID != ids[0] {
		t.Fatalf("second page %+v %v", rest, err)
	}
	if none, _ := users.List(ctx, ports.UserFilter{Status: "FROZEN", CreatedFrom: base, Limit: 10}); len(none) != 0 {
		t.Fatalf("frozen %+v", none)
	}
	st, err := users.Stats(ctx, base.Add(90*time.Minute), 3)
	if err != nil || st.Total < 3 || st.CreatedSince < 1 {
		t.Fatalf("stats %+v %v", st, err)
	}
	var days int64
	for _, n := range st.Days {
		days += n
	}
	if days < 3 {
		t.Fatalf("per day %+v", st.Days)
	}
}
