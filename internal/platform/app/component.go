package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Component is a long-running part of a service, such as a server or a
// consumer loop.
type Component interface {
	// Run blocks until the component stops. It returns nil after Stop and an
	// error when the component fails on its own.
	Run() error
	// Stop makes Run return, letting in-flight work finish until ctx is done.
	Stop(ctx context.Context) error
}

// HTTP server timeouts. Handlers that stream or upgrade the connection manage
// their own deadlines.
const (
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 30 * time.Second
	httpIdleTimeout       = 2 * time.Minute
)

// HTTPServer runs an http.Server as a Component.
type HTTPServer struct {
	srv *http.Server
	ln  net.Listener
	log *slog.Logger
}

// NewHTTPServer binds addr right away, so that a busy port fails setup
// instead of surfacing after the service has reported that it started.
func NewHTTPServer(ctx context.Context, addr string, h http.Handler, log *slog.Logger) (*HTTPServer, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	return &HTTPServer{
		srv: &http.Server{
			Handler:           h,
			ReadHeaderTimeout: httpReadHeaderTimeout,
			ReadTimeout:       httpReadTimeout,
			WriteTimeout:      httpWriteTimeout,
			IdleTimeout:       httpIdleTimeout,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
		ln:  ln,
		log: log,
	}, nil
}

// Addr returns the address the server listens on.
func (s *HTTPServer) Addr() net.Addr { return s.ln.Addr() }

// Run serves requests until Stop.
func (s *HTTPServer) Run() error {
	s.log.Info("http server listening", "addr", s.ln.Addr().String())
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http: %w", err)
	}
	return nil
}

// Stop closes the listener and waits for in-flight requests to finish. When
// ctx ends first, it closes the remaining connections and returns ctx's error.
func (s *HTTPServer) Stop(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if err != nil {
		_ = s.srv.Close()
	}
	// Serve closes the listener, but Run may never have been called.
	_ = s.ln.Close()
	return err
}

// Loop turns fn into a Component. fn runs until the context it receives is
// canceled, which is how Stop asks it to return; returning that context's
// error then counts as a clean stop.
func Loop(fn func(ctx context.Context) error) Component {
	ctx, cancel := context.WithCancel(context.Background())
	return &loop{fn: fn, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

type loop struct {
	fn     func(ctx context.Context) error
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func (l *loop) Run() error {
	defer close(l.done)
	err := l.fn(l.ctx)
	if l.ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (l *loop) Stop(ctx context.Context) error {
	l.cancel()
	select {
	case <-l.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
