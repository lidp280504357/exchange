package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/trading/application"
)

// serve answers one request with the order routes over a service that has
// nothing behind it: the requests below are refused before they reach it.
func serve(t *testing.T, path, body string, header http.Header) (int, string) {
	t.Helper()
	r := chi.NewRouter()
	(&Handler{Svc: &application.Service{}}).Routes(r)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var answer struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &answer)
	return w.Code, answer.Code
}

func TestInternalRoutesRefuseWhatTheyCannotServe(t *testing.T) {
	const (
		liquidation = `{"liquidation_id":"0199b0a0-0000-7000-8000-000000000001","user_id":"0199b0a0-0000-7000-8000-0000000000aa",` +
			`"account":"MARGIN_ISOLATED","symbol":"BTC-USDT","side":"SELL","quantity":"0.1"`
		cancel = `{"user_id":"0199b0a0-0000-7000-8000-0000000000aa","account":"MARGIN_CROSS"`
	)
	viaGateway := http.Header{http.CanonicalHeaderKey(httpx.HeaderUserID): {"0199b0a0-0000-7000-8000-0000000000aa"}}
	for _, c := range []struct {
		name, path, body string
		header           http.Header
		status           int
		code             string
	}{
		// A request that came through the gateway carries its caller.
		{"liquidation via the gateway", "/internal/orders/liquidations", liquidation + "}", viaGateway, http.StatusNotFound, "COMMON_NOT_FOUND"},
		{"cancel via the gateway", "/internal/orders/cancel", cancel + "}", viaGateway, http.StatusNotFound, "COMMON_NOT_FOUND"},
		// The bodies are checked before anything is placed or canceled.
		{"unknown field", "/internal/orders/liquidations", liquidation + `,"price":"1"}`, nil, http.StatusBadRequest, "COMMON_INVALID_ARGUMENT"},
		{"bad quantity", "/internal/orders/liquidations", strings.Replace(liquidation, `"0.1"`, `"0.1x"`, 1) + "}", nil, http.StatusBadRequest, "COMMON_INVALID_ARGUMENT"},
		{"negative attempt", "/internal/orders/liquidations", liquidation + `,"attempt":-1}`, nil, http.StatusBadRequest, "COMMON_INVALID_ARGUMENT"},
		{"attempt too high", "/internal/orders/liquidations", liquidation + `,"attempt":1001}`, nil, http.StatusBadRequest, "COMMON_INVALID_ARGUMENT"},
		{
			"liquidation ID no UUID", "/internal/orders/liquidations", strings.Replace(liquidation, "0199b0a0-0000-7000-8000-000000000001", "liq-1", 1) + "}", nil,
			http.StatusBadRequest, "COMMON_INVALID_ARGUMENT",
		},
		{"cancel of a spot account", "/internal/orders/cancel", strings.Replace(cancel, "MARGIN_CROSS", "SPOT", 1) + "}", nil, http.StatusBadRequest, "COMMON_INVALID_ARGUMENT"},
		{
			"isolated cancel without its pair", "/internal/orders/cancel", strings.Replace(cancel, "MARGIN_CROSS", "MARGIN_ISOLATED", 1) + "}", nil,
			http.StatusBadRequest, "COMMON_INVALID_ARGUMENT",
		},
	} {
		if status, code := serve(t, c.path, c.body, c.header); status != c.status || code != c.code {
			t.Errorf("%s: %d %s, want %d %s", c.name, status, code, c.status, c.code)
		}
	}
}
