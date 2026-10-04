package backends

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// TestMarketSimTargets checks how the console reads a threshold target's
// plan and previews one (ASTRA A6): the paths and the query market-sim
// takes, its answer as it is, and its refusals as their apperr.
func TestMarketSimTargets(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/internal/sim/events/0192a000-0000-7000-8000-00000000a6a6/plan":
			_, _ = w.Write([]byte(`{"event_id":"0192a000-0000-7000-8000-00000000a6a6","points":[]}`))
		case "/internal/sim/target-preview":
			if r.URL.Query().Get("price") == "9" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"SIM_TARGET_INFEASIBLE","message":"too short","details":{"min_duration_seconds":660}}`))
				return
			}
			_, _ = w.Write([]byte(`{"feasible":true,"min_duration_seconds":120,"move":0.03,"needs_approval":false,"points":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"COMMON_NOT_FOUND","message":"no such target"}`))
		}
	}))
	defer srv.Close()
	m := MarketSim{REST: REST{Client: &http.Client{Timeout: 5 * time.Second}}, Base: srv.URL}
	ctx := context.Background()

	raw, err := m.Plan(ctx, "0192a000-0000-7000-8000-00000000a6a6")
	if err != nil || string(raw) != `{"event_id":"0192a000-0000-7000-8000-00000000a6a6","points":[]}` {
		t.Fatalf("the plan %s %v", raw, err)
	}
	if _, err := m.Plan(ctx, "0192a000-0000-7000-8000-00000000ffff"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("no such target: %v", err)
	}
	q := ports.SimTargetQuery{Direction: "ABOVE", Price: "0.6", DurationSeconds: 1800, StartsAt: "2026-10-05T04:00:00Z"}
	if raw, err := m.TargetPreview(ctx, q); err != nil || string(raw) == "" {
		t.Fatalf("the preview %s %v", raw, err)
	}
	if _, err := m.TargetPreview(ctx, ports.SimTargetQuery{Price: "9", DurationSeconds: 60}); !apperr.Is(err, "SIM_TARGET_INFEASIBLE") {
		t.Fatalf("market-sim's refusal: %v", err)
	}
	want := []string{
		"GET /internal/sim/events/0192a000-0000-7000-8000-00000000a6a6/plan",
		"GET /internal/sim/events/0192a000-0000-7000-8000-00000000ffff/plan",
		"GET /internal/sim/target-preview?direction=ABOVE&duration_seconds=1800&price=0.6&starts_at=2026-10-05T04%3A00%3A00Z",
		"GET /internal/sim/target-preview?duration_seconds=60&price=9",
	}
	if len(got) != len(want) {
		t.Fatalf("asked %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("asked %q, want %q", got[i], want[i])
		}
	}
}
