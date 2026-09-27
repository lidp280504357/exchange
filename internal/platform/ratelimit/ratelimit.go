// Package ratelimit enforces request quotas with Redis counters
// (requirements §12.1, §5.3). A window opens on the first hit and closes
// after its duration. Several rules are checked in one Lua script: if any
// is exhausted, none is consumed. Redis only holds data that may be lost;
// losing the counters just resets the quotas.
package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Rule is a quota: Limit hits per Window.
type Rule struct {
	Name   string
	Limit  int
	Window time.Duration
}

// Check applies one rule to one key, e.g. the OTP target or client IP.
type Check struct {
	Rule Rule
	Key  string
}

// Result reports the outcome of Allow.
type Result struct {
	Allowed bool
	// Rule is the exhausted rule when Allowed is false.
	Rule Rule
	// RetryAfter is when the exhausted window closes.
	RetryAfter time.Duration
}

// Limiter keeps counters under a key prefix.
type Limiter struct {
	rdb    redis.Cmdable
	prefix string
}

// New returns a limiter whose keys start with prefix, e.g. "auth:rl:".
func New(rdb redis.Cmdable, prefix string) *Limiter {
	return &Limiter{rdb: rdb, prefix: prefix}
}

// allowScript returns {0, 0} after incrementing every counter, or
// {i, ttl_ms} for the first exhausted check i (1-based) without touching
// any counter.
var allowScript = redis.NewScript(`
for i, key in ipairs(KEYS) do
  local n = tonumber(redis.call('GET', key) or '0')
  if n >= tonumber(ARGV[2*i-1]) then
    return {i, redis.call('PTTL', key)}
  end
end
for i, key in ipairs(KEYS) do
  if redis.call('INCR', key) == 1 then
    redis.call('PEXPIRE', key, ARGV[2*i])
  end
end
return {0, 0}
`)

// Allow consumes one hit of every check, or none if any is exhausted.
func (l *Limiter) Allow(ctx context.Context, checks ...Check) (Result, error) {
	if len(checks) == 0 {
		return Result{Allowed: true}, nil
	}
	keys := make([]string, len(checks))
	args := make([]any, 0, 2*len(checks))
	for i, c := range checks {
		keys[i] = l.prefix + c.Rule.Name + ":" + c.Key
		args = append(args, strconv.Itoa(c.Rule.Limit), strconv.FormatInt(c.Rule.Window.Milliseconds(), 10))
	}
	res, err := allowScript.Run(ctx, l.rdb, keys, args...).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: %w", err)
	}
	if res[0] == 0 {
		return Result{Allowed: true}, nil
	}
	c := checks[res[0]-1]
	retry := time.Duration(res[1]) * time.Millisecond
	if retry <= 0 {
		retry = c.Rule.Window
	}
	return Result{Rule: c.Rule, RetryAfter: retry}, nil
}

// Reset clears the counters of the checks, e.g. failed logins after a
// success.
func (l *Limiter) Reset(ctx context.Context, checks ...Check) error {
	keys := make([]string, len(checks))
	for i, c := range checks {
		keys[i] = l.prefix + c.Rule.Name + ":" + c.Key
	}
	if err := l.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("ratelimit: reset: %w", err)
	}
	return nil
}
