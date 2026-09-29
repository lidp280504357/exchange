package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := CORS([]string{"tauri://localhost", "http://tauri.localhost"})(next)
	serve := func(method, origin, preflight string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, "/v1/orders", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if preflight != "" {
			req.Header.Set("Access-Control-Request-Method", preflight)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := serve(http.MethodOptions, "tauri://localhost", "POST")
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "tauri://localhost" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "" || rec.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}
	rec = serve(http.MethodPost, "http://tauri.localhost", "")
	if rec.Code != http.StatusTeapot || rec.Header().Get("Access-Control-Allow-Origin") != "http://tauri.localhost" ||
		rec.Header().Get("Access-Control-Expose-Headers") == "" {
		t.Fatalf("request: %d %v", rec.Code, rec.Header())
	}
	for _, origin := range []string{"https://evil.example", ""} {
		rec = serve(http.MethodOptions, origin, "POST")
		if rec.Code != http.StatusTeapot || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("origin %q: %d %v", origin, rec.Code, rec.Header())
		}
	}
	if got := CORS(nil)(next); got == nil {
		t.Fatal("no origins must pass requests through")
	}
}
