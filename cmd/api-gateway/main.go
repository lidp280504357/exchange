// Command api-gateway is the public REST and WebSocket entry point
// (requirements §5.1). nginx forwards /v1/ to it on port 8080; it answers
// GET /v1/time itself and forwards the rest to the owning services.
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/lidp280504357/exchange/internal/gateway"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

type settings struct {
	// HTTPAddr is the public listen address (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// Upstream REST addresses (AUTH_SERVICE_URL, NOTIFICATION_SERVICE_URL).
	AuthURL         string `koanf:"auth_service_url"`
	NotificationURL string `koanf:"notification_service_url"`
}

func main() {
	app.Main("api-gateway", setup, app.WithDefaultOpsAddr(":9080"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr:        ":8080",
		AuthURL:         "http://localhost:8081",
		NotificationURL: "http://localhost:8083",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	auth, err := upstream(cfg.AuthURL)
	if err != nil {
		return err
	}
	notification, err := upstream(cfg.NotificationURL)
	if err != nil {
		return err
	}

	r := a.NewRouter()
	r.Get("/v1/time", serverTime(time.Now))
	r.Handle("/v1/auth/*", gateway.NewProxy(auth))
	if a.Config().Env != config.EnvProd {
		// Dev inbox of the mock providers (codes sent by SMS or to test mail
		// domains); never routed in production.
		r.Handle("/v1/dev/*", gateway.NewProxy(notification))
	}

	srv, err := app.NewHTTPServer(ctx, cfg.HTTPAddr, r, a.Logger())
	if err != nil {
		return err
	}
	a.Add("http", srv)
	return nil
}

func upstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid upstream URL %q", raw)
	}
	return u, nil
}

type serverTimeResponse struct {
	ServerTime string `json:"server_time"`
	EpochMS    int64  `json:"epoch_ms"`
}

func serverTime(now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		t := now()
		httpx.WriteJSON(w, http.StatusOK, serverTimeResponse{ServerTime: httpx.FormatTime(t), EpochMS: t.UnixMilli()})
	}
}
