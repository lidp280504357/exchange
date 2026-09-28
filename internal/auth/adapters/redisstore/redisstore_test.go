package redisstore

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/platform/authtoken"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func TestRevocations(t *testing.T) {
	rdb, _ := testenv.Redis(t)
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() { rdb.Del(context.Background(), authtoken.RevokedKey(a), authtoken.RevokedKey(b)) })

	if err := NewRevocations(rdb).Revoke(ctx, a, b); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b} {
		ttl, err := rdb.TTL(ctx, authtoken.RevokedKey(id)).Result()
		if err != nil || ttl <= 0 || ttl > authtoken.AccessTTL {
			t.Fatalf("mark of %s: ttl %v %v", id, ttl, err)
		}
	}
}

func TestGuard(t *testing.T) {
	rdb, prefix := testenv.Redis(t)
	ctx := context.Background()
	g := NewGuard(rdb, prefix)

	if n, ttl, err := g.Failures(ctx, "k"); err != nil || n != 0 || ttl != 0 {
		t.Fatalf("no failures: %d %v %v", n, ttl, err)
	}
	for i := 1; i <= 3; i++ {
		if n, err := g.Fail(ctx, "k"); err != nil || n != i {
			t.Fatalf("fail %d: %d %v", i, n, err)
		}
	}
	n, ttl, err := g.Failures(ctx, "k")
	if err != nil || n != 3 || ttl <= 0 || ttl > domain.LockDuration {
		t.Fatalf("failures: %d %v %v", n, ttl, err)
	}
	if err := g.Clear(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := g.Failures(ctx, "k"); n != 0 {
		t.Fatalf("after clear: %d", n)
	}
}

func TestMarkStaleKeepsTheLatest(t *testing.T) {
	rdb, _ := testenv.Redis(t)
	ctx := context.Background()
	user := uuid.NewString()
	t.Cleanup(func() { rdb.Del(context.Background(), authtoken.StaleKey(user)) })
	r := NewRevocations(rdb)
	later := time.Now()
	if err := r.MarkStale(ctx, user, later); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkStale(ctx, user, later.Add(-time.Minute)); err != nil { // a redelivered older change
		t.Fatal(err)
	}
	got, err := rdb.Get(ctx, authtoken.StaleKey(user)).Int64()
	if err != nil || got != later.UnixMilli() {
		t.Fatalf("mark: %d %v, want %d", got, err, later.UnixMilli())
	}
	if ttl, _ := rdb.TTL(ctx, authtoken.StaleKey(user)).Result(); ttl <= 0 || ttl > authtoken.AccessTTL {
		t.Fatalf("ttl: %v", ttl)
	}
}
