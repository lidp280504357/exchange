package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
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
