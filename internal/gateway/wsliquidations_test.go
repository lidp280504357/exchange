package gateway

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/platform/event"
)

func TestWebSocketLiquidationsChannel(t *testing.T) {
	hub := NewHub(nil, nil, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	go func() { _ = hub.Run() }()
	t.Cleanup(func() { _ = hub.Stop(context.Background()) })
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	events := WSEvents(hub)
	at := time.Date(2026, 10, 6, 12, 30, 1, 893_000_000, time.UTC)
	var last *eventv1.Envelope
	emit := func(symbol, side string) {
		t.Helper()
		env, err := event.NewFactory("test", "t").New(context.Background(), &marketv1.LiquidationOccurred{
			Symbol: symbol, PositionSide: side, Price: "9425.5", AveragePrice: "9496.5", Quantity: "3", ValueUsd: "300",
			TradedAt: timestamppb.New(at),
		}, "symbol", symbol)
		if err != nil {
			t.Fatal(err)
		}
		if err := events(context.Background(), env); err != nil {
			t.Fatal(err)
		}
		last = env
	}
	// Nothing is kept for later subscribers.
	emit("BTC-USD-PERP", "LONG")

	c := dial(t, "ws"+strings.TrimPrefix(srv.URL, "http"))
	c.send(`{"op":"subscribe","args":["liquidations:BTC-USDT"]}`)
	if m := c.next(); m["ok"] == true {
		t.Fatalf("a pair has no liquidations: %v", m)
	}
	c.send(`{"op":"subscribe","args":["liquidations:BTC-USD-PERP"]}`)
	if m := c.next(); m["ok"] != true {
		t.Fatalf("subscribe: %v", m)
	}
	// The ok is queued with the subscription in place (38cc9847): a
	// liquidation emitted right after it is pushed, though this channel
	// sends nothing on subscribing.
	emit("ETH-USDT-PERP", "LONG") // another contract's
	emit("BTC-USD-PERP", "SHORT")
	m := c.next()
	data, _ := m["data"].(map[string]any)
	if m["channel"] != "liquidations:BTC-USD-PERP" || data["position_side"] != "SHORT" || data["average_price"] != "9496.5" ||
		data["quantity"] != "3" || data["value_usd"] != "300" || data["traded_at"] != "2026-10-06T12:30:01.893Z" || data["symbol"] != "BTC-USD-PERP" {
		t.Fatalf("liquidation: %v", m)
	}
	// The same event again (market-data's second try, C40 ⑥) is not
	// pushed again; the next one is (review FD, A68 ①).
	if err := events(context.Background(), last); err != nil {
		t.Fatal(err)
	}
	emit("BTC-USD-PERP", "LONG")
	if m := c.next(); m["data"].(map[string]any)["position_side"] != "LONG" {
		t.Fatalf("the repeated event pushed again: %v", m)
	}
}
