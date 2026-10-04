// Package gateway is the public entry point's logic (requirements §5.1):
// it forwards /v1 requests to the owning services, and authenticates,
// rate-limits and deduplicates them on the way.
package gateway

import (
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// internalHeaders are set only by the gateway; client copies are dropped.
var internalHeaders = []string{httpx.HeaderUserID, httpx.HeaderSessionID, httpx.HeaderScope}

// transport is shared by the proxies: services are close, so timeouts are
// short.
var transport = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	MaxIdleConns:          200,
	MaxIdleConnsPerHost:   50,
	IdleConnTimeout:       90 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}

// NewProxy forwards requests to target, keeping the path. The service
// receives the client IP resolved by the gateway (X-Real-Ip), the request
// ID and the trace context; identity headers are set by the gateway alone.
func NewProxy(target *url.URL) http.Handler {
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			ctx := pr.In.Context()
			for _, h := range internalHeaders {
				pr.Out.Header.Del(h)
			}
			if ip := httpx.ClientIPFrom(ctx); ip != "" {
				pr.Out.Header.Set(httpx.HeaderRealIP, ip)
			}
			pr.Out.Header.Set(httpx.HeaderRequestID, httpx.RequestIDFrom(ctx))
			otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(pr.Out.Header))
			if id, ok := ctx.Value(identityKey{}).(Identity); ok {
				pr.Out.Header.Set(httpx.HeaderUserID, id.UserID)
				pr.Out.Header.Set(httpx.HeaderSessionID, id.SessionID)
				pr.Out.Header.Set(httpx.HeaderScope, id.Scope)
			}
		},
		// The gateway has already set the request and trace IDs of the
		// response (the service saw the same ones); without this the client
		// would get each header twice.
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del(httpx.HeaderRequestID)
			resp.Header.Del(httpx.HeaderTraceID)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, http.ErrAbortHandler) {
				return
			}
			httpx.WriteError(w, r, apperr.Unavailable(err))
		},
	}
}

// Identity is the authenticated caller, attached by the auth middleware.
type Identity struct {
	UserID    string
	SessionID string
	Scope     string
}

type identityKey struct{}
