// Command api-gateway is the public REST and WebSocket entry point
// (requirements §5.1). nginx forwards /v1/ to it on port 8080; it answers
// GET /v1/time itself, authenticates the rest and forwards it to the
// owning services.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/lidp280504357/exchange/internal/gateway"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/authtoken"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/redisx"
)

type settings struct {
	// HTTPAddr is the public listen address (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// Upstream REST addresses (AUTH_SERVICE_URL, NOTIFICATION_SERVICE_URL).
	AuthURL         string `koanf:"auth_service_url"`
	NotificationURL string `koanf:"notification_service_url"`
	// Redis holds the session revocation marks auth-service sets.
	Redis redisx.Config `koanf:",squash"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Redis.Validate())
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
	authURL, err := upstream(cfg.AuthURL)
	if err != nil {
		return err
	}
	notificationURL, err := upstream(cfg.NotificationURL)
	if err != nil {
		return err
	}
	rdb, err := bootstrap.Redis(ctx, a, cfg.Redis)
	if err != nil {
		return err
	}

	authn := &gateway.Authenticator{
		Verifier: authtoken.NewVerifier(authtoken.HTTPKeys(http.DefaultClient, authURL.JoinPath("/internal/jwks").String())),
		Revoked: func(ctx context.Context, sessionID string) (bool, error) {
			n, err := rdb.Exists(ctx, authtoken.RevokedKey(sessionID)).Result()
			return n > 0, err
		},
		Log: a.Logger(),
		Now: time.Now,
	}
	up := gateway.Upstreams{Auth: gateway.NewProxy(authURL)}
	if a.Config().Env != config.EnvProd {
		// Dev inbox of the mock providers (codes sent by SMS or to test mail
		// domains); never routed in production.
		up.DevInbox = gateway.NewProxy(notificationURL)
	}

	r := a.NewRouter()
	r.Get("/v1/time", serverTime(time.Now))
	gateway.Mount(r, authn, up)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
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
