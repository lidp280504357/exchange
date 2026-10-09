package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/internal/user/adapters/postgres"
	"github.com/skill/exchange/internal/user/domain"
	"github.com/skill/exchange/internal/user/ports"
	"github.com/skill/exchange/migrations"
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

// A keyword keeps the usernames that contain it, whatever the case (an
// underscore or a percent sign is no wildcard), and the accounts given by
// ID; a username is found whatever its case (B167).
func TestListByKeywordAndFindUsername(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	users := store.Read().Users()
	var ids []string
	for _, name := range []string{"user_8c6fbf82", "Satoshi_N", "pacfan", "userx8c6"} {
		u, err := domain.NewUser(uuid.NewString(), "SG", "", "")
		if err != nil {
			t.Fatal(err)
		}
		u.Username = name
		if _, err := users.Create(ctx, u, nil); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	names := func(f ports.UserFilter) []string {
		t.Helper()
		f.Limit = 10
		list, err := users.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, u := range list {
			out = append(out, u.Username)
		}
		slices.Sort(out)
		return out
	}
	if got := names(ports.UserFilter{Q: "SATOSHI"}); !slices.Equal(got, []string{"Satoshi_N"}) {
		t.Fatalf("by username: %v", got)
	}
	if got := names(ports.UserFilter{Q: "user_"}); !slices.Equal(got, []string{"user_8c6fbf82"}) {
		t.Fatalf("an underscore is no wildcard: %v", got)
	}
	if got := names(ports.UserFilter{Q: "%"}); len(got) != 0 {
		t.Fatalf("a percent sign is no wildcard: %v", got)
	}
	if got := names(ports.UserFilter{Q: "pac", IDs: []string{ids[1], "not-a-uuid"}}); !slices.Equal(got, []string{"Satoshi_N", "pacfan"}) {
		t.Fatalf("with the accounts matched elsewhere: %v", got)
	}
	if got := names(ports.UserFilter{}); len(got) != 4 {
		t.Fatalf("no keyword: %v", got)
	}
	if id, err := users.FindUsername(ctx, "SATOSHI_n"); err != nil || id != ids[1] {
		t.Fatalf("find: %q %v", id, err)
	}
	if _, err := users.FindUsername(ctx, "nobody_here"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

// Kinds (L0): HUMAN until set; the store sets one, keeps the change,
// lists the accounts of some kinds, filters the console's list and counts
// each kind; an update of the profile leaves the kind alone.
func TestAccountKindsStore(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	users := store.Read().Users()
	var ids []string
	for range 3 {
		u, err := domain.NewUser(uuid.NewString(), "SG", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := users.Create(ctx, u, nil); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	if got, _ := users.Get(ctx, ids[0]); got.Kind != domain.KindHuman {
		t.Fatalf("new: %q", got.Kind)
	}
	if err := users.SetKind(ctx, ids[0], domain.KindBot); err != nil {
		t.Fatal(err)
	}
	if err := users.SetKind(ctx, uuid.NewString(), domain.KindBot); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := users.SetKind(ctx, ids[1], domain.KindSystem); err != nil {
		t.Fatal(err)
	}
	if err := users.AddKindChange(ctx, domain.KindChange{UserID: ids[0], From: "HUMAN", To: "BOT", Actor: "astra.sh", Reason: "bot", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM user_kind_changes WHERE user_id = $1 AND to_kind = 'BOT'`, ids[0]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("history: %d %v", n, err)
	}
	got, err := users.IDsOfKinds(ctx, []string{domain.KindBot, domain.KindSystem})
	if want := slices.Sorted(slices.Values([]string{ids[0], ids[1]})); err != nil || !slices.Equal(got, want) {
		t.Fatalf("of kinds: %v %v", got, err)
	}
	if none, err := users.IDsOfKinds(ctx, []string{domain.KindTest}); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("none: %v %v", none, err)
	}
	list, err := users.List(ctx, ports.UserFilter{Kinds: []string{domain.KindHuman}, Limit: 10})
	if err != nil || len(list) != 1 || list[0].ID != ids[2] {
		t.Fatalf("humans: %+v %v", list, err)
	}
	u, _ := users.GetForUpdate(ctx, ids[0])
	u.Language = "en"
	if after, err := users.Update(ctx, u); err != nil || after.Kind != domain.KindBot {
		t.Fatalf("an update keeps the kind: %+v %v", after, err)
	}
	st, err := users.Stats(ctx, time.Now().Add(-time.Hour), 0)
	if err != nil || st.Total != 3 || st.ByKind[domain.KindHuman].Total != 1 || st.ByKind[domain.KindBot].CreatedSince != 1 || st.ByKind[domain.KindSystem].Total != 1 {
		t.Fatalf("stats %+v %v", st, err)
	}
	// Purged (L4): out of the lists and counts unless asked, in its kind's IDs.
	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := users.SetPurged(ctx, ids[0], at); err != nil {
		t.Fatal(err)
	}
	if got, _ := users.Get(ctx, ids[0]); !got.PurgedAt.Equal(at) {
		t.Fatalf("purged at %v", got.PurgedAt)
	}
	if list, _ := users.List(ctx, ports.UserFilter{Limit: 10}); len(list) != 2 {
		t.Fatalf("hidden: %d", len(list))
	}
	if list, _ := users.List(ctx, ports.UserFilter{IncludePurged: true, Limit: 10}); len(list) != 3 {
		t.Fatalf("asked for: %d", len(list))
	}
	if st, _ := users.Stats(ctx, time.Now().Add(-time.Hour), 1); st.Total != 2 || st.ByKind[domain.KindBot].Total != 0 {
		t.Fatalf("counts %+v", st)
	}
	if bots, _ := users.IDsOfKinds(ctx, []string{domain.KindBot}); len(bots) != 1 {
		t.Fatalf("its kind's IDs %v", bots)
	}
}

// Usernames are unique whatever the case, a clash is ErrUsernameTaken on
// create and update alike; the change time and the avatar round-trip
// (design 2026-10-07, avatars and usernames).
func TestUsernamesAndAvatars(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	users := store.Read().Users()
	consents := []domain.Consent{{Document: domain.DocumentTerms, Version: "v1"}, {Document: domain.DocumentRiskDisclosure, Version: "v1"}}
	a, _ := domain.NewUser(uuid.NewString(), "SG", "", "")
	a.Username = "Satoshi_N"
	if _, err := users.Create(ctx, a, consents); err != nil {
		t.Fatal(err)
	}
	b, _ := domain.NewUser(uuid.NewString(), "SG", "", "")
	b.Username = "satoshi_n"
	if _, err := users.Create(ctx, b, consents); !errors.Is(err, domain.ErrUsernameTaken) {
		t.Fatalf("a clash on create: %v", err)
	}
	b.Username = "hal_f"
	if _, err := users.Create(ctx, b, consents); err != nil {
		t.Fatal(err)
	}
	got, err := users.Get(ctx, b.ID)
	if err != nil || got.Username != "hal_f" || !got.UsernameChangedAt.IsZero() || got.Avatar != nil {
		t.Fatalf("read %+v %v", got, err)
	}
	got.Username = "SATOSHI_N"
	if _, err := users.Update(ctx, got); !errors.Is(err, domain.ErrUsernameTaken) {
		t.Fatalf("a clash on update: %v", err)
	}
	at := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	got.Username, got.UsernameChangedAt = "hal_finney", at
	got.Avatar = &domain.Avatar{Path: b.ID + "/abc.webp", ThumbPath: b.ID + "/abc_64.webp", UploadedAt: at, Size: 1234, SHA256: "ff"}
	if _, err := users.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, err = users.Get(ctx, b.ID)
	if err != nil || got.Username != "hal_finney" || !got.UsernameChangedAt.Equal(at) || got.Avatar == nil ||
		got.Avatar.ThumbPath != b.ID+"/abc_64.webp" || got.Avatar.Size != 1234 || !got.Avatar.UploadedAt.Equal(at) {
		t.Fatalf("round trip %+v %+v %v", got, got.Avatar, err)
	}
	got.Avatar, got.UsernameChangedAt = nil, time.Time{}
	if _, err := users.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, err = users.Get(ctx, b.ID); err != nil || got.Avatar != nil || !got.UsernameChangedAt.IsZero() {
		t.Fatalf("back to the default %+v %v", got, err)
	}
}
