package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/marketmaker/ports"
)

func TestPlaceSendsAValidOrderAsTheMarketMaker(t *testing.T) {
	var got map[string]string
	var user string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user = r.Header.Get("X-User-Id")
		_ = json.NewDecoder(r.Body).Decode(&got)
		if len(got) == 0 {
			http.Error(w, `{"code":"COMMON_INVALID_ARGUMENT"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &Client{Trading: srv.URL, UserID: "mm-user", HTTP: srv.Client()}
	q := domain.Quote{Side: domain.Sell, Price: decimal.RequireFromString("84012.5"), Quantity: decimal.RequireFromString("0.002")}
	if err := c.Place(context.Background(), "BTC-USDT", q); err != nil {
		t.Fatal(err)
	}
	// The trading service's rule for client_order_id.
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,36}$`).MatchString(got["client_order_id"]) {
		t.Fatalf("client_order_id %q", got["client_order_id"])
	}
	if user != "mm-user" || got["price"] != "84012.5" || got["quantity"] != "0.002" || got["type"] != "LIMIT" || got["side"] != "SELL" {
		t.Fatalf("request %v as %q", got, user)
	}
}

func TestRefusalsForFundsAndFinishedOrdersAreNotErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := "LEDGER_INSUFFICIENT_BALANCE"
		if r.Method == http.MethodDelete {
			code = "ORDER_ALREADY_FILLED"
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"` + code + `"}`))
	}))
	defer srv.Close()
	c := &Client{Trading: srv.URL, UserID: "mm-user", HTTP: srv.Client()}
	q := domain.Quote{Side: domain.Buy, Price: decimal.RequireFromString("1"), Quantity: decimal.RequireFromString("1")}
	if err := c.Place(context.Background(), "BTC-USDT", q); err != nil {
		t.Fatalf("place: %v", err)
	}
	if err := c.Cancel(context.Background(), "o1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func TestContractsQuoteThroughTheDerivativesAPI(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	var placed map[string]string
	refuse := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-Id") != "mm-user" {
			http.Error(w, `{"code":"COMMON_UNAUTHENTICATED"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/derivatives/orders":
			if refuse != "" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"code":"` + refuse + `"}`))
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&placed)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/v1/derivatives/orders":
			_, _ = w.Write([]byte(`{"items":[{"order_id":"o1","side":"BUY","type":"LIMIT","price":"59940","cancel_requested":false},
				{"order_id":"o2","side":"SELL","type":"LIMIT","price":"60060","cancel_requested":true},
				{"order_id":"o3","side":"SELL","type":"MARKET","price":"61000"}]}`))
		case r.URL.Path == "/v1/derivatives/positions":
			_, _ = w.Write([]byte(`{"positions":[{"quantity":"0.3"},{"quantity":"-0.1"}]}`))
		case r.URL.Path == "/v1/market/BTC-USDT-PERP/mark-price":
			_, _ = w.Write([]byte(`{"mark_price":"60000.5","degraded":false,"updated_at":"2026-09-29T11:59:58Z"}`))
		case r.URL.Path == "/v1/market/contracts/BTC-USDT-PERP":
			_, _ = w.Write([]byte(`{"status":"TRADING","base_asset":"BTC","quote_asset":"USDT","tick_size":"0.1","lot_size":"0.001"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Contracts{
		Client:      &Client{Market: srv.URL, Instrument: srv.URL, UserID: "mm-user", HTTP: srv.Client()},
		Derivatives: srv.URL, Now: func() time.Time { return now },
	}
	ctx := context.Background()
	open, err := c.Open(ctx, "BTC-USDT-PERP")
	if err != nil || len(open) != 2 || !open[1].CancelRequested || open[0].Key() != "BUY@59940" {
		t.Fatalf("open %+v %v", open, err)
	}
	if net, err := c.Net(ctx, "BTC-USDT-PERP"); err != nil || !net.Equal(decimal.RequireFromString("0.2")) {
		t.Fatalf("net %s %v", net, err)
	}
	mark, fresh, err := c.Price(ctx, "BTC-USDT-PERP")
	if err != nil || !fresh || !mark.Equal(decimal.RequireFromString("60000.5")) {
		t.Fatalf("mark %s %v %v", mark, fresh, err)
	}
	now = now.Add(10 * time.Second)
	if _, fresh, _ := c.Price(ctx, "BTC-USDT-PERP"); fresh {
		t.Fatal("a mark 12 seconds old is not fresh")
	}
	spec, err := c.Pair(ctx, "BTC-USDT-PERP")
	if err != nil || spec.Status != "TRADING" || !spec.TickSize.Equal(decimal.RequireFromString("0.1")) {
		t.Fatalf("contract %+v %v", spec, err)
	}
	q := domain.Quote{Side: domain.Buy, Price: decimal.RequireFromString("59940"), Quantity: decimal.RequireFromString("0.002")}
	if err := c.Place(ctx, "BTC-USDT-PERP", q); err != nil || placed["type"] != "LIMIT" || placed["time_in_force"] != "GTC" ||
		placed["price"] != "59940" || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,36}$`).MatchString(placed["client_order_id"]) {
		t.Fatalf("place %v %v", placed, err)
	}
	refuse = "DERIV_INSUFFICIENT_MARGIN"
	if err := c.Place(ctx, "BTC-USDT-PERP", q); err != nil {
		t.Fatalf("short of margin is retried next round: %v", err)
	}
	refuse = "DERIV_REDUCE_ONLY_MODE"
	if err := c.Place(ctx, "BTC-USDT-PERP", q); !errors.Is(err, ports.ErrPaused) {
		t.Fatalf("reduce-only pauses the quotes: %v", err)
	}
	refuse = "DERIV_LEVERAGE_EXCEEDED"
	if err := c.Place(ctx, "BTC-USDT-PERP", q); err == nil || errors.Is(err, ports.ErrPaused) {
		t.Fatalf("another refusal is an error: %v", err)
	}
}
