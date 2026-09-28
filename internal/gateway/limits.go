package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
)

// Quotas of the gateway (requirements §12.1): a ceiling per IP on every
// request, a tighter one on the anonymous auth flows, one per signed-in
// user, and one per user on account transfers.
var (
	RuleIP       = ratelimit.Rule{Name: "ip", Limit: 1200, Window: time.Minute}
	RuleIPAuth   = ratelimit.Rule{Name: "ip_auth", Limit: 60, Window: time.Minute}
	RuleUser     = ratelimit.Rule{Name: "user", Limit: 600, Window: time.Minute}
	RuleTransfer = ratelimit.Rule{Name: "user_transfer", Limit: 60, Window: time.Minute}
)

// Limiter is the rate limiter (ratelimit.Limiter).
type Limiter interface {
	Allow(ctx context.Context, checks ...ratelimit.Check) (ratelimit.Result, error)
}

// Limits applies quotas and reports them in X-RateLimit-* headers (§7.1).
type Limits struct {
	Limiter Limiter
	Log     *slog.Logger
}

var errRateLimited = apperr.New(apperr.KindRateLimited, apperr.CodeRateLimited, "too many requests; slow down")

// ByIP limits every request by client IP.
func (l *Limits) ByIP(rules ...ratelimit.Rule) func(http.Handler) http.Handler {
	return l.middleware(func(r *http.Request) []ratelimit.Check {
		ip := httpx.ClientIPFrom(r.Context())
		checks := make([]ratelimit.Check, len(rules))
		for i, rule := range rules {
			checks[i] = ratelimit.Check{Rule: rule, Key: ip}
		}
		return checks
	})
}

// ByUser limits authenticated requests by user; it runs after the
// authenticator and passes anonymous requests through.
func (l *Limits) ByUser(rules ...ratelimit.Rule) func(http.Handler) http.Handler {
	return l.middleware(func(r *http.Request) []ratelimit.Check {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			return nil
		}
		checks := make([]ratelimit.Check, len(rules))
		for i, rule := range rules {
			checks[i] = ratelimit.Check{Rule: rule, Key: id.UserID}
		}
		return checks
	})
}

func (l *Limits) middleware(checks func(*http.Request) []ratelimit.Check) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cs := checks(r)
			if len(cs) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			res, err := l.Limiter.Allow(r.Context(), cs...)
			if err != nil {
				// Losing the counters must not take the API down (§4).
				l.Log.WarnContext(r.Context(), "rate limiter unavailable; letting the request through", "error", err)
				next.ServeHTTP(w, r)
				return
			}
			if !res.Allowed {
				retry := int(res.RetryAfter.Seconds()) + 1
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(res.Rule.Limit))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				httpx.WriteError(w, r, errRateLimited.WithDetail("retry_after_seconds", retry))
				return
			}
			// Nested limiters overwrite the headers: the innermost, most
			// specific quota is reported.
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(res.Rule.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(max(res.Remaining, 0)))
			next.ServeHTTP(w, r)
		})
	}
}
