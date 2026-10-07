package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketsim/ports"
	"github.com/skill/exchange/internal/platform/svcsign"
)

// handlerTransport serves requests with a handler, without a listener.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}

var overlaySecret = []byte(strings.Repeat("o", svcsign.MinSecret))

// The simulated market follows the reference market's own BTC and ETH, not
// a price event's (review GD ④); a followed pair's prices, the pushes
// signed with the key market-data knows market-sim by, and HOUSE's rooms
// (404: not quoted).
func TestTheOverlayClients(t *testing.T) {
	verifier := &svcsign.Verifier{Keys: map[string][]byte{OverlayKeyID: overlaySecret}}
	var pushed string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/market/BTC-USDT/reference", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"followed":true,"price":"116000","source_price":"100000","overlay_factor":"1.16","fresh":true}`))
	})
	mux.HandleFunc("GET /internal/market/ASTRA-USDT/reference", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"followed":false,"price":"1.02","source_price":null,"overlay_factor":"1","fresh":true}`))
	})
	mux.Handle("PUT /internal/market/overlay/BTC-USDT", verifier.Changes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		pushed = svcsign.KeyID(r.Context()) + " " + string(b)
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /internal/house/rooms/BTC-USDT", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"symbol":"BTC-USDT","buy":"13.7","sell":"10.5","mid":"100000","unit_value":"100000","inverse":false}`))
	})
	hc := &http.Client{Transport: handlerTransport{mux}, Timeout: 2 * time.Second}
	ctx := context.Background()

	c := &Client{MarketURL: "http://market-data", HTTP: hc}
	if p, fresh, err := c.Reference(ctx, "BTC-USDT"); err != nil || !fresh || !p.Equal(decimal.RequireFromString("100000")) {
		t.Fatalf("BTC-USDT for the model: %s %v %v", p, fresh, err)
	}
	if p, _, err := c.Reference(ctx, "ASTRA-USDT"); err != nil || !p.Equal(decimal.RequireFromString("1.02")) {
		t.Fatalf("its own pair: %s %v", p, err)
	}

	o := &Overlays{
		MarketURL: "http://market-data", MarketMakerURL: "http://market-maker", HTTP: hc,
		Signer: svcsign.Client{KeyID: OverlayKeyID, Secret: overlaySecret},
	}
	f, err := o.Followed(ctx, "BTC-USDT")
	if err != nil || !f.Followed || !f.Source.Equal(decimal.RequireFromString("100000")) || !f.Shown.Equal(decimal.RequireFromString("116000")) ||
		!f.Factor.Equal(decimal.RequireFromString("1.16")) || !f.Fresh {
		t.Fatalf("followed %+v %v", f, err)
	}
	until := time.Date(2026, 10, 7, 4, 0, 4, 0, time.UTC)
	ends := time.Date(2026, 10, 7, 4, 0, 20, 0, time.UTC)
	if err := o.Push(ctx, "BTC-USDT", ports.OverlayPush{
		Factor: decimal.RequireFromString("1.08"), Until: until, Risk: true, EventID: "e1", Seq: 7,
		EndsAt: ends,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pushed, "sim ") || !strings.Contains(pushed, `"factor":"1.08"`) || !strings.Contains(pushed, `"ends_at":"2026-10-07T04:00:20Z"`) ||
		!strings.Contains(pushed, `"seq":7`) {
		t.Fatalf("pushed %s", pushed)
	}
	if err := o.Push(ctx, "ETH-USDT", ports.OverlayPush{Factor: decimal.RequireFromString("1.08"), Until: until, EventID: "e2"}); err == nil {
		t.Fatal("a refused push reported done")
	}
	if r, ok, err := o.Rooms(ctx, "BTC-USDT"); err != nil || !ok || !r.Buy.Equal(decimal.RequireFromString("13.7")) || r.Inverse {
		t.Fatalf("rooms %+v %v %v", r, ok, err)
	}
	if _, ok, err := o.Rooms(ctx, "SOL-USDT"); err != nil || ok {
		t.Fatalf("not quoted: %v %v", ok, err)
	}
}
