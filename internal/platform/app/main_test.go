//go:build unix

package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperModeEnv makes the test binary act as a service process, so that the
// tests below can exercise Main with real signals and exit codes.
const helperModeEnv = "APP_TEST_HELPER_MODE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperModeEnv); mode != "" {
		helperMain(mode)
	}
	os.Exit(m.Run())
}

func helperMain(mode string) {
	Main("helper", func(_ context.Context, a *App) error {
		switch mode {
		case "serve":
			a.Add("loop", Loop(func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			}))
		case "fail":
			a.Add("loop", Loop(func(context.Context) error { return errors.New("boom") }))
		case "hang":
			a.Add("loop", Loop(func(context.Context) error {
				time.Sleep(time.Hour) // ignores Stop
				return nil
			}))
		case "setup-error":
			a.Cleanup("resource", func(context.Context) error {
				a.Logger().Info("cleanup ran")
				return nil
			})
			return errors.New("bad setup")
		}
		return nil
	})
}

type helper struct {
	cmd    *exec.Cmd
	stdout *syncBuffer
	stderr *syncBuffer
}

// startHelper runs this test binary as a service. env replaces the
// variables the helper cares about; everything else is inherited.
func startHelper(t *testing.T, mode string, env ...string) *helper {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	h := &helper{stdout: &syncBuffer{}, stderr: &syncBuffer{}}
	h.cmd = exec.CommandContext(ctx, os.Args[0]) //nolint:gosec // re-executes the test binary itself
	h.cmd.Env = append(inheritedEnv(), helperModeEnv+"="+mode)
	h.cmd.Env = append(h.cmd.Env, env...)
	h.cmd.Stdout, h.cmd.Stderr = h.stdout, h.stderr
	if err := h.cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	return h
}

func inheritedEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "APP_ENV", "LOG_LEVEL", "LOG_FORMAT", "SHUTDOWN_TIMEOUT", helperModeEnv:
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (h *helper) waitForLog(t *testing.T, substr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.stdout.String(), substr) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s\nstdout:\n%s\nstderr:\n%s", substr, h.stdout, h.stderr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *helper) signal(t *testing.T, sig os.Signal) {
	t.Helper()
	if err := h.cmd.Process.Signal(sig); err != nil {
		t.Fatalf("signal: %v", err)
	}
}

// wait returns the exit code, or -1 when a signal killed the process.
func (h *helper) wait(t *testing.T) int {
	t.Helper()
	return exitCode(t, h.cmd.Wait())
}

func exitCode(t *testing.T, waitErr error) int {
	t.Helper()
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("wait: %v", waitErr)
	}
	return exitErr.ExitCode()
}

var serviceEnv = []string{"APP_ENV=test", "LOG_FORMAT=json", "SHUTDOWN_TIMEOUT=5s"}

func TestMainShutsDownCleanlyOnSIGTERM(t *testing.T) {
	h := startHelper(t, "serve", serviceEnv...)
	h.waitForLog(t, `"msg":"service started"`)
	h.signal(t, syscall.SIGTERM)

	if code := h.wait(t); code != 0 {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, h.stdout, h.stderr)
	}
	out := h.stdout.String()
	for _, s := range []string{`"msg":"service starting"`, `"reason":"terminated signal received"`, `"msg":"shutdown complete"`} {
		if !strings.Contains(out, s) {
			t.Fatalf("log missing %s:\n%s", s, out)
		}
	}
}

func TestMainExitsNonZeroWhenComponentFails(t *testing.T) {
	h := startHelper(t, "fail", serviceEnv...)
	if code := h.wait(t); code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, h.stdout)
	}
	if !strings.Contains(h.stdout.String(), "component loop: boom") {
		t.Fatalf("failure not logged:\n%s", h.stdout)
	}
}

func TestMainRunsCleanupsWhenSetupFails(t *testing.T) {
	h := startHelper(t, "setup-error", serviceEnv...)
	if code := h.wait(t); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	out := h.stdout.String()
	if !strings.Contains(out, `"msg":"setup failed"`) || !strings.Contains(out, `"msg":"cleanup ran"`) {
		t.Fatalf("setup failure or cleanup not logged:\n%s", out)
	}
}

func TestMainReportsConfigErrorsOnStderr(t *testing.T) {
	h := startHelper(t, "serve") // no APP_ENV
	if code := h.wait(t); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "helper: config: APP_ENV is required") {
		t.Fatalf("stderr = %q", h.stderr)
	}
}

func TestMainSecondSignalKillsHungShutdown(t *testing.T) {
	h := startHelper(t, "hang", "APP_ENV=test", "LOG_FORMAT=json", "SHUTDOWN_TIMEOUT=1m")
	h.waitForLog(t, `"msg":"service started"`)
	h.signal(t, syscall.SIGTERM)
	h.waitForLog(t, `"msg":"shutting down"`)

	// The default handler is restored right after the first signal; repeat
	// until it has been, then the process dies of the signal.
	exited := make(chan error, 1)
	go func() { exited <- h.cmd.Wait() }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case err := <-exited:
			if code := exitCode(t, err); code != -1 {
				t.Fatalf("exit code %d, want death by signal", code)
			}
			if strings.Contains(h.stdout.String(), `"msg":"shutdown complete"`) {
				t.Fatal("hung shutdown must not complete")
			}
			return
		case <-ticker.C:
			_ = h.cmd.Process.Signal(syscall.SIGTERM)
		case <-timeout:
			t.Fatal("second SIGTERM did not kill the process")
		}
	}
}
