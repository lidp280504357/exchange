package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func opsGet(t *testing.T, base, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func readyStatus(t *testing.T, body string) string {
	t.Helper()
	var rep struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("bad readiness body %q: %v", body, err)
	}
	return rep.Status
}

func TestOpsEndpointsFollowTheLifecycle(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.setupOps(t.Context()); err != nil {
		t.Fatalf("setupOps: %v", err)
	}
	base := "http://" + a.components[0].Component.(*HTTPServer).Addr().String()

	stopping, release := make(chan struct{}), make(chan struct{})
	a.Add("worker", Loop(func(ctx context.Context) error {
		<-ctx.Done()
		close(stopping)
		<-release
		return nil
	}))

	ctx, cancel := context.WithCancel(t.Context())
	done := runAsync(ctx, a)

	// Readiness turns green once Run has started the components.
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body := opsGet(t, base, "/readyz")
		if code == http.StatusOK && readyStatus(t, body) == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never became ready: %d %s", code, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ := opsGet(t, base, "/healthz"); code != http.StatusOK {
		t.Fatalf("healthz = %d", code)
	}
	code, body := opsGet(t, base, "/metrics")
	if code != http.StatusOK || !strings.Contains(body, `exchange_build_info{service="test-svc"`) || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("metrics = %d\n%s", code, body)
	}
	if code, _ := opsGet(t, base, "/debug/pprof/"); code != http.StatusOK {
		t.Fatalf("pprof = %d", code)
	}

	// During shutdown the ops server keeps serving but reports draining,
	// because it was registered first and so stops last.
	cancel()
	<-stopping
	code, body = opsGet(t, base, "/readyz")
	if code != http.StatusServiceUnavailable || readyStatus(t, body) != "draining" {
		t.Fatalf("while draining: %d %s", code, body)
	}
	close(release)
	if err := waitErr(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
