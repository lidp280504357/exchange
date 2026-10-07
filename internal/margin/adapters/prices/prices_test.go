package prices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestPricesFromTickers(t *testing.T) {
	now := time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC)
	list, err := Parse(strings.NewReader(`{"tickers":[
		{"symbol":"BTC-USDT","last":"62000.5","updated_at":"2026-10-06T01:59:59Z"},
		{"symbol":"ETH-USDT","last":"2400","updated_at":"2026-10-06T01:57:00Z"},
		{"symbol":"ETH-BTC","last":"0.039","updated_at":"2026-10-06T01:59:59Z"},
		{"symbol":"SOL-USDT","last":null,"updated_at":"2026-10-06T01:59:59Z"},
		{"symbol":"XRP-USDT","last":"0","updated_at":"2026-10-06T01:59:59Z"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	p := &Poller{Now: func() time.Time { return clock }}
	p.Set(list, now)
	got := p.Prices()
	if len(got) != 2 {
		t.Fatalf("prices %v", got)
	}
	if btc := got["BTC"]; !btc.Value.Equal(decimal.RequireFromString("62000.5")) || !btc.Fresh {
		t.Errorf("BTC %+v", btc)
	}
	// A ticker computed three minutes ago (its feed stopped) is stale.
	if eth := got["ETH"]; !eth.Value.Equal(decimal.RequireFromString("2400")) || eth.Fresh {
		t.Errorf("ETH %+v", eth)
	}
	// Without a poll for a while every price is stale.
	clock = now.Add(MaxPollAge + time.Second)
	if btc := p.Prices()["BTC"]; btc.Fresh {
		t.Errorf("BTC after the polls stopped %+v", btc)
	}
	if _, err := Parse(strings.NewReader(`{"tickers":`)); err == nil {
		t.Error("a broken body parsed")
	}
}

// A price event leaving the leverage on the reference market's price
// (risk false) is valued at market-data's price for risk; one with risk,
// as the ticker shows it. Unread, the pair keeps the price last polled.
func TestAPriceEventWithoutRiskIsValuedAtThePriceForRisk(t *testing.T) {
	now := time.Date(2026, 10, 7, 2, 0, 0, 0, time.UTC)
	var riskDown atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/market/tickers", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tickers":[{"symbol":"BTC-USDT","last":"116000","updated_at":"2026-10-07T02:00:00Z"},
			{"symbol":"ETH-USDT","last":"3600","updated_at":"2026-10-07T02:00:00Z"}]}`))
	})
	mux.HandleFunc("GET /internal/market/overlay", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"symbol":"BTC-USDT","factor":"1.16","risk":false},{"symbol":"ETH-USDT","factor":"0.9","risk":true}]}`))
	})
	mux.HandleFunc("GET /internal/market/BTC-USDT/reference", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("for") != "risk" || riskDown.Load() {
			http.Error(w, "no", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"price":"100000.5","updated_at":"2026-10-07T02:00:00Z"}`))
	})
	p := &Poller{Base: "http://market-data", Client: &http.Client{Transport: handlerTransport{mux}}, Now: func() time.Time { return now }}
	// Never priced and its price for risk unread: no price rather than
	// the event's.
	riskDown.Store(true)
	if err := p.Poll(context.Background()); err == nil {
		t.Fatal("an unread price for risk not reported")
	}
	if btc, ok := p.Prices()["BTC"]; ok {
		t.Fatalf("BTC priced at the event's %+v", btc)
	}
	riskDown.Store(false)
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := p.Prices()
	if !got["BTC"].Value.Equal(decimal.RequireFromString("100000.5")) || !got["ETH"].Value.Equal(decimal.RequireFromString("3600")) {
		t.Fatalf("prices %v", got)
	}
	riskDown.Store(true)
	if err := p.Poll(context.Background()); err == nil {
		t.Fatal("an unread price for risk not reported")
	}
	if btc := p.Prices()["BTC"]; !btc.Value.Equal(decimal.RequireFromString("100000.5")) {
		t.Fatalf("BTC while its price for risk is unread: %+v", btc)
	}
}

// handlerTransport serves requests with a handler, without a listener.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}
