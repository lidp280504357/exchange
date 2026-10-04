package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/wallet/application"
	"github.com/skill/exchange/internal/wallet/domain"
)

// A callback is taken only on the provider's lower-case path, the one the
// edge proxy guards; another case is no such callback.
func TestACallbackPathIsLowerCase(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{}).Routes(r)
	for _, path := range []string{"/v1/wallet/callbacks/UDUN", "/v1/wallet/callbacks/Udun"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader("timestamp=1")))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}

// A withdrawal's user sees why it ended, not the custodian's words while
// it is with the custodian; the reviewers see both.
func TestTheReasonShowsOnceAWithdrawalEnded(t *testing.T) {
	wd := domain.Withdrawal{
		ID: "w", Status: domain.WithdrawalSubmitted, ProviderStatus: domain.CustodyUncertain,
		RejectReason: "UNCERTAIN: code 4001: insufficient balance",
	}
	if j := WithdrawalJSONOf(wd); j.RejectReason != nil {
		t.Fatalf("shown to the user while with the custodian: %q", *j.RejectReason)
	}
	if j := AdminWithdrawalJSONOf(wd); j.RejectReason == nil || !strings.HasPrefix(*j.RejectReason, "UNCERTAIN") {
		t.Fatalf("hidden from the reviewers: %+v", j.RejectReason)
	}
	wd.Status, wd.RejectReason = domain.WithdrawalFailed, "CUSTODY_FAILED: 0xbeef"
	if j := WithdrawalJSONOf(wd); j.RejectReason == nil || *j.RejectReason != "CUSTODY_FAILED: 0xbeef" {
		t.Fatalf("a failed one: %+v", j.RejectReason)
	}
}

// The callback path takes only the custodian's addresses when they are
// known (UDUN_CALLBACK_ALLOWED_IPS), any address otherwise: the signature
// decides then (2026-10-03, review AB). Each provider has its own list
// (ADR-0017): the stand-in's network does not open the real gateway's
// path, nor the other way round.
func TestCallbacksComeFromTheAllowedAddresses(t *testing.T) {
	send := func(h *Handler, path, from string) int {
		t.Helper()
		r := chi.NewRouter()
		r.Use(httpx.ClientIP(httpx.DefaultTrustedProxies))
		h.Routes(r)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader("timestamp=1"))
		req.RemoteAddr = from + ":4321"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	allowed := map[string][]netip.Prefix{domain.ProviderUdun: {netip.MustParsePrefix("203.0.113.0/24")}}
	if code := send(&Handler{CallbackFrom: allowed}, "/v1/wallet/callbacks/udun", "198.51.100.7"); code != http.StatusForbidden {
		t.Fatalf("from elsewhere: %d", code)
	}
	// Past the gate the service decides: no custodian here, no such callback.
	svc := &application.Service{}
	if code := send(&Handler{Svc: svc, CallbackFrom: allowed}, "/v1/wallet/callbacks/udun", "203.0.113.9"); code != http.StatusNotFound {
		t.Fatalf("from an allowed address: %d", code)
	}
	if code := send(&Handler{Svc: svc}, "/v1/wallet/callbacks/udun", "198.51.100.7"); code != http.StatusNotFound {
		t.Fatalf("no allow list, any address: %d", code)
	}
	// Another provider's list is its own.
	allowed[domain.ProviderUdunMock] = []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}
	if code := send(&Handler{Svc: svc, CallbackFrom: allowed}, "/v1/wallet/callbacks/udunmock", "203.0.113.9"); code != http.StatusForbidden {
		t.Fatalf("the stand-in's path from the real gateway's addresses: %d", code)
	}
	if code := send(&Handler{Svc: svc, CallbackFrom: allowed}, "/v1/wallet/callbacks/udun", "172.18.0.5"); code != http.StatusForbidden {
		t.Fatalf("the real gateway's path from the stand-in's network: %d", code)
	}
	if code := send(&Handler{Svc: svc, CallbackFrom: allowed}, "/v1/wallet/callbacks/udunmock", "172.18.0.5"); code != http.StatusNotFound {
		t.Fatalf("the stand-in's path from its network: %d", code)
	}
}
