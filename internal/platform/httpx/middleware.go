package httpx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/logging"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

// Header names used across services.
const (
	HeaderRequestID = "X-Request-Id"
	HeaderTraceID   = "X-Trace-Id"
	HeaderRealIP    = "X-Real-Ip"
	HeaderForwarded = "X-Forwarded-For"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	clientIPKey
)

// RequestIDFrom returns the request ID attached by RequestID.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// ClientIPFrom returns the client address resolved by ClientIP.
func ClientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey).(string)
	return ip
}

// RequestID accepts a well-formed X-Request-Id from the caller or creates
// one, echoes it in the response and attaches it to the context and logs.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID(id) {
			id = uuid.Must(uuid.NewV7()).String()
		}
		w.Header().Set(HeaderRequestID, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = logging.WithAttrs(ctx, slog.String("request_id", id))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:", c)
		if !ok {
			return false
		}
	}
	return true
}

// DefaultTrustedProxies are the private and loopback ranges, where nginx
// and the other containers live.
var DefaultTrustedProxies = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
}

// ClientIP resolves the client address. Forwarding headers are honored only
// when the direct peer is a trusted proxy: X-Real-IP first (nginx sets it
// from Cloudflare's CF-Connecting-IP), then the right-most untrusted hop of
// X-Forwarded-For.
func ClientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peerAddr(r.RemoteAddr)
			if ip.IsValid() && isTrusted(ip) {
				if realIP, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(HeaderRealIP))); err == nil {
					ip = realIP
				} else if fwd := r.Header.Get(HeaderForwarded); fwd != "" {
					hops := strings.Split(fwd, ",")
					for i := len(hops) - 1; i >= 0; i-- {
						hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
						if err != nil {
							break
						}
						ip = hop
						if !isTrusted(hop) {
							break
						}
					}
				}
			}
			s := ""
			if ip.IsValid() {
				s = ip.Unmap().String()
			}
			ctx := context.WithValue(r.Context(), clientIPKey, s)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func peerAddr(remote string) netip.Addr {
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return ap.Addr()
	}
	a, _ := netip.ParseAddr(remote)
	return a
}

// Trace continues the caller's W3C trace context or starts a new trace, and
// returns the trace ID in X-Trace-Id so that every response carries it.
func Trace(tracer trace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, r.Method, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()
			w.Header().Set(HeaderTraceID, tracing.TraceID(ctx))

			rec := wrap(w)
			next.ServeHTTP(rec, r.WithContext(ctx))

			span.SetName(r.Method + " " + routePattern(r))
			span.SetAttributes(attribute.Int("http.response.status_code", rec.status))
			if rec.status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(rec.status))
			}
		})
	}
}

// AccessLog logs one line per request once the response is written.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := wrap(w)
			next.ServeHTTP(rec, r)
			level := slog.LevelInfo
			if rec.status >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			log.Log(r.Context(), level, "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"route", routePattern(r),
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"client_ip", ClientIPFrom(r.Context()),
			)
		})
	}
}

// HTTPMetrics counts requests and observes latency per method, route and
// status. Routes come from the router's patterns, which bounds cardinality.
type HTTPMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewHTTPMetrics registers the HTTP server metrics with reg.
func NewHTTPMetrics(reg prometheus.Registerer) *HTTPMetrics {
	m := &HTTPMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_server_requests_total",
			Help: "HTTP requests served, by method, route and status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_request_duration_seconds",
			Help:    "HTTP request latency, by method, route and status.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "route", "status"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

// Middleware records the metrics of each request.
func (m *HTTPMetrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := wrap(w)
		next.ServeHTTP(rec, r)
		labels := prometheus.Labels{"method": r.Method, "route": routePattern(r), "status": strconv.Itoa(rec.status)}
		m.requests.With(labels).Inc()
		m.duration.With(labels).Observe(time.Since(start).Seconds())
	})
}

// Recover turns a panic into a logged COMMON_INTERNAL response.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if p == http.ErrAbortHandler { //nolint:errorlint // sentinel value compared as net/http does
				panic(p)
			}
			slog.Default().ErrorContext(r.Context(), "handler panic", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			rec, ok := w.(*recorder)
			if !ok || !rec.wroteHeader {
				WriteError(w, r, apperr.Internal(fmt.Errorf("panic: %v", p)))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// NotFound and MethodNotAllowed answer unmatched routes in the unified format.
func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, apperr.NotFound("no such endpoint"))
}

// MethodNotAllowed answers a known path requested with the wrong method.
func MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusMethodNotAllowed, ErrorBody{
		Code:    apperr.CodeMethodNotAllowed,
		Message: "method not allowed",
		TraceID: tracing.TraceID(r.Context()),
	})
}

func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}

// recorder captures the status and size of a response. It is shared by the
// middlewares of one request, and it keeps Flush and Hijack working for
// streaming and WebSocket handlers.
type recorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func wrap(w http.ResponseWriter) *recorder {
	if rec, ok := w.(*recorder); ok {
		return rec
	}
	return &recorder{ResponseWriter: w, status: http.StatusOK}
}

func (r *recorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpx: response writer does not support hijacking")
	}
	r.status = http.StatusSwitchingProtocols
	r.wroteHeader = true
	return h.Hijack()
}
