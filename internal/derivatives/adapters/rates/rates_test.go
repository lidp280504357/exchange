package rates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/market/BTC-USDT-PERP/funding-rates" || r.URL.Query().Get("from") != "2026-09-30T08:00:00Z" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"symbol":"BTC-USDT-PERP","funding_rates":[{"funding_time":"2026-09-30T08:00:00Z","funding_rate":"0.0001","mark_price":"60010.5"}]}`))
	}))
	defer srv.Close()
	c := Client{Base: srv.URL, Client: srv.Client()}
	rate, mark, found, err := c.Rate(context.Background(), "BTC-USDT-PERP", time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC))
	if err != nil || !found || rate.String() != "0.0001" || mark.String() != "60010.5" {
		t.Fatalf("%s %s %v %v", rate, mark, found, err)
	}
	if _, _, found, err := c.Rate(context.Background(), "ETH-USDT-PERP", time.Now()); err != nil || found {
		t.Fatalf("unknown contract: %v %v", found, err)
	}
}
