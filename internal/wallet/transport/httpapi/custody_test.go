package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/wallet/domain"
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
