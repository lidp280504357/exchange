package backends

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// TestProductLines asks the services that run the product lines (K3):
// spot-trading-service for spot, derivatives-service for the contracts,
// what closing one touches and to cancel a closed one's orders; a service
// without the routes yet is ErrProductLineMissing, its own refusal an
// error.
func TestProductLines(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		asked = append(asked, r.Method+" "+r.URL.Path+" "+body["actor"]+" "+body["reason"])
		switch r.Method + " " + r.URL.Path {
		case "GET /trading/internal/products/spot":
			_, _ = w.Write([]byte(`{"product":"spot","closed":false,"open_orders":12,"open_positions":0}`))
		case "POST /trading/internal/products/spot/cancel-open":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"canceled":3,"orders":[]}`))
		case "POST /derivatives/internal/products/coin_m/cancel-open":
			httpx.WriteError(w, r, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the engine is busy"))
		default: // a service before K1b: no such route
			httpx.WriteError(w, r, apperr.NotFound("no such endpoint"))
		}
	}))
	defer srv.Close()
	c := ProductLines{REST: REST{Client: &http.Client{Timeout: 5 * time.Second}}, Trading: srv.URL + "/trading", Derivatives: srv.URL + "/derivatives"}
	ctx := context.Background()
	if l, err := c.Line(ctx, "spot"); err != nil || l.OpenOrders != 12 || l.OpenPositions != 0 {
		t.Fatalf("spot's counts %+v %v", l, err)
	}
	if _, err := c.Line(ctx, "usdt_m"); !errors.Is(err, ports.ErrProductLineMissing) {
		t.Fatalf("no route for the counts: %v", err)
	}
	if n, err := c.CancelOpen(ctx, "spot", "boss@example.com", "close spot"); err != nil || n != 3 {
		t.Fatalf("spot's cancel %d %v", n, err)
	}
	if _, err := c.CancelOpen(ctx, "usdt_m", "boss@example.com", "close usdt_m"); !errors.Is(err, ports.ErrProductLineMissing) {
		t.Fatalf("no route for the cancel: %v", err)
	}
	if _, err := c.CancelOpen(ctx, "coin_m", "boss@example.com", "close coin_m"); !apperr.Is(err, apperr.CodeUnavailable) || errors.Is(err, ports.ErrProductLineMissing) {
		t.Fatalf("the service's refusal: %v", err)
	}
	if len(asked) != 5 || asked[0] != "GET /trading/internal/products/spot  " ||
		asked[2] != "POST /trading/internal/products/spot/cancel-open boss@example.com close spot" ||
		asked[3] != "POST /derivatives/internal/products/usdt_m/cancel-open boss@example.com close usdt_m" {
		t.Fatalf("asked %q", asked)
	}
}
