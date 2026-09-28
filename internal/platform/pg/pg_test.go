package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func TestConfigValidate(t *testing.T) {
	c := pg.DefaultConfig()
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "POSTGRES_DSN") {
		t.Fatalf("missing DSN must fail: %v", err)
	}
	c.DSN = "postgres://x"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func setupTable(t *testing.T, db *pg.DB) {
	t.Helper()
	if _, err := db.Exec(context.Background(), "CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL, CONSTRAINT items_name_key UNIQUE (name))"); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *pg.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM items").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInTxCommitsAndRollsBack(t *testing.T) {
	db := testenv.Postgres(t)
	setupTable(t, db)
	ctx := context.Background()

	err := db.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO items VALUES (1, 'a')")
		return err
	})
	if err != nil || count(t, db) != 1 {
		t.Fatalf("commit: err=%v count=%d", err, count(t, db))
	}

	boom := errors.New("boom")
	err = db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (2, 'b')"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || count(t, db) != 1 {
		t.Fatalf("rollback: err=%v count=%d", err, count(t, db))
	}
}

func TestInTxRetriesSerializationFailures(t *testing.T) {
	db := testenv.Postgres(t)
	attempts := 0
	err := db.InTxOpts(context.Background(), pgx.TxOptions{IsoLevel: pgx.Serializable}, func(pgx.Tx) error {
		attempts++
		if attempts == 1 {
			return &pgconn.PgError{Code: "40001", Message: "could not serialize access"}
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}

	attempts = 0
	err = db.InTx(context.Background(), func(pgx.Tx) error {
		attempts++
		return &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
	})
	if err == nil || attempts != 3 {
		t.Fatalf("retries must stop after 3 attempts: err=%v attempts=%d", err, attempts)
	}
}

func TestErrorHelpers(t *testing.T) {
	db := testenv.Postgres(t)
	setupTable(t, db)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "INSERT INTO items VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(ctx, "INSERT INTO items VALUES (2, 'a')")
	if c, ok := pg.UniqueViolation(err); !ok || c != "items_name_key" {
		t.Fatalf("unique violation not detected: %q %v (%v)", c, ok, err)
	}
	var name string
	err = db.QueryRow(ctx, "SELECT name FROM items WHERE id = 99").Scan(&name)
	if !pg.IsNoRows(err) {
		t.Fatalf("no rows not detected: %v", err)
	}
}

func TestSchemaIsolation(t *testing.T) {
	a := testenv.Postgres(t)
	b := testenv.Postgres(t)
	setupTable(t, a)
	if _, err := b.Exec(context.Background(), "SELECT 1 FROM items"); err == nil {
		t.Fatal("unqualified names must resolve only inside the pool's own schema")
	}
}

func TestRegisterMetrics(t *testing.T) {
	db := testenv.Postgres(t)
	reg := prometheus.NewRegistry()
	db.RegisterMetrics(reg)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{"pg_pool_total_conns", "pg_pool_max_conns", "pg_pool_acquires_total"} {
		if !names[want] {
			t.Fatalf("metric %s missing: %v", want, names)
		}
	}
}

func TestLeasesAdmitOneHolderAtATime(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	name := testenv.Name("lease")
	first, err := pg.AcquireLease(ctx, db, name)
	if err != nil {
		t.Fatal(err)
	}
	// A held connection would keep the pool from closing after a failure.
	t.Cleanup(func() { _ = first.Release(ctx) })
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := pg.AcquireLease(waitCtx, db, name); err == nil {
		t.Fatal("a second holder got the lease")
	}
	if err := first.Release(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := pg.AcquireLease(ctx, db, name)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	t.Cleanup(func() { _ = second.Release(ctx) })
	// The interval stays well above the round trip to the test database.
	holdCtx, stop := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer stop()
	if err := second.Hold(holdCtx, time.Second); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatalf("second release: %v", err)
	}
}
