package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/logging"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

func init() {
	// Install the W3C propagator and a tracer provider that issues IDs.
	_ = tracing.Setup()
}

func newTestRouter(t *testing.T) (http.Handler, *bytes.Buffer, *prometheus.Registry) {
	t.Helper()
	logs := &bytes.Buffer{}
	reg := prometheus.NewRegistry()
	r := NewRouter(RouterOptions{
		Logger:  logging.New(logs, logging.FormatJSON, slog.LevelInfo),
		Metrics: NewHTTPMetrics(reg),
	})
	r.Get("/v1/ok", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"client_ip": ClientIPFrom(r.Context()), "request_id": RequestIDFrom(r.Context())})
	})
	r.Get("/v1/fail", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance").WithDetail("asset", "USDT"))
	})
	r.Get("/v1/boom", func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	})
	r.Get("/v1/leak", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, errors.New("pq: password authentication failed for user exchange"))
	})
	return r, logs, reg
}

func do(t *testing.T, h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	req.RemoteAddr = "172.18.0.5:51000" // nginx container
	for k, v := range header {
		req.Header[k] = v
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) ErrorBody {
	t.Helper()
	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad error body %q: %v", rr.Body.String(), err)
	}
	return body
}

func TestErrorResponsesAreUnified(t *testing.T) {
	h, _, _ := newTestRouter(t)
	cases := []struct {
		method, path string
		status       int
		code         string
	}{
		{http.MethodGet, "/v1/fail", http.StatusUnprocessableEntity, "LEDGER_INSUFFICIENT_BALANCE"},
		{http.MethodGet, "/v1/nope", http.StatusNotFound, apperr.CodeNotFound},
		{http.MethodPost, "/v1/ok", http.StatusMethodNotAllowed, apperr.CodeMethodNotAllowed},
		{http.MethodGet, "/v1/boom", http.StatusInternalServerError, apperr.CodeInternal},
		{http.MethodGet, "/v1/leak", http.StatusInternalServerError, apperr.CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rr := do(t, h, tc.method, tc.path, nil)
			body := decodeError(t, rr)
			if rr.Code != tc.status || body.Code != tc.code {
				t.Fatalf("got %d %+v, want %d %s", rr.Code, body, tc.status, tc.code)
			}
			if len(body.TraceID) != 32 || rr.Header().Get(HeaderTraceID) != body.TraceID {
				t.Fatalf("trace id missing or inconsistent: body %q header %q", body.TraceID, rr.Header().Get(HeaderTraceID))
			}
			if strings.Contains(rr.Body.String(), "password authentication") || strings.Contains(rr.Body.String(), "kaboom") {
				t.Fatalf("internal detail leaked: %s", rr.Body.String())
			}
		})
	}
	rr := do(t, h, http.MethodGet, "/v1/fail", nil)
	if decodeError(t, rr).Details["asset"] != "USDT" {
		t.Fatalf("details lost: %s", rr.Body.String())
	}
}

func TestRequestID(t *testing.T) {
	h, _, _ := newTestRouter(t)
	rr := do(t, h, http.MethodGet, "/v1/ok", http.Header{HeaderRequestID: {"client-req_1.2:3"}})
	if rr.Header().Get(HeaderRequestID) != "client-req_1.2:3" || !strings.Contains(rr.Body.String(), "client-req_1.2:3") {
		t.Fatalf("well-formed id must be kept: %s %s", rr.Header().Get(HeaderRequestID), rr.Body.String())
	}
	for _, bad := range []string{"", "has space", strings.Repeat("a", 129), "<script>"} {
		rr := do(t, h, http.MethodGet, "/v1/ok", http.Header{HeaderRequestID: {bad}})
		got := rr.Header().Get(HeaderRequestID)
		if got == bad || len(got) != 36 {
			t.Fatalf("id %q must be replaced by a UUID, got %q", bad, got)
		}
	}
}

func TestTraceContinuesCallerTrace(t *testing.T) {
	h, _, _ := newTestRouter(t)
	rr := do(t, h, http.MethodGet, "/v1/ok", http.Header{"Traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}})
	if got := rr.Header().Get(HeaderTraceID); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %q", got)
	}
	a := do(t, h, http.MethodGet, "/v1/ok", nil).Header().Get(HeaderTraceID)
	b := do(t, h, http.MethodGet, "/v1/ok", nil).Header().Get(HeaderTraceID)
	if len(a) != 32 || a == b {
		t.Fatalf("new traces must get fresh ids: %q %q", a, b)
	}
}

func TestClientIP(t *testing.T) {
	trusted := DefaultTrustedProxies
	mw := ClientIP(trusted)
	cases := []struct {
		name, remote string
		header       http.Header
		want         string
	}{
		{"direct", "203.0.113.7:1234", nil, "203.0.113.7"},
		{"untrusted peer cannot spoof", "203.0.113.7:1234", http.Header{HeaderRealIP: {"1.1.1.1"}}, "203.0.113.7"},
		{"nginx real ip", "172.18.0.5:1234", http.Header{HeaderRealIP: {"198.51.100.9"}}, "198.51.100.9"},
		{"forwarded chain", "172.18.0.5:1234", http.Header{HeaderForwarded: {"1.2.3.4, 198.51.100.9, 10.0.0.2"}}, "198.51.100.9"},
		{"ipv6 peer", "[2001:db8::1]:443", nil, "2001:db8::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ClientIPFrom(r.Context()) }))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = tc.remote
			for k, v := range tc.header {
				req.Header[k] = v
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Fatalf("client ip = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAccessLogAndMetrics(t *testing.T) {
	h, logs, reg := newTestRouter(t)
	do(t, h, http.MethodGet, "/v1/ok", http.Header{HeaderRealIP: {"198.51.100.9"}})
	do(t, h, http.MethodGet, "/v1/fail", nil)
	do(t, h, http.MethodGet, "/v1/unknown/path/123", nil)

	var line map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(logs.String(), "\n", 2)[0]), &line); err != nil {
		t.Fatalf("bad log line: %v\n%s", err, logs.String())
	}
	if line["msg"] != "http request" || line["route"] != "/v1/ok" || line["status"] != float64(200) ||
		line["client_ip"] != "198.51.100.*" || line["request_id"] == nil || line["trace_id"] == nil {
		t.Fatalf("unexpected access log: %v", line)
	}

	if got := requestCount(t, reg, "/v1/fail", "422"); got != 1 {
		t.Fatalf("fail counter = %v", got)
	}
	if got := requestCount(t, reg, "unmatched", "404"); got != 1 {
		t.Fatalf("unmatched routes must share one label: %v", got)
	}
}

func requestCount(t *testing.T, reg *prometheus.Registry, route, status string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "http_server_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["route"] == route && labels["status"] == status {
				return m.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("no counter for route %s status %s", route, status)
	return 0
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	cases := map[string]struct {
		body string
		ok   bool
	}{
		"valid":          {`{"name":"a","count":1}`, true},
		"unknown field":  {`{"name":"a","extra":1}`, false},
		"wrong type":     {`{"count":"one"}`, false},
		"malformed":      {`{"name":`, false},
		"empty":          {``, false},
		"trailing data":  {`{"name":"a"}{"name":"b"}`, false},
		"trailing space": {"{\"name\":\"a\"}\n  ", true},
		"too large":      {`{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", io.NopCloser(strings.NewReader(tc.body)))
			var p payload
			err := DecodeJSON(httptest.NewRecorder(), req, &p)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !apperr.Is(err, apperr.CodeInvalidArgument) {
				t.Fatalf("decode errors must be COMMON_INVALID_ARGUMENT, got %v", err)
			}
		})
	}
}
