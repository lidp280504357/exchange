package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func get(t *testing.T, url string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func TestSetupServesAndShutsDown(t *testing.T) {
	shutdownTracing := tracing.Setup() // app.Main does this in production
	t.Cleanup(func() { _ = shutdownTracing(context.Background()) })
	addr := freeAddr(t)
	vars := []string{"APP_ENV=test", "HTTP_ADDR=" + addr, "OPS_ADDR=127.0.0.1:0"}
	a, err := app.New("api-gateway",
		app.WithLoader(config.Loader{Environ: func() []string { return vars }}),
		app.WithLogOutput(io.Discard),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := setup(t.Context(), a); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	status, header, body := get(t, "http://"+addr+"/v1/time")
	if status != http.StatusOK || header.Get(httpx.HeaderTraceID) == "" {
		t.Fatalf("time: %d %s", status, body)
	}
	status, _, body = get(t, "http://"+addr+"/v1/unknown")
	var e httpx.ErrorBody
	if err := json.Unmarshal(body, &e); err != nil || status != http.StatusNotFound || e.Code != "COMMON_NOT_FOUND" {
		t.Fatalf("unknown route: %d %s", status, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not shut down")
	}
}

func TestServerTimeFormat(t *testing.T) {
	fixed := time.Date(2026, 9, 28, 12, 0, 0, 123_456_789, time.FixedZone("CST", 8*3600))
	rr := httptest.NewRecorder()
	serverTime(func() time.Time { return fixed })(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/time", http.NoBody))

	var got serverTimeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ServerTime != "2026-09-28T04:00:00.123Z" || got.EpochMS != fixed.UnixMilli() {
		t.Fatalf("got %+v", got)
	}
}
