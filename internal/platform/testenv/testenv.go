// Package testenv gives integration tests real infrastructure. Tests call
// these helpers and are skipped when the matching variable is unset, so
// `task ci` stays self-contained:
//
//	TEST_POSTGRES_DSN   a database the tests may create schemas in
//	TEST_REDIS_URL      a Redis database the tests may write keys to
//	TEST_KAFKA_BROKERS  a broker the tests may create topics on
//
// CI provides them with service containers; locally `task test:integration`
// points them at the test server (database exchange_test, Redis DB 15).
package testenv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Name returns a unique lower-case identifier with the given prefix, usable
// as a schema, key prefix or topic name.
func Name(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func lookup(t testing.TB, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Skipf("%s is not set; skipping integration test", name)
	}
	return v
}

// Postgres opens a pool bound to a fresh schema and drops the schema when
// the test ends.
func Postgres(t testing.TB) *pg.DB {
	t.Helper()
	dsn := lookup(t, "TEST_POSTGRES_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := pg.Open(ctx, pg.Config{DSN: dsn, MaxConns: 4}, Name("t"))
	if err != nil {
		t.Fatalf("testenv: postgres: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := db.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{db.Schema()}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("testenv: drop schema %s: %v", db.Schema(), err)
		}
		db.Close()
	})
	return db
}

// PostgresDSN returns TEST_POSTGRES_DSN for tests that open their own pools.
func PostgresDSN(t testing.TB) string {
	t.Helper()
	return lookup(t, "TEST_POSTGRES_DSN")
}

// Redis returns a client and a unique key prefix; keys under the prefix are
// deleted when the test ends.
func Redis(t testing.TB) (*redis.Client, string) {
	t.Helper()
	opts, err := redis.ParseURL(lookup(t, "TEST_REDIS_URL"))
	if err != nil {
		t.Fatalf("testenv: TEST_REDIS_URL: %v", err)
	}
	client := redis.NewClient(opts)
	prefix := Name("t") + ":"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		iter := client.Scan(ctx, 0, prefix+"*", 100).Iterator()
		for iter.Next(ctx) {
			client.Del(ctx, iter.Val())
		}
		_ = client.Close()
	})
	return client, prefix
}

// KafkaBrokers returns TEST_KAFKA_BROKERS split on commas.
func KafkaBrokers(t testing.TB) []string {
	t.Helper()
	return strings.Split(lookup(t, "TEST_KAFKA_BROKERS"), ",")
}
