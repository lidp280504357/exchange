package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketmaker/application"
	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/marketmaker/transport/httpapi"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/kafka"
)

type store struct {
	mu sync.Mutex
	s  *domain.StoredCaps
}

func (m *store) Caps(context.Context) (domain.StoredCaps, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.s == nil {
		return domain.StoredCaps{}, false, nil
	}
	return *m.s, true, nil
}

func (m *store) Seed(_ context.Context, c domain.Caps, actor string, at time.Time) (domain.StoredCaps, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.s == nil {
		m.s = &domain.StoredCaps{Caps: c, Version: 1, UpdatedBy: actor, UpdatedAt: at}
	}
	return *m.s, nil
}

func (m *store) Change(_ context.Context, c domain.CapsChange, at time.Time) (domain.StoredCaps, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.s.Version != c.Version {
		return domain.StoredCaps{}, domain.ErrCapsVersion
	}
	m.s = &domain.StoredCaps{Caps: c.Caps, Version: c.Version + 1, UpdatedBy: c.Actor, UpdatedAt: at}
	return *m.s, nil
}

func (m *store) Changes(context.Context, int) ([]domain.CapsRecord, error) {
	return []domain.CapsRecord{{Version: 1, Caps: m.s.Caps, Actor: "environment", At: time.Unix(0, 0)}}, nil
}

type specs struct{}

func (specs) Specs(context.Context) ([]domain.Spec, error) { return nil, nil }
func (specs) Backed(context.Context) ([]string, error)     { return nil, nil }

type house struct{}

func (house) Holdings(context.Context) (domain.Holdings, error) { return domain.Holdings{}, nil }
func (house) Contracts(context.Context, []string) (domain.ContractAccount, error) {
	return domain.ContractAccount{}, nil
}

type noFlags struct{}

func (noFlags) Enabled(string, flags.Subject) bool { return false }

type noKafka struct{}

func (noKafka) Publish(context.Context, ...kafka.Record) error { return nil }

func serve(t *testing.T, r http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	raw, _ := io.ReadAll(rec.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, raw)
	}
	return rec.Code, out
}

// The console reads HOUSE's caps and changes some of them on the version it
// read (review C45): the others stay, a stale version is 409, a bad
// decimal 400.
func TestTheCapsAPI(t *testing.T) {
	pub := application.New(application.DefaultConfig(), specs{}, house{}, noFlags{}, noKafka{}, event.NewFactory("market-maker", "test"),
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	caps := application.NewCaps(&store{}, pub, slog.New(slog.DiscardHandler))
	if err := caps.Start(context.Background(), application.DefaultConfig().Caps); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	(&httpapi.Handler{Caps: caps}).Routes(r)

	code, got := serve(t, r, http.MethodGet, "/internal/house/caps", "")
	if code != 200 || got["level"] != "20000" || got["contract_leverage"] != "10" || got["version"] != float64(1) || got["updated_by"] != "environment" {
		t.Fatalf("get: %d %v", code, got)
	}
	code, got = serve(t, r, http.MethodPut, "/internal/house/caps",
		`{"level":"500000000","total":"500000000","version":1,"actor":"admin:a","approver":"admin:b","approval_id":"ap1","reason":"the user's decision"}`)
	if code != 200 || got["level"] != "500000000" || got["total"] != "500000000" || got["symbol"] != "100000" || got["version"] != float64(2) {
		t.Fatalf("put: %d %v", code, got)
	}
	if c := caps.Get().Caps; !c.Level.Equal(decimal.NewFromInt(500000000)) {
		t.Fatalf("in force: %+v", c)
	}
	if code, got = serve(t, r, http.MethodPut, "/internal/house/caps", `{"level":"1","version":1,"actor":"a","reason":"r"}`); code != 409 ||
		got["code"] != "HOUSE_CAPS_VERSION" {
		t.Fatalf("a stale version: %d %v", code, got)
	}
	if code, _ = serve(t, r, http.MethodPut, "/internal/house/caps", `{"level":"lots","version":2,"actor":"a","reason":"r"}`); code != 400 {
		t.Fatalf("not a decimal: %d", code)
	}
	if code, _ = serve(t, r, http.MethodPut, "/internal/house/caps", `{"level":"-1","version":2,"actor":"a","reason":"r"}`); code != 400 {
		t.Fatalf("below zero: %d", code)
	}
	if code, _ = serve(t, r, http.MethodPut, "/internal/house/caps", `{"contract_leverage":"0","version":2,"actor":"a","reason":"r"}`); code != 400 {
		t.Fatalf("no leverage: %d", code)
	}
	if code, got = serve(t, r, http.MethodGet, "/internal/house/caps/changes", ""); code != 200 || len(got["items"].([]any)) != 1 {
		t.Fatalf("changes: %d %v", code, got)
	}
}
