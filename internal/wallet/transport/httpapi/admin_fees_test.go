package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/wallet/application"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

// The console's fee routes (review AL): a status other than the three is
// refused, a custodian neither configured nor known is not found (review
// AT, as for /custody), a withdrawal that is no UUID is not found, an
// amount that is no number and a body that is no JSON are refused, all
// before anything is read; a fee renders with its custodian, from its key
// when the list did not give it (none from a key without one).
func TestTheCustodyFeeRoutes(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{Svc: &application.Service{}}).Routes(r)
	send := func(method, path, body string) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body)))
		var e httpx.ErrorBody
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		return w.Code, e.Code
	}
	for _, c := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{http.MethodGet, "/internal/wallet/custody/fees?status=PAID", "", http.StatusBadRequest, ""},
		{http.MethodGet, "/internal/wallet/custody/fees?provider=FOO", "", http.StatusNotFound, ""},
		{http.MethodPost, "/internal/wallet/custody/fees/not-a-withdrawal/book", `{"actor":"ops","reason":"booked"}`, http.StatusNotFound, "WALLET_CUSTODY_FEE_NOT_FOUND"},
		{http.MethodPost, "/internal/wallet/custody/fees/not-a-withdrawal/write-off", `{"actor":"ops","reason":"none"}`, http.StatusNotFound, "WALLET_CUSTODY_FEE_NOT_FOUND"},
		{http.MethodPost, "/internal/wallet/custody/fees/01a101cc-60e3-747a-8d31-c4d6984cd769/book", `{"amount":"1,5","actor":"ops","reason":"x"}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/internal/wallet/custody/fees/01a101cc-60e3-747a-8d31-c4d6984cd769/write-off", `{"actor":`, http.StatusBadRequest, ""},
	} {
		status, code := send(c.method, c.path, c.body)
		if status != c.status || (c.code != "" && code != c.code) {
			t.Errorf("%s %s: %d %s, want %d %s", c.method, c.path, status, code, c.status, c.code)
		}
	}
	f := domain.CustodyFee{
		ChainFee:     domain.ChainFee{TxHash: "UDUNMOCK:1791042381321272", Asset: "TUSD", Network: "TRON-TEST", Amount: decimal.RequireFromString("1.5")},
		WithdrawalID: "01a101cc-60e3-747a-8d31-c4d6984cd769",
	}
	if j := CustodyFeeJSONOf(f); j.Provider != domain.ProviderUdunMock || j.Amount != "1.5" || j.Unit != nil {
		t.Fatalf("a fee from its key %+v", j)
	}
	f.Provider = domain.ProviderUdun
	if j := CustodyFeeJSONOf(f); j.Provider != domain.ProviderUdun {
		t.Fatalf("a fee with its custodian %+v", j)
	}
	f.Provider, f.TxHash = "", "0xabc"
	if j := CustodyFeeJSONOf(f); j.Provider != "" {
		t.Fatalf("a fee whose key names no custodian %+v", j)
	}
}
