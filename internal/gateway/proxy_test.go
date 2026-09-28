package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

func TestProxyForwardsContextAndStripsIdentity(t *testing.T) {
	_ = tracing.Setup()
	var got http.Header
	var path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, path = r.Header.Clone(), r.URL.Path
		w.Header().Set("Set-Cookie", "rt=abc; HttpOnly")
		// Services set these too, with the same values.
		w.Header().Set(httpx.HeaderTraceID, "0123456789abcdef0123456789abcdef")
		w.Header().Set(httpx.HeaderRequestID, "req-1")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)

	r := httpx.NewRouter(httpx.RouterOptions{Logger: slog.New(slog.DiscardHandler)})
	r.Handle("/v1/auth/*", NewProxy(target))
	gw := httptest.NewServer(r)
	defer gw.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, gw.URL+"/v1/auth/otp/request", http.NoBody)
	req.Header.Set(httpx.HeaderUserID, "forged-user")
	req.Header.Set(httpx.HeaderSessionID, "forged-session")
	req.Header.Set(httpx.HeaderRequestID, "req-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot || resp.Header.Get("Set-Cookie") == "" {
		t.Fatalf("response must pass through: %d %v", resp.StatusCode, resp.Header)
	}
	if n, m := len(resp.Header.Values(httpx.HeaderTraceID)), len(resp.Header.Values(httpx.HeaderRequestID)); n != 1 || m != 1 {
		t.Fatalf("trace and request IDs must appear once, got %d and %d: %v", n, m, resp.Header)
	}
	if path != "/v1/auth/otp/request" {
		t.Fatalf("path = %s", path)
	}
	if got.Get(httpx.HeaderUserID) != "" || got.Get(httpx.HeaderSessionID) != "" {
		t.Fatalf("client identity headers must be dropped: %v", got)
	}
	if got.Get(httpx.HeaderRequestID) != "req-1" || got.Get("Traceparent") == "" || got.Get(httpx.HeaderRealIP) != "127.0.0.1" {
		t.Fatalf("request context must be forwarded: %v", got)
	}
}

func TestProxyReportsUnavailableUpstream(t *testing.T) {
	target, _ := url.Parse("http://127.0.0.1:1") // nothing listens there
	r := httpx.NewRouter(httpx.RouterOptions{Logger: slog.New(slog.DiscardHandler)})
	r.Handle("/v1/auth/*", NewProxy(target))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/auth/x", http.NoBody))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %s", rr.Code, rr.Body)
	}
}
