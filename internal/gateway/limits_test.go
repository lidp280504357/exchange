package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
)

type fakeLimiter struct {
	res ratelimit.Result
	err error
	got []ratelimit.Check
}

func (f *fakeLimiter) Allow(_ context.Context, checks ...ratelimit.Check) (ratelimit.Result, error) {
	f.got = checks
	return f.res, f.err
}

func TestLimitsHeadersAndRefusals(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	serve := func(l *fakeLimiter) *httptest.ResponseRecorder {
		h := (&Limits{Limiter: l, Log: slog.New(slog.DiscardHandler)}).ByIP(RuleIP)(ok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/market/pairs", http.NoBody))
		return rec
	}

	rec := serve(&fakeLimiter{res: ratelimit.Result{Allowed: true, Rule: RuleIP, Remaining: 41}})
	if rec.Code != 204 || rec.Header().Get("X-RateLimit-Limit") != "1200" || rec.Header().Get("X-RateLimit-Remaining") != "41" {
		t.Fatalf("allowed: %d %v", rec.Code, rec.Header())
	}
	rec = serve(&fakeLimiter{res: ratelimit.Result{Rule: RuleIP, RetryAfter: 1500 * time.Millisecond}})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" || rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("refused: %d %v", rec.Code, rec.Header())
	}
	if rec := serve(&fakeLimiter{err: errors.New("redis down")}); rec.Code != 204 {
		t.Fatalf("a broken limiter lets requests through: %d", rec.Code)
	}

	// Per-user quotas skip anonymous requests.
	l := &fakeLimiter{res: ratelimit.Result{Allowed: true, Rule: RuleUser}}
	h := (&Limits{Limiter: l, Log: slog.New(slog.DiscardHandler)}).ByUser(RuleUser)(ok)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody))
	if l.got != nil {
		t.Fatal("anonymous requests have no user quota")
	}
	ctx := context.WithValue(context.Background(), identityKey{}, Identity{UserID: "u-1"})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody))
	if len(l.got) != 1 || l.got[0].Key != "u-1" {
		t.Fatalf("user quota: %+v", l.got)
	}
}
