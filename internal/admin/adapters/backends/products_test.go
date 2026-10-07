package backends

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
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
// error with the orders it canceled and the users it could not before
// failing (C60, A87), one too slow an error after the bound (A85) -
// ErrProductCancelTimeout for a cancel - and one not reached
// ErrProductCancelUnreachable.
func TestProductLines(t *testing.T) {
	var (
		mu    sync.Mutex
		asked []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		asked = append(asked, r.Method+" "+r.URL.Path+" "+body["actor"]+" "+body["reason"])
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /trading/internal/products/spot":
			_, _ = w.Write([]byte(`{"product":"spot","closed":false,"open_orders":12,"open_positions":0}`))
		case "POST /trading/internal/products/spot/cancel-open":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"canceled":3,"orders":[]}`))
		case "POST /derivatives/internal/products/coin_m/cancel-open":
			httpx.WriteError(w, r, apperr.Unavailable(errors.New("a user's lock")).WithDetail("canceled", 2).WithDetail("failed_users", 1))
		case "GET /derivatives/internal/products/coin_m", "POST /derivatives/internal/products/slow/cancel-open":
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`{"product":"coin_m","closed":false,"open_orders":1,"open_positions":1}`))
		default: // a service before K1b: no such route
			httpx.WriteError(w, r, apperr.NotFound("no such endpoint"))
		}
	}))
	defer srv.Close()
	c := ProductLines{
		REST: REST{Client: &http.Client{Timeout: 5 * time.Second}}, Trading: srv.URL + "/trading", Derivatives: srv.URL + "/derivatives",
		ReadTimeout: 100 * time.Millisecond, CancelTimeout: 100 * time.Millisecond,
	}
	ctx := context.Background()
	if l, err := c.Line(ctx, "spot"); err != nil || l.OpenOrders != 12 || l.OpenPositions != 0 {
		t.Fatalf("spot's counts %+v %v", l, err)
	}
	if _, err := c.Line(ctx, "usdt_m"); !errors.Is(err, ports.ErrProductLineMissing) {
		t.Fatalf("no route for the counts: %v", err)
	}
	if n, err := c.CancelOpen(ctx, "spot", "boss@example.com", "close spot"); err != nil || n != (ports.ProductCanceled{Orders: 3}) {
		t.Fatalf("spot's cancel %+v %v", n, err)
	}
	if _, err := c.CancelOpen(ctx, "usdt_m", "boss@example.com", "close usdt_m"); !errors.Is(err, ports.ErrProductLineMissing) {
		t.Fatalf("no route for the cancel: %v", err)
	}
	if n, err := c.CancelOpen(ctx, "coin_m", "boss@example.com", "close coin_m"); !apperr.Is(err, apperr.CodeUnavailable) ||
		errors.Is(err, ports.ErrProductLineMissing) || errors.Is(err, ports.ErrProductCancelUnreachable) ||
		n != (ports.ProductCanceled{Orders: 2, FailedUsers: 1}) {
		t.Fatalf("the service's failure part way %+v %v", n, err)
	}
	if _, err := c.CancelOpen(ctx, "slow", "boss@example.com", "close it"); !errors.Is(err, ports.ErrProductCancelTimeout) {
		t.Fatalf("a cancel too slow: %v", err)
	}
	gone := ProductLines{REST: c.REST, Trading: "http://127.0.0.1:1", Derivatives: srv.URL}
	if _, err := gone.CancelOpen(ctx, "spot", "boss@example.com", "close spot"); !errors.Is(err, ports.ErrProductCancelUnreachable) ||
		errors.Is(err, ports.ErrProductCancelTimeout) {
		t.Fatalf("a service not reached: %v", err)
	}
	start := time.Now()
	if _, err := c.Line(ctx, "coin_m"); !apperr.Is(err, apperr.CodeUnavailable) || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("a count too slow: %v after %v", err, time.Since(start))
	}
	mu.Lock()
	got := slices.Clone(asked)
	mu.Unlock()
	if len(got) != 7 || got[0] != "GET /trading/internal/products/spot  " ||
		got[2] != "POST /trading/internal/products/spot/cancel-open boss@example.com close spot" ||
		got[3] != "POST /derivatives/internal/products/usdt_m/cancel-open boss@example.com close usdt_m" {
		t.Fatalf("asked %q", got)
	}
}
