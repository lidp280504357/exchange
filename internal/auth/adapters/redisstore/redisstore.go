// Package redisstore keeps auth-service's short-lived state in Redis:
// session revocation marks for the gateway and password-failure counters.
// Both may be lost without harm (requirements §4).
package redisstore

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/platform/authtoken"
)

// Revocations implements ports.Revocations.
type Revocations struct{ rdb redis.Cmdable }

// NewRevocations returns the revocation marker.
func NewRevocations(rdb redis.Cmdable) *Revocations { return &Revocations{rdb: rdb} }

// Revoke marks sessions as ended for as long as their access tokens live.
func (r *Revocations) Revoke(ctx context.Context, sessionIDs ...string) error {
	pipe := r.rdb.Pipeline()
	for _, id := range sessionIDs {
		pipe.Set(ctx, authtoken.RevokedKey(id), "1", authtoken.AccessTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("mark revoked sessions: %w", err)
	}
	return nil
}

// markStale keeps the latest time only, so a redelivered older change
// cannot shorten the window.
var markStale = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if tonumber(ARGV[1]) > cur then
  redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
end
return 0`)

// MarkStale records that the user's tokens issued up to upTo carry an
// outdated scope.
func (r *Revocations) MarkStale(ctx context.Context, userID string, upTo time.Time) error {
	err := markStale.Run(ctx, r.rdb, []string{authtoken.StaleKey(userID)}, upTo.UnixMilli(), int(authtoken.AccessTTL.Seconds())).Err()
	if err != nil {
		return fmt.Errorf("mark stale tokens: %w", err)
	}
	return nil
}

// Guard implements ports.LoginGuard with a counter per identifier whose
// window restarts at every failure.
type Guard struct {
	rdb    redis.Cmdable
	prefix string
}

// NewGuard returns counters under prefix, e.g. "auth:".
func NewGuard(rdb redis.Cmdable, prefix string) *Guard { return &Guard{rdb: rdb, prefix: prefix} }

// Failures returns the count and the time until it resets.
func (g *Guard) Failures(ctx context.Context, key string) (int, time.Duration, error) {
	pipe := g.rdb.Pipeline()
	get := pipe.Get(ctx, g.prefix+key)
	ttl := pipe.PTTL(ctx, g.prefix+key)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil { //nolint:errorlint // redis.Nil is a sentinel value
		return 0, 0, fmt.Errorf("read login failures: %w", err)
	}
	n, err := get.Int()
	if err != nil {
		return 0, 0, nil //nolint:nilerr // no counter means no failures
	}
	return n, ttl.Val(), nil
}

// Fail counts one failure and restarts the window.
func (g *Guard) Fail(ctx context.Context, key string) (int, error) {
	pipe := g.rdb.TxPipeline()
	incr := pipe.Incr(ctx, g.prefix+key)
	pipe.Expire(ctx, g.prefix+key, domain.LockDuration)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("count login failure: %w", err)
	}
	return int(incr.Val()), nil
}

// Clear forgets the failures after a successful login.
func (g *Guard) Clear(ctx context.Context, key string) error {
	if err := g.rdb.Del(ctx, g.prefix+key).Err(); err != nil {
		return fmt.Errorf("clear login failures: %w", err)
	}
	return nil
}
