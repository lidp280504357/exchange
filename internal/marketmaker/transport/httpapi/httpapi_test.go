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
	"github.com/skill/exchange/internal/platform/svcsign"
)

type store struct {
	mu   sync.Mutex
	s    *domain.StoredCaps
	last domain.CapsChange
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
	caps, err := c.Next(m.s.Caps)
	if err != nil {
		return domain.StoredCaps{}, err
	}
	m.last = c
	m.s = &domain.StoredCaps{Caps: caps, Version: c.Version + 1, UpdatedBy: c.Actor, UpdatedAt: at}
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

var (
	opsSecret   = []byte(strings.Repeat("o", svcsign.MinSecret))
	adminSecret = []byte(strings.Repeat("a", svcsign.MinSecret))
)

// serve sends a request, signed with the key named (none: unsigned), and
// returns the status and the JSON answer.
func serve(t *testing.T, r http.Handler, key, method, path, body string) (int, map[string]any) {
	t.Helper()
	code, out, _ := serveHeader(t, r, key, "", method, path, body)
	return code, out
}

// serveHeader is serve with a signature header given (header) or made
// (key); it returns the header sent.
func serveHeader(t *testing.T, r http.Handler, key, header, method, path, body string) (int, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	switch {
	case header != "":
		req.Header.Set(svcsign.Header, header)
	case key == httpapi.KeyOps:
		svcsign.SignRequest(req, key, opsSecret, []byte(body), time.Now())
	case key == httpapi.KeyAdmin:
		svcsign.SignRequest(req, key, adminSecret, []byte(body), time.Now())
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, raw)
	}
	return rec.Code, out, req.Header.Get(svcsign.Header)
}

// The console reads HOUSE's caps and changes some of them on the version it
// read (review C45): the others stay, a stale version is 409, a bad
// decimal 400. A change must be signed, and only the admin console's key
// names an approver (review FL, C47); each change keeps the key. Caps out
// of bounds, a step of more than ten times and a change of nothing are
// 400.
func TestTheCapsAPI(t *testing.T) {
	pub := application.New(application.DefaultConfig(), specs{}, house{}, noFlags{}, noKafka{}, event.NewFactory("market-maker", "test"),
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	st := &store{}
	caps := application.NewCaps(st, pub, slog.New(slog.DiscardHandler))
	if err := caps.Start(context.Background(), application.DefaultConfig().Caps); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	signed := &svcsign.Verifier{Keys: map[string][]byte{httpapi.KeyOps: opsSecret, httpapi.KeyAdmin: adminSecret}}
	(&httpapi.Handler{Caps: caps, Signed: signed}).Routes(r)

	code, got := serve(t, r, "", http.MethodGet, "/internal/house/caps", "")
	if code != 200 || got["level"] != "20000" || got["contract_leverage"] != "10" || got["version"] != float64(1) || got["updated_by"] != "environment" {
		t.Fatalf("get: %d %v", code, got)
	}
	approved := `{"level":"200000","total":"5000000","version":1,"actor":"admin:a","approver":"admin:b","approval_id":"ap1","reason":"the user's decision"}`
	if code, got = serve(t, r, "", http.MethodPut, "/internal/house/caps", approved); code != 401 || got["code"] != "SERVICE_UNSIGNED" {
		t.Fatalf("unsigned: %d %v", code, got)
	}
	if code, got = serve(t, r, httpapi.KeyOps, http.MethodPut, "/internal/house/caps", approved); code != 403 ||
		got["code"] != "HOUSE_CAPS_APPROVAL_NEEDS_ADMIN" {
		t.Fatalf("an approver signed by ops: %d %v", code, got)
	}
	code, got, header := serveHeader(t, r, httpapi.KeyAdmin, "", http.MethodPut, "/internal/house/caps", approved)
	if code != 200 || got["level"] != "200000" || got["total"] != "5000000" || got["symbol"] != "100000" || got["version"] != float64(2) {
		t.Fatalf("put: %d %v", code, got)
	}
	if c := caps.Get().Caps; !c.Level.Equal(decimal.NewFromInt(200000)) {
		t.Fatalf("in force: %+v", c)
	}
	if st.last.SignedBy != httpapi.KeyAdmin || st.last.Approver != "admin:b" {
		t.Fatalf("kept: %+v", st.last)
	}
	if code, got, _ = serveHeader(t, r, "", header, http.MethodPut, "/internal/house/caps", approved); code != 401 {
		t.Fatalf("the same signature again: %d %v", code, got)
	}
	// The ops key cannot pass for the admin's (review FR).
	forged := svcsign.Sign(httpapi.KeyAdmin, opsSecret, http.MethodPut, "/internal/house/caps", []byte(approved), time.Now())
	if code, got, _ = serveHeader(t, r, "", forged, http.MethodPut, "/internal/house/caps", approved); code != 401 || got["code"] != "SERVICE_UNSIGNED" {
		t.Fatalf("ops signing as admin: %d %v", code, got)
	}
	if code, got = serve(t, r, httpapi.KeyOps, http.MethodPut, "/internal/house/caps", `{"level":"1","version":1,"actor":"a","reason":"r"}`); code != 409 ||
		got["code"] != "HOUSE_CAPS_VERSION" {
		t.Fatalf("a stale version: %d %v", code, got)
	}
	if code, _ = serve(t, r, httpapi.KeyOps, http.MethodPut, "/internal/house/caps", `{"level":"lots","version":2,"actor":"a","reason":"r"}`); code != 400 {
		t.Fatalf("not a decimal: %d", code)
	}
	if code, _ = serve(t, r, httpapi.KeyOps, http.MethodPut, "/internal/house/caps", `{"level":"-1","version":2,"actor":"a","reason":"r"}`); code != 400 {
		t.Fatalf("below zero: %d", code)
	}
	for _, bad := range []string{`"contract_leverage":"0"`, `"contract_leverage":"1001"`, `"symbol":"0"`, `"safety":"0"`, `"total":"2e15"`} {
		if code, got = serve(t, r, httpapi.KeyOps, http.MethodPut, "/internal/house/caps", `{`+bad+`,"version":2,"actor":"a","reason":"r"}`); code != 400 ||
			got["code"] != "COMMON_INVALID_ARGUMENT" {
			t.Fatalf("out of bounds %s: %d %v", bad, code, got)
		}
	}
	if code, got = serve(t, r, httpapi.KeyOps, http.MethodPut, "/internal/house/caps", `{"symbol":"1000001","version":2,"actor":"a","reason":"r"}`); code != 400 ||
		got["code"] != "HOUSE_CAPS_STEP" {
		t.Fatalf("more than ten times: %d %v", code, got)
	}
	if code, got = serve(t, r, httpapi.KeyOps, http.MethodPut, "/internal/house/caps", `{"version":2,"actor":"a","reason":"r"}`); code != 400 {
		t.Fatalf("nothing to change: %d %v", code, got)
	}
	if code, got = serve(t, r, httpapi.KeyOps, http.MethodPut, "/internal/house/caps", `{"safety":"2000","version":2,"actor":"ops:x","reason":"r"}`); code != 200 ||
		got["version"] != float64(3) || st.last.SignedBy != httpapi.KeyOps {
		t.Fatalf("signed by ops: %d %v %+v", code, got, st.last)
	}
	if code, got = serve(t, r, "", http.MethodGet, "/internal/house/caps/changes", ""); code != 200 || len(got["items"].([]any)) != 1 {
		t.Fatalf("changes: %d %v", code, got)
	}
}
