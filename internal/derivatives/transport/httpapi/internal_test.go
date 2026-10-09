package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/derivatives/transport/httpapi"
	"github.com/skill/exchange/internal/platform/httpx"
)

// A request carrying a caller's identity came through the gateway: no
// internal route serves it, as spot-trading-service's do not (review C63
// ①, api/internal/products.yaml).
func TestInternalRoutesRefuseACallersIdentity(t *testing.T) {
	r := chi.NewRouter()
	(&httpapi.Handler{}).InternalRoutes(r)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/internal/products/usdt_m"},
		{http.MethodPost, "/internal/products/coin_m/cancel-open"},
		{http.MethodGet, "/internal/derivatives/contracts"},
		{http.MethodPost, "/internal/derivatives/positions/close"},
	} {
		req := httptest.NewRequestWithContext(context.Background(), c.method, c.path, strings.NewReader("{}"))
		req.Header.Set(httpx.HeaderUserID, "01a0ee73-9d3a-79c1-a9b8-8cc210950404")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s with a caller's identity: %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
}

// The console's lists take user_ids or exclude_user_ids (review L3), not
// both, UUIDs only: refused before anything is read.
func TestTheListsTakeUsersToShowOrLeaveOut(t *testing.T) {
	r := chi.NewRouter()
	(&httpapi.Handler{}).InternalRoutes(r)
	a, b := "01a0ee73-9d3a-79c1-a9b8-8cc210950404", "01a0ee73-9d3a-79c1-a9b8-8cc210950405"
	for _, path := range []string{
		"/internal/derivatives/positions?user_ids=" + a + "&exclude_user_ids=" + b,
		"/internal/derivatives/positions?exclude_user_ids=bob",
		"/internal/derivatives/risk?user_ids=" + a + "," + "nope",
		"/internal/derivatives/risk?user_ids=" + a + "&exclude_user_ids=",
		"/internal/derivatives/positions?user_id=bob&exclude_user_ids=" + a,
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "COMMON_INVALID_ARGUMENT") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	// The POST .../list variants take the accounts in their body (review
	// C76), up to 5,000: not in the query string, one set, UUIDs.
	for _, c := range []struct{ path, body string }{
		{"/internal/derivatives/positions/list?user_ids=" + a, `{}`},
		{"/internal/derivatives/positions/list", `{"user_ids":["` + a + `"],"exclude_user_ids":["` + b + `"]}`},
		{"/internal/derivatives/risk/list", `{"exclude_user_ids":["bob"]}`},
		{"/internal/derivatives/risk/list", `[]`},
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("POST %s %s: %d %s", c.path, c.body, w.Code, w.Body)
		}
	}
}
