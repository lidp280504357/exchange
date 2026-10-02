package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

// SetupFunc loads a service's own settings and registers its components and
// cleanups. ctx is canceled when the process is signaled during setup.
type SetupFunc func(ctx context.Context, a *App) error

// Main runs the named service and exits the process: status 0 after a
// requested, clean shutdown and 1 on any failure. Besides the components
// registered by setup, every service serves the ops endpoints on OPS_ADDR.
func Main(name string, setup SetupFunc, opts ...Option) {
	os.Exit(run(name, setup, opts...))
}

func run(name string, setup SetupFunc, opts ...Option) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Restore the default handlers as soon as shutdown starts, so that a
	// second signal kills a shutdown that hangs.
	context.AfterFunc(ctx, stop)

	a, err := New(name, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 1
	}
	slog.SetDefault(a.log)
	a.log.Info("service starting", "env", a.cfg.Env, "version", version(), "instance", a.cfg.InstanceID)
	a.Cleanup("tracing", tracing.Setup())

	if err = a.setupOps(ctx); err != nil {
		err = fmt.Errorf("ops server: %w", err)
	} else {
		err = setup(ctx, a)
	}
	if err != nil {
		a.log.Error("setup failed", "error", err)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.ShutdownTimeout)
		defer cancel()
		if a.ops != nil {
			_ = a.ops.Stop(cleanupCtx)
		}
		if err := errors.Join(a.runCleanups(cleanupCtx)...); err != nil {
			a.log.Error("cleanup after failed setup", "error", err)
		}
		return 1
	}
	if err := a.Run(ctx); err != nil {
		return 1
	}
	return 0
}

// setupOps starts the ops server before setup, so that /healthz answers
// and /readyz says "starting" while setup works (an engine's recovery can
// take minutes) instead of leaving probes hanging; registered first, it
// stops last.
func (a *App) setupOps(ctx context.Context) error {
	srv, err := NewHTTPServer(ctx, a.cfg.OpsAddr, a.opsHandler(), a.log.With("server", "ops"))
	if err != nil {
		return err
	}
	a.ops = startEarly(srv)
	a.Add("ops", a.ops)
	return nil
}

// early is a component started before Run: Run waits for it to stop.
type early struct {
	c    Component
	done chan error
}

func startEarly(c Component) *early {
	e := &early{c: c, done: make(chan error, 1)}
	go func() { e.done <- runGuarded(c) }()
	return e
}

func (e *early) Run() error                     { return <-e.done }
func (e *early) Stop(ctx context.Context) error { return e.c.Stop(ctx) }

// buildVersion is set at link time by the image build, which has no .git:
// -ldflags "-X github.com/lidp280504357/exchange/internal/platform/app.buildVersion=<sha>".
var buildVersion string

// version reports the VCS revision the binary was built from.
func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return revision(info.Settings)
}

func revision(settings []debug.BuildSetting) string {
	rev, dirty := "devel", ""
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	return rev + dirty
}
