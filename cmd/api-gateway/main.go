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

	"github.com/redis/go-redis/v9"

	"github.com/lidp280504357/exchange/internal/gateway"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/authtoken"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
	"github.com/lidp280504357/exchange/internal/platform/redisx"
)

type settings struct {
	// HTTPAddr is the public listen address (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// Upstream REST addresses (AUTH_SERVICE_URL, USER_SERVICE_URL,
	// NOTIFICATION_SERVICE_URL, INSTRUMENT_SERVICE_URL, LEDGER_SERVICE_URL,
	// TRADING_SERVICE_URL, MARKET_DATA_SERVICE_URL, WALLET_SERVICE_URL,
	// DERIVATIVES_SERVICE_URL).
	AuthURL         string `koanf:"auth_service_url"`
	UserURL         string `koanf:"user_service_url"`
	NotificationURL string `koanf:"notification_service_url"`
	InstrumentURL   string `koanf:"instrument_service_url"`
	LedgerURL       string `koanf:"ledger_service_url"`
	TradingURL      string `koanf:"trading_service_url"`
	MarketURL       string `koanf:"market_data_service_url"`
	WalletURL       string `koanf:"wallet_service_url"`
	DerivativesURL  string `koanf:"derivatives_service_url"`
	// Redis holds the session revocation marks auth-service sets, the rate
	// limit counters and the idempotency cache.
	Redis redisx.Config `koanf:",squash"`
	// Kafka carries the private events pushed over WebSocket.
	Kafka kafka.Config `koanf:",squash"`
	// WSOrigins are the browser origins (host patterns) allowed to open
	// /v1/ws (WS_ORIGINS).
	WSOrigins []string `koanf:"ws_origins"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Redis.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("api-gateway", setup, app.WithDefaultOpsAddr(":9080"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr:        ":8080",
		AuthURL:         "http://localhost:8081",
		UserURL:         "http://localhost:8082",
		NotificationURL: "http://localhost:8083",
		InstrumentURL:   "http://localhost:8084",
		LedgerURL:       "http://localhost:8085",
		TradingURL:      "http://localhost:8088",
		MarketURL:       "http://localhost:8090",
		WalletURL:       "http://localhost:8092",
		DerivativesURL:  "http://localhost:8095",
		WSOrigins:       []string{"astras.vip", "m.astras.vip", "localhost:5173", "localhost:5174"},
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	authURL, err := upstream(cfg.AuthURL)
	if err != nil {
		return err
	}
	userURL, err := upstream(cfg.UserURL)
	if err != nil {
		return err
	}
	notificationURL, err := upstream(cfg.NotificationURL)
	if err != nil {
		return err
	}
	instrumentURL, err := upstream(cfg.InstrumentURL)
	if err != nil {
		return err
	}
	ledgerURL, err := upstream(cfg.LedgerURL)
	if err != nil {
		return err
	}
	tradingURL, err := upstream(cfg.TradingURL)
	if err != nil {
		return err
	}
	marketURL, err := upstream(cfg.MarketURL)
	if err != nil {
		return err
	}
	walletURL, err := upstream(cfg.WalletURL)
	if err != nil {
		return err
	}
	derivativesURL, err := upstream(cfg.DerivativesURL)
	if err != nil {
		return err
	}
	rdb, err := bootstrap.Redis(ctx, a, cfg.Redis)
	if err != nil {
		return err
	}

	authn := &gateway.Authenticator{
		Verifier: authtoken.NewVerifier(authtoken.HTTPKeys(http.DefaultClient, authURL.JoinPath("/internal/jwks").String())),
		State: func(ctx context.Context, sessionID, userID string) (bool, int64, error) {
			pipe := rdb.Pipeline()
			revoked := pipe.Exists(ctx, authtoken.RevokedKey(sessionID))
			stale := pipe.Get(ctx, authtoken.StaleKey(userID))
			if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
				return false, 0, err
			}
			upTo, _ := stale.Int64()
			return revoked.Val() > 0, upTo, nil
		},
		Log: a.Logger(),
		Now: time.Now,
	}
	notification := gateway.NewProxy(notificationURL)
	up := gateway.Upstreams{
		Auth: gateway.NewProxy(authURL), User: gateway.NewProxy(userURL), Notification: notification,
		Instrument: gateway.NewProxy(instrumentURL), Ledger: gateway.NewProxy(ledgerURL), Trading: gateway.NewProxy(tradingURL),
		Market: gateway.NewProxy(marketURL), Wallet: gateway.NewProxy(walletURL), Derivatives: gateway.NewProxy(derivativesURL),
	}
	if a.Config().Env != config.EnvProd {
		// Dev inbox of the mock providers (codes sent by SMS or to test mail
		// domains); never routed in production.
		up.DevInbox = notification
	}

	hub := gateway.NewHub(authn, cfg.WSOrigins, a.Logger(), a.Metrics())
	a.Add("websocket hub", hub)
	if err := bootstrap.Tail(ctx, a, cfg.Kafka, gateway.WSTopics, gateway.WSEvents(hub)); err != nil {
		return err
	}
	guards := gateway.Guards{
		Authn:       authn,
		Limits:      &gateway.Limits{Limiter: ratelimit.New(rdb, "gw:rl:"), Log: a.Logger()},
		Idempotency: &gateway.Idempotency{Redis: rdb, Log: a.Logger()},
		WS:          hub,
	}

	r := a.NewRouter()
	r.Get("/v1/time", serverTime(time.Now))
	gateway.Mount(r, guards, up)
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
