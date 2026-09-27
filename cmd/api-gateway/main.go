// Command api-gateway is the public REST and WebSocket entry point
// (requirements §5.1). nginx forwards /v1/ to it on port 8080.
//
// Routing, authentication and rate limiting are not implemented yet, so
// every request gets 404.
package main

import (
	"context"
	"net/http"

	"github.com/lidp280504357/exchange/internal/platform/app"
)

type settings struct {
	// HTTPAddr is the public listen address (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
}

func main() {
	app.Main("api-gateway", setup)
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8080"}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	srv, err := app.NewHTTPServer(ctx, cfg.HTTPAddr, http.NotFoundHandler(), a.Logger())
	if err != nil {
		return err
	}
	a.Add("http", srv)
	return nil
}
