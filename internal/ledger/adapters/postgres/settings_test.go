package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/ledger/application"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The welcome credits (design 2026-10-04 §4.2): the environment's value
// is the first and only seed; operators change them on the version they
// read, audited; an empty list grants nothing, and a redelivered
// registration finds its grant whatever the credits are now.
func TestWelcomeCreditsSetting(t *testing.T) {
	svc, _, db := setup(t)
	ctx := context.Background()

	w, err := svc.WelcomeCredits(ctx)
	if err != nil || w.Version != 1 || w.UpdatedBy != domain.WelcomeFromEnv || len(w.Credits) != 2 || !w.Credits[0].Amount.Equal(d("10000")) {
		t.Fatalf("seeded: %+v %v", w, err)
	}
	if err := svc.SeedWelcomeCredits(ctx, []application.Credit{{Asset: "USDT", Amount: d("5")}}); err != nil {
		t.Fatal(err)
	}
	if w, _ := svc.WelcomeCredits(ctx); w.Version != 1 || !w.Credits[0].Amount.Equal(d("10000")) {
		t.Fatalf("a second seed changed it: %+v", w)
	}

	early := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), early, "SG"); err != nil {
		t.Fatal(err)
	}

	for name, credits := range map[string][]application.Credit{
		"unknown asset": {{Asset: "XYZ", Amount: d("1")}},
		"too precise":   {{Asset: "USDT", Amount: d("0.0000001")}},
		"zero":          {{Asset: "USDT", Amount: d("0")}},
		"twice":         {{Asset: "BTC", Amount: d("1")}, {Asset: "BTC", Amount: d("2")}},
	} {
		if _, err := svc.SetWelcomeCredits(ctx, credits, 1, "admin:ops@example.com", "testing the checks"); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := svc.SetWelcomeCredits(ctx, nil, 1, "admin:ops@example.com", ""); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("without a reason: %v", err)
	}

	cleared, err := svc.SetWelcomeCredits(ctx, []application.Credit{}, 1, "admin:ops@example.com", "going live")
	if err != nil || cleared.Version != 2 || len(cleared.Credits) != 0 || cleared.UpdatedBy != "admin:ops@example.com" {
		t.Fatalf("cleared: %+v %v", cleared, err)
	}
	if _, err := svc.SetWelcomeCredits(ctx, []application.Credit{{Asset: "USDT", Amount: d("1")}}, 1, "admin:b@example.com", "stale"); !apperr.Is(err, "LEDGER_SETTINGS_CHANGED") {
		t.Fatalf("on a stale version: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM outbox WHERE event_type = 'audit.AdminActionPerformed'`); n != 1 {
		t.Fatalf("audit events: %d", n)
	}

	late := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), late, "SG"); err != nil {
		t.Fatal(err)
	}
	if av, _ := usdt(t, svc, late, domain.AccountSpot); !av.IsZero() {
		t.Fatalf("granted after clearing: %s", av)
	}
	// The early user's registration, redelivered after the change.
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), early, "SG"); err != nil {
		t.Fatalf("redelivered: %v", err)
	}
	if av, _ := usdt(t, svc, early, domain.AccountSpot); !av.Equal(d("10000")) {
		t.Fatalf("the early grant: %s", av)
	}

	raised, err := svc.SetWelcomeCredits(ctx, []application.Credit{{Asset: "USDT", Amount: d("2.5")}}, 2, "admin:ops@example.com", "a small grant")
	if err != nil || raised.Version != 3 {
		t.Fatalf("raised: %+v %v", raised, err)
	}
	third := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), third, "SG"); err != nil {
		t.Fatal(err)
	}
	if av, _ := usdt(t, svc, third, domain.AccountSpot); !av.Equal(d("2.5")) {
		t.Fatalf("granted after the raise: %s", av)
	}
}
