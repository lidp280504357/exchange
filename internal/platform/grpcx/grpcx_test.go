package grpcx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/logging"
	"github.com/skill/exchange/internal/platform/tracing"
)

func init() { _ = tracing.Setup() }

// testService is registered by hand, so the test needs no generated code.
func testService() *grpc.ServiceDesc {
	method := func(name string, fn func(ctx context.Context) (*wrapperspb.StringValue, error)) grpc.MethodDesc {
		return grpc.MethodDesc{
			MethodName: name,
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := new(wrapperspb.StringValue)
				if err := dec(in); err != nil {
					return nil, err
				}
				h := func(ctx context.Context, _ any) (any, error) { return fn(ctx) }
				return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/test.Svc/" + name}, h)
			},
		}
	}
	return &grpc.ServiceDesc{
		ServiceName: "test.Svc",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			method("Echo", func(ctx context.Context) (*wrapperspb.StringValue, error) {
				md, _ := metadata.FromIncomingContext(ctx)
				return wrapperspb.String(tracing.TraceID(ctx) + "|" + strings.Join(md.Get(requestIDKey), ",")), nil
			}),
			method("Coded", func(context.Context) (*wrapperspb.StringValue, error) {
				return nil, apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")
			}),
			method("Plain", func(context.Context) (*wrapperspb.StringValue, error) {
				return nil, errors.New("pq: password authentication failed")
			}),
			method("Panic", func(context.Context) (*wrapperspb.StringValue, error) {
				panic("kaboom")
			}),
			method("Status", func(context.Context) (*wrapperspb.StringValue, error) {
				return nil, status.Error(codes.Unavailable, "downstream down")
			}),
		},
	}
}

func startServer(t *testing.T) (*Server, *grpc.ClientConn, *bytes.Buffer, *prometheus.Registry) {
	t.Helper()
	logs := &bytes.Buffer{}
	reg := prometheus.NewRegistry()
	srv, err := NewServer(t.Context(), "127.0.0.1:0", ServerOptions{
		Logger:  logging.New(logs, logging.FormatJSON, slog.LevelInfo),
		Metrics: NewMetrics(reg),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.RegisterService(testService(), struct{}{})
	go func() { _ = srv.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})
	conn, err := Dial(srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, conn, logs, reg
}

func call(ctx context.Context, conn *grpc.ClientConn, method string) (string, error) {
	out := new(wrapperspb.StringValue)
	err := conn.Invoke(ctx, "/test.Svc/"+method, wrapperspb.String(""), out)
	return out.GetValue(), err
}

func TestHealthService(t *testing.T) {
	_, conn, _, _ := startServer(t)
	resp, err := healthpb.NewHealthClient(conn).Check(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health: %v %v", resp, err)
	}
}

func TestTraceAndRequestIDPropagate(t *testing.T) {
	_, conn, _, _ := startServer(t)
	ctx, span := tracing.Tracer("test").Start(t.Context(), "client")
	defer span.End()
	ctx = httpx.WithRequestID(ctx, "req-42")

	got, err := call(ctx, conn, "Echo")
	if err != nil {
		t.Fatal(err)
	}
	if got != tracing.TraceID(ctx)+"|req-42" {
		t.Fatalf("server saw %q, client trace %s", got, tracing.TraceID(ctx))
	}
}

func TestCodedErrorsCrossTheWire(t *testing.T) {
	_, conn, _, _ := startServer(t)
	_, err := call(t.Context(), conn, "Coded")
	e := apperr.From(err)
	if e.Code != "LEDGER_INSUFFICIENT_BALANCE" || e.Kind != apperr.KindUnprocessable || e.Message != "insufficient balance" {
		t.Fatalf("coded error mangled: %+v", e)
	}
}

func TestInternalErrorsAreOpaque(t *testing.T) {
	_, conn, logs, _ := startServer(t)
	for _, m := range []string{"Plain", "Panic"} {
		_, err := call(t.Context(), conn, m)
		e := apperr.From(err)
		if e.Code != apperr.CodeInternal || strings.Contains(e.Message, "password") || strings.Contains(e.Message, "kaboom") {
			t.Fatalf("%s: internal detail reached the client: %+v", m, e)
		}
	}
	if !strings.Contains(logs.String(), "password authentication failed") || !strings.Contains(logs.String(), "kaboom") {
		t.Fatalf("causes must be logged on the server:\n%s", logs)
	}
}

func TestUnavailableMapsToUnavailable(t *testing.T) {
	_, conn, _, _ := startServer(t)
	_, err := call(t.Context(), conn, "Status")
	if e := apperr.From(err); e.Kind != apperr.KindUnavailable || e.Code != apperr.CodeUnavailable {
		t.Fatalf("got %+v", e)
	}
}

func TestMetricsAndStop(t *testing.T) {
	srv, conn, _, reg := startServer(t)
	_, _ = call(t.Context(), conn, "Echo")
	_, _ = call(t.Context(), conn, "Coded")
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, mf := range mfs {
		if mf.GetName() != "grpc_server_handled_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var labels []string
			for _, lp := range m.GetLabel() {
				labels = append(labels, lp.GetValue())
			}
			seen[strings.Join(labels, " ")] = true
		}
	}
	if !seen["OK /test.Svc/Echo"] || !seen["FailedPrecondition /test.Svc/Coded"] {
		t.Fatalf("metrics = %v", seen)
	}
	// Methods never called are exported at zero from the start.
	if !seen["OK /test.Svc/Panic"] || !seen["OK /grpc.health.v1.Health/Check"] {
		t.Fatalf("uncalled methods missing: %v", seen)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := call(t.Context(), conn, "Echo"); err == nil {
		t.Fatal("calls after Stop must fail")
	}
}

func TestToStatusAndBack(t *testing.T) {
	if ToStatus(nil) != nil || FromStatus(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	if st := ToStatus(context.DeadlineExceeded); st.Code() != codes.DeadlineExceeded {
		t.Fatalf("deadline: %v", st.Code())
	}
	plain := errors.New("not a status")
	if !errors.Is(FromStatus(plain), plain) {
		t.Fatal("non-status errors pass through")
	}
}
