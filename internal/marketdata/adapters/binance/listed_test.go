package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		symbol := r.URL.Query().Get("symbol")
		switch {
		case r.URL.Path == "/api/v3/ticker/price" && symbol == "LINKBTC", r.URL.Path == "/fapi/v1/ticker/price" && symbol == "BTCUSDT":
			_, _ = w.Write([]byte(`{"price":"1"}`))
		case symbol == "BUSY":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":-1121,"msg":"Invalid symbol."}`))
		}
	}))
	defer srv.Close()
	s := New(srv.URL, "wss://spot", srv.Client()).WithFutures(srv.URL, "wss://futures")
	s.gap = 0
	ctx := context.Background()
	for _, c := range []struct {
		symbol  string
		futures bool
		want    bool
	}{
		{"LINKBTC", false, true},
		{"LINKBTC", true, false},
		{"BTCUSDT", true, true},
		{"NOSUCHCOIN", false, false},
	} {
		if got, err := s.Listed(ctx, c.symbol, c.futures); err != nil || got != c.want {
			t.Fatalf("%s futures=%v: %v %v", c.symbol, c.futures, got, err)
		}
	}
	if _, err := s.Listed(ctx, "BUSY", false); err == nil {
		t.Fatal("a failure is not an answer")
	}
	if _, err := New(srv.URL, "", srv.Client()).Listed(ctx, "BTCUSDT", true); err == nil {
		t.Fatal("futures without its endpoints")
	}
}
