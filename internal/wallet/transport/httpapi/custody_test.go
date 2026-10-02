package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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
