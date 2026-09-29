package gateway

import (
	"net/http"
	"slices"
	"strings"
)

// CORS lets the listed origins call the API from another origin
// (requirements §6.6): the desktop app's WebView is served from its own
// origin (tauri://localhost, or http(s)://tauri.localhost on Windows) and
// signs in with bearer tokens, never cookies, so credentials are not
// allowed. The H5 is same-origin and needs none of this. Preflights of an
// allowed origin are answered here; other origins get no CORS headers,
// which the browser treats as a refusal.
func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := slices.Clone(origins)
	return func(next http.Handler) http.Handler {
		if len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" || !slices.Contains(allowed, origin) {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", strings.Join(exposedHeaders, ", "))
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Request headers the clients send (§7.1), and response headers they read.
var (
	allowedHeaders = []string{
		"Authorization", "Content-Type", "Idempotency-Key", "X-Client-Type", "X-Step-Up-Token", "X-Request-Id", "Accept-Language",
	}
	exposedHeaders = []string{
		"X-Trace-Id", "X-Request-Id", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After",
		"Idempotent-Replayed",
	}
)
