package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/config"
)

func TestSetupServesAndShutsDown(t *testing.T) {
	// Reserve a free port, then hand it to the gateway through HTTP_ADDR.
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	vars := []string{"APP_ENV=test", "HTTP_ADDR=" + addr}
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

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/v1/anything", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
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
