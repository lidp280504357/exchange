package inbox_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/inbox"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func setup(t *testing.T) *pg.DB {
	t.Helper()
	db := testenv.Postgres(t)
	if err := migrate.UpPlatform(context.Background(), db, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), "CREATE TABLE credits (n int NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	return db
}

func credit(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "INSERT INTO credits VALUES (1)")
	return err
}

func credits(t *testing.T, db *pg.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM credits").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRedeliveryIsSkipped(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	env := &eventv1.Envelope{EventId: "0196a1f2-0000-7000-8000-000000000001"}

	for i, want := range []bool{true, false, false} {
		processed, err := inbox.Process(ctx, db, "ledger.credits", env, credit)
		if err != nil || processed != want {
			t.Fatalf("delivery %d: processed=%v err=%v", i+1, processed, err)
		}
	}
	if credits(t, db) != 1 {
		t.Fatalf("event applied %d times", credits(t, db))
	}
	// Another consumer of the same event is independent.
	if processed, err := inbox.Process(ctx, db, "analytics", env, credit); err != nil || !processed {
		t.Fatalf("second consumer: processed=%v err=%v", processed, err)
	}
}

func TestFailedHandlingCanBeRetried(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	env := &eventv1.Envelope{EventId: "0196a1f2-0000-7000-8000-000000000002"}
	boom := errors.New("downstream unavailable")

	_, err := inbox.Process(ctx, db, "c", env, func(ctx context.Context, tx pgx.Tx) error {
		if err := credit(ctx, tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || credits(t, db) != 0 {
		t.Fatalf("failure must roll back: err=%v credits=%d", err, credits(t, db))
	}
	if processed, err := inbox.Process(ctx, db, "c", env, credit); err != nil || !processed || credits(t, db) != 1 {
		t.Fatalf("retry: processed=%v err=%v credits=%d", processed, err, credits(t, db))
	}
}

func TestPurge(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	if _, err := inbox.Process(ctx, db, "c", &eventv1.Envelope{EventId: "0196a1f2-0000-7000-8000-000000000003"}, credit); err != nil {
		t.Fatal(err)
	}
	if n, err := inbox.Purge(ctx, db, time.Now().Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
}
