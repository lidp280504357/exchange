package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/marketdata/application"
	"github.com/skill/exchange/internal/marketdata/transport/httpapi"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/svcsign"
)

type overlayOn struct{}

func (overlayOn) Enabled(key string, _ flags.Subject) bool { return key == flags.KeyOverlay }

var simSecret = []byte(strings.Repeat("s", svcsign.MinSecret))

func send(t *testing.T, r http.Handler, signed bool, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if signed {
		svcsign.SignRequest(req, "sim", simSecret, []byte(body), time.Now())
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return rec.Code, out
}

// market-sim pushes a price event's factor, signed with its key; the
// overlays are listed; a clear ends one (J0 contract §2.1, §2.2).
func TestTheOverlayAPI(t *testing.T) {
	overlay := application.NewOverlay(overlayOn{}, func(s string) bool { return s == "BTC-USDT" }, prometheus.NewRegistry())
	h := &httpapi.Handler{Overlay: overlay, Signed: &svcsign.Verifier{Keys: map[string][]byte{"sim": simSecret}}, Now: time.Now}
	r := chi.NewRouter()
	h.Routes(r)
	until := time.Now().Add(4 * time.Second).UTC().Format(time.RFC3339Nano)
	body := `{"factor":"1.16","until":"` + until + `","event_id":"e1","seq":1}`
	if code, _ := send(t, r, false, http.MethodPut, "/internal/market/overlay/BTC-USDT", body); code != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", code)
	}
	if code, out := send(t, r, true, http.MethodPut, "/internal/market/overlay/btc-usdt", body); code != http.StatusNoContent {
		t.Fatalf("push: %d %v", code, out)
	}
	if code, out := send(t, r, true, http.MethodPut, "/internal/market/overlay/SOL-USDT", body); code != http.StatusConflict ||
		out["code"] != "MARKET_NOT_FOLLOWED" {
		t.Fatalf("not followed: %d %v", code, out)
	}
	code, out := send(t, r, false, http.MethodGet, "/internal/market/overlay", "")
	items, _ := out["items"].([]any)
	if code != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["factor"] != "1.16" || items[0].(map[string]any)["risk"] != true {
		t.Fatalf("list: %d %v", code, out)
	}
	if code, _ := send(t, r, true, http.MethodDelete, "/internal/market/overlay/BTC-USDT", ""); code != http.StatusNoContent {
		t.Fatalf("clear: %d", code)
	}
	if _, out := send(t, r, false, http.MethodGet, "/internal/market/overlay", ""); len(out["items"].([]any)) != 0 {
		t.Fatalf("after the clear: %v", out)
	}
}
