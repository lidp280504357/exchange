package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func probe(t *testing.T, h http.Handler) (int, Report) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", http.NoBody))
	var rep Report
	if err := json.Unmarshal(rr.Body.Bytes(), &rep); err != nil {
		t.Fatalf("bad body %q: %v", rr.Body.String(), err)
	}
	return rr.Code, rep
}

func TestReadinessFollowsLifecycle(t *testing.T) {
	r := New()
	r.Add("db", func(context.Context) error { return nil })

	if code, rep := probe(t, r.ReadinessHandler()); code != http.StatusServiceUnavailable || rep.Status != "starting" {
		t.Fatalf("starting: %d %+v", code, rep)
	}
	r.SetReady()
	if code, rep := probe(t, r.ReadinessHandler()); code != http.StatusOK || rep.Status != "ready" || rep.Checks["db"] != "ok" {
		t.Fatalf("ready: %d %+v", code, rep)
	}
	r.SetDraining()
	r.SetReady() // must not undo draining
	if code, rep := probe(t, r.ReadinessHandler()); code != http.StatusServiceUnavailable || rep.Status != "draining" {
		t.Fatalf("draining: %d %+v", code, rep)
	}
	if code, rep := probe(t, r.LivenessHandler()); code != http.StatusOK || rep.Status != "ok" {
		t.Fatalf("liveness while draining: %d %+v", code, rep)
	}
}

func TestReadinessReportsFailingChecks(t *testing.T) {
	r := New()
	r.Add("db", func(context.Context) error { return nil })
	r.Add("redis", func(context.Context) error { return errors.New("dial tcp: refused") })
	r.SetReady()
	code, rep := probe(t, r.ReadinessHandler())
	if code != http.StatusServiceUnavailable || rep.Status != "unready" {
		t.Fatalf("got %d %+v", code, rep)
	}
	if rep.Checks["db"] != "ok" || rep.Checks["redis"] != "dial tcp: refused" {
		t.Fatalf("checks = %v", rep.Checks)
	}
}

func TestReadinessChecksTimeOut(t *testing.T) {
	r := New()
	r.Add("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	r.SetReady()
	r.timeout = 50 * time.Millisecond
	start := time.Now()
	_, ok := r.Ready(t.Context())
	if ok || time.Since(start) > time.Second {
		t.Fatalf("slow check must fail within the timeout: ok=%v after %s", ok, time.Since(start))
	}
}
