package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/internal/user/adapters/postgres"
	"github.com/skill/exchange/internal/user/domain"
	"github.com/skill/exchange/migrations"
)

func TestUsersStatus(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Users(), quiet); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	u, _ := domain.NewUser(id, "SG", "", "")
	if _, err := postgres.NewStore(db, nil).Read().Users().Create(ctx, u, nil); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := usersWith(ctx, db, args, &out)
		return out.String(), err
	}

	if out, err := run("status", id, "--to", "FROZEN"); err == nil {
		t.Fatalf("--reason is required:\n%s", out)
	}
	out, err := run("status", id, "--to", "FROZEN", "--reason", "SUSPICIOUS_LOGIN", "--note", "ticket 42")
	if err != nil || !strings.Contains(out, "ACTIVE -> FROZEN") {
		t.Fatalf("freeze: %v\n%s", err, out)
	}
	if _, err := run("status", id, "--to", "RISK_REVIEW", "--reason", "RISK_RULE"); err == nil {
		t.Fatal("FROZEN -> RISK_REVIEW must be refused")
	}
	out, err = run("show", id)
	if err != nil || !strings.Contains(out, `"Status": "FROZEN"`) || !strings.Contains(out, "SUSPICIOUS_LOGIN") {
		t.Fatalf("show: %v\n%s", err, out)
	}
	var queued int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type IN ('user.UserStatusChanged', 'audit.AdminActionPerformed')`).Scan(&queued); err != nil || queued != 2 {
		t.Fatalf("queued events: %d %v", queued, err)
	}
}

// users kind (L0): sets the kind of the accounts named, kept and audited;
// a run again changes nothing; the kind and the reason are checked.
func TestUsersKind(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Users(), quiet); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for range 2 {
		u, _ := domain.NewUser(uuid.NewString(), "SG", "", "")
		if _, err := postgres.NewStore(db, nil).Read().Users().Create(ctx, u, nil); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := usersWith(ctx, db, args, &out)
		return out.String(), err
	}
	both := ids[0] + "," + ids[1]
	out, err := run("kind", "--user", both, "--kind", "bot", "--reason", "the simulated market's bots")
	if err != nil || !strings.Contains(out, "HUMAN -> BOT") || !strings.Contains(out, "2 accounts, 2 changed to BOT") {
		t.Fatalf("set: %v\n%s", err, out)
	}
	if out, err := run("kind", "--user", both, "--kind", "BOT", "--reason", "again"); err != nil || !strings.Contains(out, "2 accounts, 0 changed") {
		t.Fatalf("again: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"kind", "--user", both, "--kind", "ROBOT", "--reason", "x"},
		{"kind", "--user", both, "--kind", "BOT"},
		{"kind", "--kind", "BOT", "--reason", "x"},
		{"kind", "--user", both, "--email-like", "e2e-%@example.com", "--kind", "TEST", "--reason", "x"},
	} {
		if out, err := run(args...); err == nil {
			t.Fatalf("%v accepted:\n%s", args, out)
		}
	}
	var changes, audits int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM user_kind_changes WHERE to_kind = 'BOT'`).Scan(&changes); err != nil || changes != 2 {
		t.Fatalf("changes: %d %v", changes, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type = 'audit.AdminActionPerformed'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audits: %d %v", audits, err)
	}
}

// users exempt (L4): keeps the accounts named out of the purge, or lets
// them in again (--off), audited; a run again changes nothing; one of
// --user and --email-like, and a reason, are required.
func TestUsersExempt(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Users(), quiet); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for range 2 {
		u, _ := domain.NewUser(uuid.NewString(), "SG", "", "")
		if _, err := postgres.NewStore(db, nil).Read().Users().Create(ctx, u, nil); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := usersWith(ctx, db, args, &out)
		return out.String(), err
	}
	both := ids[0] + "," + ids[1]
	if out, err := run("exempt", "--user", both, "--reason", "funding.sh's hedge"); err != nil || !strings.Contains(out, "2 accounts, 2 changed to exempt=true") {
		t.Fatalf("exempt: %v\n%s", err, out)
	}
	if out, err := run("exempt", "--user", both, "--reason", "again"); err != nil || !strings.Contains(out, "2 accounts, 0 changed") {
		t.Fatalf("again: %v\n%s", err, out)
	}
	if out, err := run("exempt", "--user", ids[0], "--off", "--reason", "the hedge is gone"); err != nil || !strings.Contains(out, "1 accounts, 1 changed to exempt=false") {
		t.Fatalf("off: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"exempt", "--user", both},
		{"exempt", "--reason", "x"},
		{"exempt", "--user", both, "--email-like", "e2e-%@example.com", "--reason", "x"},
	} {
		if out, err := run(args...); err == nil {
			t.Fatalf("%v accepted:\n%s", args, out)
		}
	}
	var exempt, audits int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM users WHERE purge_exempt`).Scan(&exempt); err != nil || exempt != 1 {
		t.Fatalf("exempt: %d %v", exempt, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type = 'audit.AdminActionPerformed'`).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audits: %d %v", audits, err)
	}
}
