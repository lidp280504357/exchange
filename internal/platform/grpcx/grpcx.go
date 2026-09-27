// Package grpcx is the gRPC template for synchronous calls between services
// (requirements §4: freeze, transfer and similar paths). Servers get panic
// recovery, trace context, access logs, metrics and apperr mapping; clients
// propagate the trace and request ID and turn statuses back into apperr
// errors.
package grpcx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"

	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/logging"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

// Metrics are the server-side call metrics.
type Metrics struct {
	handled  *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics registers the gRPC server metrics with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		handled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_handled_total",
			Help: "gRPC calls completed, by method and status code.",
		}, []string{"method", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "grpc_server_handling_seconds",
			Help:    "gRPC call latency, by method.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}, []string{"method"}),
	}
	reg.MustRegister(m.handled, m.duration)
	return m
}

// ServerOptions configures NewServer.
type ServerOptions struct {
	Logger  *slog.Logger
	Metrics *Metrics
	// Reflection enables server reflection for grpcurl; keep it off in prod.
	Reflection bool
}

// Server runs a grpc.Server as an app component. It embeds the grpc.Server,
// so generated RegisterXxxServer functions accept it.
type Server struct {
	*grpc.Server
	ln     net.Listener
	log    *slog.Logger
	health *grpchealth.Server
}

// NewServer binds addr and returns a server with the standard interceptors
// and the gRPC health service. Register services before Run.
func NewServer(ctx context.Context, addr string, opts ServerOptions) (*Server, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("grpc: %w", err)
	}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(serverUnary(opts.Logger, opts.Metrics)))
	h := grpchealth.NewServer()
	healthpb.RegisterHealthServer(srv, h)
	if opts.Reflection {
		reflection.Register(srv)
	}
	return &Server{Server: srv, ln: ln, log: opts.Logger, health: h}, nil
}

// Addr returns the address the server listens on.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Run serves until Stop.
func (s *Server) Run() error {
	s.log.Info("grpc server listening", "addr", s.ln.Addr().String())
	if err := s.Serve(s.ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("grpc: %w", err)
	}
	return nil
}

// Stop reports NOT_SERVING, lets in-flight calls finish, and closes the
// remaining connections when ctx ends first.
func (s *Server) Stop(ctx context.Context) error {
	s.health.Shutdown()
	done := make(chan struct{})
	go func() {
		s.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.Server.Stop()
		return ctx.Err()
	}
}

// requestIDKey is the metadata key carrying the HTTP request ID.
const requestIDKey = "x-request-id"

func serverUnary(log *slog.Logger, m *Metrics) grpc.UnaryServerInterceptor {
	tracer := tracing.Tracer("grpcx")
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		md, _ := metadata.FromIncomingContext(ctx)
		ctx = otel.GetTextMapPropagator().Extract(ctx, mdCarrier(md))
		ctx, span := tracer.Start(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		if ids := md.Get(requestIDKey); len(ids) > 0 {
			ctx = logging.WithAttrs(ctx, slog.String("request_id", ids[0]))
		}

		resp, err := callGuarded(ctx, req, handler)
		code := codes.OK
		attrs := []any{"method", info.FullMethod}
		if err != nil {
			st := ToStatus(err)
			code = st.Code()
			if code == codes.Internal || code == codes.Unknown {
				span.SetStatus(otelcodes.Error, code.String())
				attrs = append(attrs, "error", err.Error()) // the cause stays in the logs
			}
			err = st.Err()
		}
		level := slog.LevelInfo
		if code == codes.Internal || code == codes.Unknown {
			level = slog.LevelError
		}
		attrs = append(attrs, "code", code.String(), "duration_ms", time.Since(start).Milliseconds())
		log.Log(ctx, level, "grpc request", attrs...)
		if m != nil {
			m.handled.WithLabelValues(info.FullMethod, code.String()).Inc()
			m.duration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		}
		return resp, err
	}
}

// callGuarded turns a handler panic into an internal error.
func callGuarded(ctx context.Context, req any, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	return handler(ctx, req)
}

// Dial returns a client connection to an internal service. Connections are
// plaintext inside the Docker network; TLS arrives with phase 4.
func Dial(target string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(clientUnary),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", target, err)
	}
	return conn, nil
}

// clientUnary propagates the trace and request ID and maps errors back to
// apperr.
func clientUnary(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	otel.GetTextMapPropagator().Inject(ctx, mdCarrier(md))
	if id := httpx.RequestIDFrom(ctx); id != "" {
		md.Set(requestIDKey, id)
	}
	return FromStatus(invoker(metadata.NewOutgoingContext(ctx, md), method, req, reply, cc, opts...))
}

// mdCarrier adapts gRPC metadata to the OpenTelemetry propagator.
type mdCarrier metadata.MD

func (c mdCarrier) Get(key string) string {
	if v := metadata.MD(c).Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

func (c mdCarrier) Set(key, value string) { metadata.MD(c).Set(strings.ToLower(key), value) }

func (c mdCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
