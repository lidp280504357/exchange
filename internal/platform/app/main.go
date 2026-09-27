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
)

// SetupFunc loads a service's own settings and registers its components and
// cleanups. ctx is canceled when the process is signaled during setup.
type SetupFunc func(ctx context.Context, a *App) error

// Main runs the named service and exits the process: status 0 after a
// requested, clean shutdown and 1 on any failure.
func Main(name string, setup SetupFunc) {
	os.Exit(run(name, setup))
}

func run(name string, setup SetupFunc) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Restore the default handlers as soon as shutdown starts, so that a
	// second signal kills a shutdown that hangs.
	context.AfterFunc(ctx, stop)

	a, err := New(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 1
	}
	slog.SetDefault(a.log)
	a.log.Info("service starting", "env", a.cfg.Env, "version", version())

	if err := setup(ctx, a); err != nil {
		a.log.Error("setup failed", "error", err)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.ShutdownTimeout)
		defer cancel()
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

// version reports the VCS revision the binary was built from.
func version() string {
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
