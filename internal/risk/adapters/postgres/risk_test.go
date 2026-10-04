package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/internal/risk/adapters/postgres"
	"github.com/skill/exchange/internal/risk/application"
	"github.com/skill/exchange/internal/risk/domain"
	"github.com/skill/exchange/migrations"
)

// enforceIn turns risk.enforce on for users of one region.
type enforceIn string

func (r enforceIn) Enabled(key string, s flags.Subject) bool {
	return key == flags.KeyRiskEnforce && s.Region == string(r)
}

func setup(t *testing.T) (*application.Service, *postgres.Store, *pg.DB) {
	t.Helper()
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Risk(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("risk-service", "test"))
	return &application.Service{
		Store: store, Rules: domain.DefaultRules(), Flags: enforceIn("AQ"), Log: log, Now: time.Now,
	}, store, db
}

func TestDevicesAreNewOnlyAfterTheFirst(t *testing.T) {
	_, store, _ := setup(t)
	ctx := context.Background()
	user, at := uuid.NewString(), time.Now()
	record := func(device string) bool {
		t.Helper()
		isNew, err := store.Read().Devices().Record(ctx, user, device, at)
		if err != nil {
			t.Fatal(err)
		}
		return isNew
	}
	if record("d1") {
		t.Fatal("the first device is not news")
	}
	if record("d1") {
		t.Fatal("the same device again is not news")
	}
	if !record("d2") {
		t.Fatal("a second device is news")
	}
	if record("d2") {
		t.Fatal("a known device is not news")
	}
}

func TestTallyCountsEachEventOnceWithinTheWindow(t *testing.T) {
	_, store, _ := setup(t)
	ctx := context.Background()
	v := store.Read().Velocity()
	t0 := time.Now().Truncate(time.Second)
	e1, e2, e3 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tally := func(event string, at time.Time) int {
		t.Helper()
		n, err := v.Tally(ctx, "r", "dev", event, at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := tally(e1, t0); n != 1 {
		t.Fatalf("first event counted %d", n)
	}
	if n := tally(e2, t0.Add(10*time.Minute)); n != 2 {
		t.Fatalf("second event: %d", n)
	}
	if n := tally(e2, t0.Add(10*time.Minute)); n != 2 {
		t.Fatalf("a redelivered event counted again: %d", n)
	}
	// The window ends at the event's own time, so a late redelivery of the
	// first event sees what the live one saw.
	if n := tally(e1, t0); n != 1 {
		t.Fatalf("the first event, redelivered: %d", n)
	}
	if n := tally(e3, t0.Add(2*time.Hour)); n != 1 {
		t.Fatalf("events older than the window counted: %d", n)
	}
	if n, err := v.Purge(ctx, t0.Add(time.Hour)); err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}
}

func registered(user, device, region string) domain.Observation {
	return domain.Observation{
		EventID: uuid.NewString(), EventType: domain.EventRegistered, At: time.Now(),
		UserID: user, DeviceID: device, Network: "203.0.113.*", Region: region,
	}
}

// outbox lists the event types queued in the outbox, oldest first.
func outbox(t *testing.T, db *pg.DB) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT event_type FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestRegistrationBurstsAreReviewedWhereEnforced(t *testing.T) {
	svc, _, db := setup(t)
	ctx := context.Background()
	// Two accounts from one device are fine; the third is scored for review
	// and, in the enforced region, handed to user-service.
	users := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for _, u := range users {
		if err := svc.Assess(ctx, registered(u, "shared-device", "AQ")); err != nil {
			t.Fatal(err)
		}
	}
	list, err := svc.Assessments(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].UserID != users[2] || list[0].Action != domain.ActionReview || !list[0].Enforced ||
		list[0].Score != 60 || list[0].Hits[0].Rule != "registration_burst_device" {
		t.Fatalf("assessments: %+v", list)
	}
	if got := outbox(t, db); len(got) != 2 || got[0] != "risk.RiskScored" || got[1] != "risk.RiskActionTaken" {
		t.Fatalf("outbox: %v", got)
	}

	// Elsewhere the same burst is only scored.
	for range 3 {
		if err := svc.Assess(ctx, registered(uuid.NewString(), "other-device", "SG")); err != nil {
			t.Fatal(err)
		}
	}
	list, _ = svc.Assessments(ctx, "", 10)
	if len(list) != 2 || list[0].Enforced || list[0].Action != domain.ActionReview {
		t.Fatalf("unenforced review: %+v", list[0])
	}
	if got := outbox(t, db); len(got) != 3 || got[2] != "risk.RiskScored" {
		t.Fatalf("outbox: %v", got)
	}
}

func TestRedeliveriesAndNewDevices(t *testing.T) {
	svc, _, db := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	reg := registered(user, "d1", "SG")
	login := domain.Observation{EventID: uuid.NewString(), EventType: domain.EventLogin, At: time.Now(), UserID: user, DeviceID: "d1"}
	for _, o := range []domain.Observation{reg, login, login} {
		if err := svc.Assess(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := svc.Assessments(ctx, user, 10); len(list) != 0 {
		t.Fatalf("registering and logging in from the same device is not news: %+v", list)
	}
	other := domain.Observation{EventID: uuid.NewString(), EventType: domain.EventLogin, At: time.Now(), UserID: user, DeviceID: "d2"}
	for range 2 { // the second is a redelivery
		if err := svc.Assess(ctx, other); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := svc.Assessments(ctx, user, 10)
	if len(list) != 1 || list[0].Hits[0].Rule != "new_device_login" || list[0].Action != domain.ActionNone || list[0].Enforced {
		t.Fatalf("new device: %+v", list)
	}
	if got := outbox(t, db); len(got) != 1 {
		t.Fatalf("a redelivery published again: %v", got)
	}
	if err := svc.Assess(ctx, domain.Observation{EventID: uuid.NewString(), EventType: domain.EventLoginFailed, At: time.Now()}); err != nil {
		t.Fatalf("a failed login of an unknown account: %v", err)
	}
}
