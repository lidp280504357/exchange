package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/internal/user/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/user/domain"
	"github.com/lidp280504357/exchange/migrations"
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
	if _, err := run("status", id, "--to", "CLOSED", "--reason", "USER_REQUEST"); err == nil {
		t.Fatal("FROZEN -> CLOSED must be refused")
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
