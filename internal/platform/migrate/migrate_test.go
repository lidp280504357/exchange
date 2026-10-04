package migrate

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"testing/fstest"

	"github.com/skill/exchange/internal/platform/testenv"
)

var discard = slog.New(slog.DiscardHandler)

func TestUpAppliesOnceInsideTheSchema(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	fsys := os.DirFS("testdata")

	for range 2 { // the second run must be a no-op
		if err := Up(ctx, db, fsys, discard); err != nil {
			t.Fatalf("Up: %v", err)
		}
	}
	if _, err := db.Exec(ctx, "INSERT INTO widgets (id, name) VALUES (1, 'a')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var color, schema string
	if err := db.QueryRow(ctx, "SELECT color FROM widgets WHERE id = 1").Scan(&color); err != nil || color != "blue" {
		t.Fatalf("color = %q, err = %v", color, err)
	}
	err := db.QueryRow(ctx, "SELECT table_schema FROM information_schema.tables WHERE table_name = 'goose_db_version' AND table_schema = $1", db.Schema()).Scan(&schema)
	if err != nil {
		t.Fatalf("version table must live in the service schema: %v", err)
	}
}

func TestUpWithoutMigrationsIsNoop(t *testing.T) {
	db := testenv.Postgres(t)
	if err := Up(context.Background(), db, nil, discard); err != nil {
		t.Fatalf("nil fs: %v", err)
	}
	if err := Up(context.Background(), db, fstest.MapFS{}, discard); err != nil {
		t.Fatalf("empty fs: %v", err)
	}
}

func TestLockIDIsStablePerSchema(t *testing.T) {
	auth, again, ledger := lockID("auth"), lockID("auth"), lockID("ledger")
	if auth != again || auth == ledger || auth < 0 || ledger < 0 {
		t.Fatal("lock IDs must be stable, distinct and non-negative")
	}
}
