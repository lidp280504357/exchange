package prices

import (
	"strings"
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
