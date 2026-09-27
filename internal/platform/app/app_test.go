package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/logging"
)

// syncBuffer is a log sink that tests may read while components still write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// recorder collects events from concurrent components in order.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func newTestApp(t *testing.T, env ...string) (*App, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	vars := append([]string{"APP_ENV=test", "SHUTDOWN_TIMEOUT=2s", "OPS_ADDR=127.0.0.1:0"}, env...)
	a, err := New("test-svc",
		WithLoader(config.Loader{Environ: func() []string { return vars }}),
		WithLogOutput(logs),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, logs
}

// waitLoop is a well-behaved component that records when it stops.
func waitLoop(rec *recorder, name string) Component {
	return Loop(func(ctx context.Context) error {
		<-ctx.Done()
		rec.add(name + " stopped")
		return ctx.Err()
	})
}

func runAsync(ctx context.Context, a *App) <-chan error {
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	return done
}

func waitErr(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

func TestNewRejectsBadSettings(t *testing.T) {
	cases := map[string]struct {
		env  []string
		want string
	}{
		"missing app env":  {nil, "APP_ENV is required"},
		"unknown app env":  {[]string{"APP_ENV=dev"}, "unknown environment"},
		"zero timeout":     {[]string{"APP_ENV=test", "SHUTDOWN_TIMEOUT=0s"}, "SHUTDOWN_TIMEOUT must be positive"},
		"unknown format":   {[]string{"APP_ENV=test", "LOG_FORMAT=xml"}, "unknown log format"},
		"unknown loglevel": {[]string{"APP_ENV=test", "LOG_LEVEL=loud"}, "log_level"},
		"missing ops addr": {[]string{"APP_ENV=test"}, "OPS_ADDR is required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := New("svc", WithLoader(config.Loader{Environ: func() []string { return tc.env }}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestNewDefaults(t *testing.T) {
	a, _ := newTestApp(t)
	cfg := a.Config()
	if cfg.Env != config.EnvTest || cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 2*time.Second || cfg.InstanceID == "" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if a.Name() != "test-svc" {
		t.Fatalf("name = %q", a.Name())
	}
}

func TestNewLogFormatFollowsEnv(t *testing.T) {
	cases := map[string]struct {
		env  []string
		want logging.Format
	}{
		"local defaults to text": {[]string{"APP_ENV=local"}, logging.FormatText},
		"test defaults to json":  {[]string{"APP_ENV=test"}, logging.FormatJSON},
		"explicit json on local": {[]string{"APP_ENV=local", "LOG_FORMAT=json"}, logging.FormatJSON},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			logs := &syncBuffer{}
			a, err := New("svc", WithLoader(config.Loader{Environ: func() []string { return tc.env }}),
				WithLogOutput(logs), WithDefaultOpsAddr("127.0.0.1:0"))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if a.Config().LogFormat != tc.want {
				t.Fatalf("format = %q, want %q", a.Config().LogFormat, tc.want)
			}
			a.Logger().Info("hello")
			isJSON := strings.HasPrefix(logs.String(), "{")
			if isJSON != (tc.want == logging.FormatJSON) {
				t.Fatalf("output does not match %s: %q", tc.want, logs.String())
			}
			if !strings.Contains(logs.String(), "svc") {
				t.Fatalf("service attribute missing: %q", logs.String())
			}
		})
	}
}

func TestNewLogLevel(t *testing.T) {
	a, logs := newTestApp(t, "LOG_LEVEL=warn")
	a.Logger().Info("quiet")
	a.Logger().Warn("loud")
	if out := logs.String(); strings.Contains(out, "quiet") || !strings.Contains(out, "loud") {
		t.Fatalf("level not applied: %q", out)
	}
}

func TestLoadConfigUsesSameSources(t *testing.T) {
	a, _ := newTestApp(t, "HTTP_ADDR=:9999")
	var svc struct {
		HTTPAddr string `koanf:"http_addr"`
	}
	if err := a.LoadConfig(&svc); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if svc.HTTPAddr != ":9999" {
		t.Fatalf("addr = %q", svc.HTTPAddr)
	}
}

func TestRunStopsInReverseOrderThenCleansUp(t *testing.T) {
	a, logs := newTestApp(t)
	rec := &recorder{}
	for _, name := range []string{"a", "b", "c"} {
		a.Add(name, waitLoop(rec, name))
	}
	for _, name := range []string{"x", "y"} {
		a.Cleanup(name, func(context.Context) error {
			rec.add("cleanup " + name)
			return nil
		})
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	done := runAsync(ctx, a)
	cancel(errors.New("test says stop"))
	if err := waitErr(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"c stopped", "b stopped", "a stopped", "cleanup y", "cleanup x"}
	if got := rec.list(); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	out := logs.String()
	for _, s := range []string{`"msg":"service started"`, `"reason":"test says stop"`, `"msg":"shutdown complete"`} {
		if !strings.Contains(out, s) {
			t.Fatalf("log missing %s:\n%s", s, out)
		}
	}
}

func TestRunShutsDownWhenComponentFails(t *testing.T) {
	a, logs := newTestApp(t)
	rec := &recorder{}
	a.Add("steady", waitLoop(rec, "steady"))
	a.Add("broken", Loop(func(context.Context) error { return errors.New("boom") }))
	a.Cleanup("db", func(context.Context) error {
		rec.add("cleanup db")
		return nil
	})

	err := waitErr(t, runAsync(context.Background(), a))
	if err == nil || !strings.Contains(err.Error(), "component broken: boom") {
		t.Fatalf("want broken component error, got %v", err)
	}
	if got, want := rec.list(), []string{"steady stopped", "cleanup db"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if !strings.Contains(logs.String(), `"msg":"shutting down after component failure"`) {
		t.Fatalf("failure not logged:\n%s", logs.String())
	}
}

func TestRunTreatsEarlyExitAsFailure(t *testing.T) {
	a, _ := newTestApp(t)
	a.Add("oneshot", Loop(func(context.Context) error { return nil }))
	err := waitErr(t, runAsync(context.Background(), a))
	if err == nil || !strings.Contains(err.Error(), "component oneshot: exited before shutdown") {
		t.Fatalf("want early-exit error, got %v", err)
	}
}

func TestRunRecoversPanics(t *testing.T) {
	a, _ := newTestApp(t)
	rec := &recorder{}
	a.Add("steady", waitLoop(rec, "steady"))
	a.Add("crashy", Loop(func(context.Context) error { panic("kaboom") }))
	err := waitErr(t, runAsync(context.Background(), a))
	if err == nil || !strings.Contains(err.Error(), "component crashy: panic: kaboom") {
		t.Fatalf("want panic error, got %v", err)
	}
	if got := rec.list(); !reflect.DeepEqual(got, []string{"steady stopped"}) {
		t.Fatalf("other components must still stop: %v", got)
	}
}

func TestRunGivesUpAfterShutdownTimeout(t *testing.T) {
	a, _ := newTestApp(t, "SHUTDOWN_TIMEOUT=100ms")
	release := make(chan struct{})
	defer close(release)
	a.Add("stubborn", Loop(func(context.Context) error {
		<-release
		return nil
	}))
	cleaned := false
	a.Cleanup("db", func(context.Context) error {
		cleaned = true
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, a)
	start := time.Now()
	cancel()
	err := waitErr(t, done)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("shutdown took %s despite a 100ms timeout", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "1 component(s) still running") {
		t.Fatalf("want timeout error, got %v", err)
	}
	if !cleaned {
		t.Fatal("cleanups must run even after a timeout")
	}
}

func TestRunReportsCleanupErrors(t *testing.T) {
	a, _ := newTestApp(t)
	a.Add("steady", waitLoop(&recorder{}, "steady"))
	a.Cleanup("db", func(context.Context) error { return errors.New("close failed") })
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, a)
	cancel()
	if err := waitErr(t, done); err == nil || !strings.Contains(err.Error(), "cleanup db: close failed") {
		t.Fatalf("want cleanup error, got %v", err)
	}
}

func TestRunWithoutComponents(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.Run(context.Background()); err == nil {
		t.Fatal("Run without components must fail")
	}
}

func TestRevision(t *testing.T) {
	cases := map[string]struct {
		settings []debug.BuildSetting
		want     string
	}{
		"no vcs info": {nil, "devel"},
		"clean": {[]debug.BuildSetting{
			{Key: "vcs.revision", Value: "46e4f77c0d27e9f59952bc2e2182d5e42c2a8f7f"},
			{Key: "vcs.modified", Value: "false"},
		}, "46e4f77c0d27"},
		"dirty": {[]debug.BuildSetting{
			{Key: "vcs.revision", Value: "46e4f77c0d27e9f59952bc2e2182d5e42c2a8f7f"},
			{Key: "vcs.modified", Value: "true"},
		}, "46e4f77c0d27-dirty"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := revision(tc.settings); got != tc.want {
				t.Fatalf("revision = %q, want %q", got, tc.want)
			}
		})
	}
}
