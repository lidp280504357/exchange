package httpx

import (
	"log/slog"
	"net/netip"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

// RouterOptions configures NewRouter.
type RouterOptions struct {
	Logger  *slog.Logger
	Metrics *HTTPMetrics
	// TrustedProxies defaults to DefaultTrustedProxies.
	TrustedProxies []netip.Prefix
}

// NewRouter returns a chi router with the standard middleware stack:
// request ID, client IP, tracing, access log, metrics and panic recovery,
// plus unified 404/405 responses. Services mount their routes on it.
func NewRouter(opts RouterOptions) *chi.Mux {
	trusted := opts.TrustedProxies
	if trusted == nil {
		trusted = DefaultTrustedProxies
	}
	r := chi.NewRouter()
	r.Use(RequestID, ClientIP(trusted), Trace(tracing.Tracer("httpx")), AccessLog(opts.Logger))
	if opts.Metrics != nil {
		r.Use(opts.Metrics.Middleware)
	}
	r.Use(Recover)
	r.NotFound(NotFound)
	r.MethodNotAllowed(MethodNotAllowed)
	return r
}
