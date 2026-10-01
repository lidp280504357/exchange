package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Contract reference data goes to instrument-service although
// /v1/market/{symbol}/* would match it for market-data-service.
func TestContractRoutes(t *testing.T) {
	var got string
	stub := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			got = name
			w.WriteHeader(http.StatusNoContent)
		})
	}
	r := httpx.NewRouter(httpx.RouterOptions{Logger: slog.New(slog.DiscardHandler)})
	Mount(r, Guards{}, Upstreams{
		Auth: stub("auth"), User: stub("user"), Notification: stub("notification"), Instrument: stub("instrument"),
		Ledger: stub("ledger"), Market: stub("market"),
	})
	for path, want := range map[string]string{
		"/v1/market/contracts":               "instrument",
		"/v1/market/contracts/BTC-USDT-PERP": "instrument",
		"/v1/market/pairs/BTC-USDT":          "instrument",
		"/v1/market/BTC-USDT/depth":          "market",
		"/v1/market/summary":                 "market",
		"/v1/market/sparklines":              "market",
		"/v1/market/tickers":                 "market",
	} {
		got = ""
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
		if rec.Code != http.StatusNoContent || got != want {
			t.Errorf("%s: %d from %q, want %s", path, rec.Code, got, want)
		}
	}
}

// The custodian's callbacks reach wallet-service without a token: they
// carry their own signature (ADR-0011).
func TestCustodyCallbackIsPublic(t *testing.T) {
	var got string
	r := httpx.NewRouter(httpx.RouterOptions{Logger: slog.New(slog.DiscardHandler)})
	Mount(r, Guards{}, Upstreams{
		Auth: http.NotFoundHandler(), User: http.NotFoundHandler(), Notification: http.NotFoundHandler(),
		Instrument: http.NotFoundHandler(), Ledger: http.NotFoundHandler(),
		Wallet: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}),
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/wallet/callbacks/udun", http.NoBody))
	if rec.Code != http.StatusOK || got != "/v1/wallet/callbacks/udun" {
		t.Fatalf("%d, reached %q", rec.Code, got)
	}
}
