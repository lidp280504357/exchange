package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A bot's active orders are read page by page (the services give 100 at
// most a page), market orders left out; more than ten pages is an error,
// not a part of the list.
func TestOpenReadsEveryPage(t *testing.T) {
	var pages atomic.Int32
	pages.Store(3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "100" || r.Header.Get("X-User-Id") != "bot" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		n := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			_, _ = fmt.Sscanf(c, "p%d", &n)
		}
		var items []map[string]any
		for i := range 100 {
			items = append(items, map[string]any{"order_id": fmt.Sprintf("o%d-%d", n, i), "side": "BUY", "type": "LIMIT", "price": "1.01"})
		}
		items[0]["type"], items[0]["price"] = "MARKET", nil
		var next any
		if int32(n+1) < pages.Load() {
			next = fmt.Sprintf("p%d", n+1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "next_cursor": next})
	}))
	defer srv.Close()
	c := &Client{TradingURL: srv.URL, DerivativesURL: srv.URL, HTTP: &http.Client{Timeout: 5 * time.Second}}
	orders, err := c.Open(context.Background(), "bot", "ASTRA-USDT")
	if err != nil || len(orders) != 3*99 || orders[0].ID != "o0-1" || orders[len(orders)-1].ID != "o2-99" {
		t.Fatalf("%d orders, %v", len(orders), err)
	}
	pages.Store(11)
	if _, err := c.OpenContract(context.Background(), "bot", "ASTRA-USDT-PERP"); err == nil {
		t.Fatal("eleven pages read as a list")
	}
}
