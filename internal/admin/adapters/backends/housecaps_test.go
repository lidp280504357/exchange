package backends

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/svcsign"
)

// TestMarketMakerCaps checks how the console reads and changes HOUSE's
// caps on market-maker's internal API (review C45, A69): the paths, the
// change's body signed with the console's key (review C47) and not sent
// without it, and market-maker's refusal as its apperr.
func TestMarketMakerCaps(t *testing.T) {
	secret := strings.Repeat("k", svcsign.MinSecret)
	verifier := &svcsign.Verifier{Keys: map[string][]byte{"admin": []byte(secret)}}
	type call struct {
		line, signedBy string
		body           map[string]any
	}
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{line: r.Method + " " + r.URL.RequestURI()}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &c.body)
		}
		if h := r.Header.Get(svcsign.Header); h != "" {
			k, err := verifier.Verify(r.Method, r.URL.RequestURI(), h, raw)
			if err != nil {
				t.Errorf("a bad signature: %v", err)
			}
			c.signedBy = k
		}
		got = append(got, c)
		switch {
		case r.Method == http.MethodPut && c.body["version"] == float64(1):
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"HOUSE_CAPS_VERSION","message":"the caps changed meanwhile: read them again","details":{"version":"2"}}`))
		case r.URL.Path == "/internal/house/caps/changes":
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			_, _ = w.Write([]byte(`{"level":"1","version":3}`))
		}
	}))
	defer srv.Close()
	m := MarketMaker{REST: REST{Client: &http.Client{Timeout: 5 * time.Second}}, Base: srv.URL}
	ctx := context.Background()

	if raw, err := m.HouseCaps(ctx); err != nil || string(raw) != `{"level":"1","version":3}` {
		t.Fatalf("read %s %v", raw, err)
	}
	if raw, err := m.HouseCapsChanges(ctx, 100); err != nil || string(raw) != `{"items":[]}` {
		t.Fatalf("changes %s %v", raw, err)
	}
	w := ports.HouseCapsWrite{
		Caps: map[string]string{"safety": "1001"}, Version: 2, Actor: "fin@example.com", Approver: "boss@example.com",
		ApprovalID: "0192a000-0000-7000-8000-0000000000a9", Reason: "a larger margin",
	}
	if _, err := m.SetHouseCaps(ctx, w); !apperr.Is(err, apperr.CodeUnavailable) {
		t.Fatalf("without the key: %v", err)
	}
	m.Signer = svcsign.Client{KeyID: "admin", Secret: []byte(secret)}
	if _, err := m.SetHouseCaps(ctx, w); err != nil {
		t.Fatalf("signed: %v", err)
	}
	w.Version = 1
	if _, err := m.SetHouseCaps(ctx, w); !apperr.Is(err, "HOUSE_CAPS_VERSION") {
		t.Fatalf("market-maker's refusal: %v", err)
	}
	if len(got) != 4 || got[0].line != "GET /internal/house/caps" || got[1].line != "GET /internal/house/caps/changes?limit=100" {
		t.Fatalf("calls %+v", got)
	}
	for i, c := range got[2:3] {
		b := c.body
		if c.line != "PUT /internal/house/caps" || b["safety"] != "1001" || b["version"] != float64(2) || b["actor"] != "fin@example.com" ||
			b["approver"] != "boss@example.com" || b["approval_id"] != w.ApprovalID || b["reason"] != "a larger margin" || len(b) != 6 {
			t.Fatalf("change %d %+v", i, c)
		}
	}
	if got[2].signedBy != "admin" || got[3].signedBy != "admin" {
		t.Fatalf("signatures %q %q", got[2].signedBy, got[3].signedBy)
	}
}

// TestMarketMakerRooms reads what HOUSE may still buy and sell of a symbol
// (J0 contract §4.2, for a price event's preview, A81): unsigned, a symbol
// it does not quote (404) is no error.
func TestMarketMakerRooms(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(svcsign.Header) != "" {
			t.Errorf("a read signed")
		}
		switch r.URL.Path {
		case "/internal/house/rooms/BTC-USD-PERP":
			_, _ = w.Write([]byte(`{"symbol":"BTC-USD-PERP","buy":"50000","sell":"48000","mid":"84100","unit_value":"100","inverse":true,"updated_at":"2026-10-07T07:00:00Z"}`))
		case "/internal/house/rooms/ASTRA-USDT":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"COMMON_NOT_FOUND","message":"HOUSE does not quote the symbol"}`))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	m := MarketMaker{REST: REST{Client: &http.Client{Timeout: 5 * time.Second}}, Base: srv.URL}
	ctx := context.Background()
	r, quoted, err := m.HouseRooms(ctx, "BTC-USD-PERP")
	if err != nil || !quoted || r.Buy.String() != "50000" || r.Sell.String() != "48000" || r.UnitValue.String() != "100" || !r.Inverse {
		t.Fatalf("rooms %+v %v %v", r, quoted, err)
	}
	if _, quoted, err := m.HouseRooms(ctx, "ASTRA-USDT"); err != nil || quoted {
		t.Fatalf("not quoted: %v %v", quoted, err)
	}
	if _, _, err := m.HouseRooms(ctx, "ETH-USDT"); err == nil {
		t.Fatal("market-maker down is an error")
	}
}
