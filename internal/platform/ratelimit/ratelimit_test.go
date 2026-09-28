package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func TestAllowCountsUntilTheLimit(t *testing.T) {
	rdb, prefix := testenv.Redis(t)
	l := ratelimit.New(rdb, prefix)
	ctx := context.Background()
	rule := ratelimit.Rule{Name: "per_target", Limit: 3, Window: time.Minute}

	for i := range 3 {
		r, err := l.Allow(ctx, ratelimit.Check{Rule: rule, Key: "a@x.com"})
		if err != nil || !r.Allowed || r.Remaining != 2-i || r.Rule.Name != "per_target" {
			t.Fatalf("hit %d: %+v %v", i+1, r, err)
		}
	}
	r, err := l.Allow(ctx, ratelimit.Check{Rule: rule, Key: "a@x.com"})
	if err != nil || r.Allowed || r.Rule.Name != "per_target" || r.RetryAfter <= 0 || r.RetryAfter > time.Minute {
		t.Fatalf("fourth hit: %+v %v", r, err)
	}
	if r, _ := l.Allow(ctx, ratelimit.Check{Rule: rule, Key: "b@x.com"}); !r.Allowed {
		t.Fatal("keys are independent")
	}
}

func TestAllowConsumesNothingWhenOneRuleIsExhausted(t *testing.T) {
	rdb, prefix := testenv.Redis(t)
	l := ratelimit.New(rdb, prefix)
	ctx := context.Background()
	resend := ratelimit.Rule{Name: "resend", Limit: 1, Window: time.Minute}
	hourly := ratelimit.Rule{Name: "hourly", Limit: 5, Window: time.Hour}
	checks := []ratelimit.Check{{Rule: hourly, Key: "t"}, {Rule: resend, Key: "t"}}

	if r, _ := l.Allow(ctx, checks...); !r.Allowed || r.Rule.Name != "resend" || r.Remaining != 0 {
		t.Fatalf("first request must pass, reporting the tightest rule: %+v", r)
	}
	for range 3 {
		if r, _ := l.Allow(ctx, checks...); r.Allowed || r.Rule.Name != "resend" {
			t.Fatalf("resend window must block: %+v", r)
		}
	}
	// The blocked attempts did not use up the hourly quota.
	n, err := rdb.Get(ctx, prefix+"hourly:t").Int()
	if err != nil || n != 1 {
		t.Fatalf("hourly counter = %d, %v", n, err)
	}
	if err := l.Reset(ctx, checks[1]); err != nil {
		t.Fatal(err)
	}
	if r, _ := l.Allow(ctx, checks...); !r.Allowed {
		t.Fatal("reset must reopen the window")
	}
}

func TestWindowExpires(t *testing.T) {
	rdb, prefix := testenv.Redis(t)
	l := ratelimit.New(rdb, prefix)
	ctx := context.Background()
	rule := ratelimit.Rule{Name: "short", Limit: 1, Window: 300 * time.Millisecond}
	if r, _ := l.Allow(ctx, ratelimit.Check{Rule: rule, Key: "k"}); !r.Allowed {
		t.Fatal("first")
	}
	time.Sleep(600 * time.Millisecond)
	if r, _ := l.Allow(ctx, ratelimit.Check{Rule: rule, Key: "k"}); !r.Allowed {
		t.Fatal("window must expire")
	}
}
