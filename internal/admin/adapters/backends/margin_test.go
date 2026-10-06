package backends

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// TestMarginRequests checks how the console calls margin-service's
// internal API (C5): the paths, the administrator in X-Admin-Id on writes
// only, the terms' numbers as written with expected_version, and its
// refusals as their apperr.
func TestMarginRequests(t *testing.T) {
	type call struct{ method, uri, admin, body string }
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, call{r.Method, r.URL.RequestURI(), r.Header.Get("X-Admin-Id"), string(b)})
		if r.URL.Path == "/internal/margin/assets/ETH" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"MARGIN_PARAMS_CHANGED","message":"the terms changed since they were read"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	m := Margin{REST: REST{Client: &http.Client{Timeout: 5 * time.Second}}, Base: srv.URL}
	ctx := context.Background()
	const user = "0199a000-0000-7000-8000-000000000001"

	terms := json.RawMessage(`{"borrowable":true,"collateral":true,"haircut":"0.70","pool_cap":"1000000","fixed_rate":0.0000123}`)
	if _, err := m.SetAsset(ctx, "btc", terms, 4, "boss@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAsset(ctx, "ETH", terms, 4, "boss@example.com"); !apperr.Is(err, "MARGIN_PARAMS_CHANGED") {
		t.Fatalf("a moved version: %v", err)
	}
	if _, err := m.SetCross(ctx, json.RawMessage(`{"leverage":5,"warn_level":"1.2"}`), 2, "boss@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetPair(ctx, "btc-usdt", json.RawMessage(`{"isolated":false,"leverage":10}`), 0, "boss@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetPair(ctx, "BTC-USDT", json.RawMessage(`[1]`), 0, "boss@example.com"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("terms not an object: %v", err)
	}
	for _, read := range []func() (json.RawMessage, error){
		func() (json.RawMessage, error) { return m.Assets(ctx) },
		func() (json.RawMessage, error) { return m.Settings(ctx) },
		func() (json.RawMessage, error) { return m.Pairs(ctx) },
		func() (json.RawMessage, error) {
			return m.Accounts(ctx, ports.MarginAccountQuery{Status: "WARNED", Account: "MARGIN_ISOLATED", Symbol: "BTC-USDT", Limit: 50})
		},
		func() (json.RawMessage, error) { return m.Account(ctx, user, "MARGIN_ISOLATED:BTC-USDT") },
	} {
		if _, err := read(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Freeze(ctx, user, "MARGIN_CROSS", "ops@example.com", "suspicious"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Unfreeze(ctx, user, "MARGIN_CROSS", "ops@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Liquidate(ctx, user, "MARGIN_CROSS", "0199a000-0000-7000-8000-00000000aaaa", "ops@example.com"); err != nil {
		t.Fatal(err)
	}

	want := []call{
		{
			"PUT", "/internal/margin/assets/BTC", "boss@example.com",
			`{"borrowable":true,"collateral":true,"expected_version":4,"fixed_rate":0.0000123,"haircut":"0.70","pool_cap":"1000000"}`,
		},
		{
			"PUT", "/internal/margin/assets/ETH", "boss@example.com",
			`{"borrowable":true,"collateral":true,"expected_version":4,"fixed_rate":0.0000123,"haircut":"0.70","pool_cap":"1000000"}`,
		},
		{"PUT", "/internal/margin/settings", "boss@example.com", `{"cross":{"leverage":5,"warn_level":"1.2"},"expected_version":2}`},
		{"PUT", "/internal/margin/pairs/BTC-USDT", "boss@example.com", `{"expected_version":0,"isolated":false,"leverage":10}`},
		{"GET", "/internal/margin/assets", "", ""},
		{"GET", "/internal/margin/settings", "", ""},
		{"GET", "/internal/margin/pairs", "", ""},
		{"GET", "/internal/margin/accounts?account=MARGIN_ISOLATED&limit=50&status=WARNED&symbol=BTC-USDT", "", ""},
		{"GET", "/internal/margin/accounts/" + user + "/MARGIN_ISOLATED:BTC-USDT", "", ""},
		{"POST", "/internal/margin/accounts/" + user + "/MARGIN_CROSS/freeze", "ops@example.com", `{"reason":"suspicious"}`},
		{"POST", "/internal/margin/accounts/" + user + "/MARGIN_CROSS/unfreeze", "ops@example.com", `{}`},
		{
			"POST", "/internal/margin/accounts/" + user + "/MARGIN_CROSS/liquidate", "ops@example.com",
			`{"approval_id":"0199a000-0000-7000-8000-00000000aaaa"}`,
		},
	}
	if len(got) != len(want) {
		t.Fatalf("asked %d: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}
