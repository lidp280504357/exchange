package backends

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPricesLeaveOutStaleTickers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tickers":[
			{"symbol":"BTC-USDT","last":"60000","updated_at":"2026-10-03T07:59:30Z","last_trade_at":null},
			{"symbol":"ETH-USDT","last":"3000","updated_at":"2026-10-03T07:58:00Z"},
			{"symbol":"NEW-USDT","last":null,"updated_at":"2026-10-03T08:00:00Z"},
			{"symbol":"ODD-USDT","last":"1"},
			{"symbol":"ASTRA-USDT","last":"1.02","updated_at":"2026-10-03T08:00:00Z","last_trade_at":"2026-10-03T07:55:00Z"},
			{"symbol":"OWN-USDT","last":"5","updated_at":"2026-10-03T08:00:00Z","last_trade_at":"2026-10-03T07:59:50Z"}]}`))
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	m := Market{REST: REST{Client: srv.Client()}, Base: srv.URL, Now: func() time.Time { return now }}
	all, err := m.Prices(context.Background(), 0)
	if err != nil || len(all) != 5 || all["ETH-USDT"].String() != "3000" {
		t.Fatalf("any age: %v %v", all, err)
	}
	// A minute: the feed that stopped two minutes ago, or never said when, is no price; a platform
	// pair's price is as old as its last trade (C5.5 ⑮).
	fresh, err := m.Prices(context.Background(), time.Minute)
	if err != nil || len(fresh) != 2 || fresh["BTC-USDT"].String() != "60000" || fresh["OWN-USDT"].String() != "5" {
		t.Fatalf("fresh only: %v %v", fresh, err)
	}
}
