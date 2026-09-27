package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

var discard = slog.New(slog.DiscardHandler)

func startHTTPServer(t *testing.T, h http.Handler) (*HTTPServer, <-chan error) {
	t.Helper()
	srv, err := NewHTTPServer(t.Context(), "127.0.0.1:0", h, discard)
	if err != nil {
		t.Fatalf("NewHTTPServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Run() }()
	return srv, done
}

type response struct {
	status int
	body   string
	err    error
}

func get(ctx context.Context, url string) response {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return response{err: err}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return response{err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: string(body), err: err}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

func TestHTTPServerStopDrainsInFlightRequests(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv, runDone := startHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "done")
	}))
	addr := srv.Addr().String()

	inFlight := make(chan response, 1)
	go func() { inFlight <- get(t.Context(), "http://"+addr+"/") }()
	receive(t, entered)

	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		stopDone <- srv.Stop(ctx)
	}()

	// Once Stop has closed the listener, new connections are refused while
	// the in-flight request is still being served.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var d net.Dialer
		conn, err := d.DialContext(t.Context(), "tcp", addr)
		if err != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting after Stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned before the in-flight request finished: %v", err)
	default:
	}

	close(release)
	if r := receive(t, inFlight); r.err != nil || r.status != http.StatusOK || r.body != "done" {
		t.Fatalf("in-flight request not completed: %+v", r)
	}
	if err := receive(t, stopDone); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := receive(t, runDone); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestHTTPServerStopDeadlineClosesConnections(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	srv, runDone := startHTTPServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))

	inFlight := make(chan response, 1)
	go func() { inFlight <- get(t.Context(), "http://"+srv.Addr().String()+"/") }()
	receive(t, entered)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := srv.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if r := receive(t, inFlight); r.err == nil {
		t.Fatalf("connection should have been closed, got %+v", r)
	}
	if err := receive(t, runDone); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestHTTPServerStopBeforeRunReleasesPort(t *testing.T) {
	srv, err := NewHTTPServer(t.Context(), "127.0.0.1:0", http.NotFoundHandler(), discard)
	if err != nil {
		t.Fatalf("NewHTTPServer: %v", err)
	}
	addr := srv.Addr().String()
	if err := srv.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := srv.Run(); err != nil {
		t.Fatalf("Run after Stop must return nil, got %v", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("port still held: %v", err)
	}
	_ = ln.Close()
}

func TestNewHTTPServerFailsOnBusyPort(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := NewHTTPServer(t.Context(), ln.Addr().String(), http.NotFoundHandler(), discard); err == nil {
		t.Fatal("binding a busy port must fail")
	}
}

func TestLoopStopCancelsAndWaits(t *testing.T) {
	finished := make(chan struct{})
	l := Loop(func(ctx context.Context) error {
		<-ctx.Done()
		close(finished)
		return fmt.Errorf("poll: %w", ctx.Err())
	})
	runDone := make(chan error, 1)
	go func() { runDone <- l.Run() }()

	if err := l.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before fn finished")
	}
	if err := receive(t, runDone); err != nil {
		t.Fatalf("a canceled loop is a clean stop, got %v", err)
	}
}

func TestLoopReturnsOwnError(t *testing.T) {
	boom := errors.New("boom")
	l := Loop(func(context.Context) error { return boom })
	if err := l.Run(); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if err := l.Stop(t.Context()); err != nil {
		t.Fatalf("Stop after exit: %v", err)
	}
}

func TestLoopCanceledErrorWithoutStopIsAFailure(t *testing.T) {
	l := Loop(func(context.Context) error { return context.Canceled })
	if err := l.Run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("an unrequested cancellation must surface, got %v", err)
	}
}

func TestLoopStopHonorsDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	l := Loop(func(context.Context) error {
		<-release
		return nil
	})
	go func() { _ = l.Run() }()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := l.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}
