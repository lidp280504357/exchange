package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMoneyRoutesNeedAKey(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for key, want := range map[string]int{
		"": http.StatusBadRequest, "   ": http.StatusBadRequest, strings.Repeat("k", 129): http.StatusBadRequest,
		"0192a000-0000-7000-8000-000000000001": http.StatusNoContent,
	} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/v1/users/someone/holds", nil)
		if key != "" {
			r.Header.Set(KeyHeader, key)
		}
		w := httptest.NewRecorder()
		needKey(next).ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("key %q: %d, want %d (%s)", key, w.Code, want, w.Body)
		}
	}
}
