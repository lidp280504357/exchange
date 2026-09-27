// Command auth-service owns identities, passwords, OTP challenges, tokens,
// device sessions and login history (requirements §5.2, §5.3, §6).
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
	"github.com/lidp280504357/exchange/internal/auth/adapters/humancheck"
	"github.com/lidp280504357/exchange/internal/auth/adapters/notifier"
	"github.com/lidp280504357/exchange/internal/auth/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/auth/application"
	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/auth/transport/httpapi"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/captcha"
	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
	"github.com/lidp280504357/exchange/internal/platform/redisx"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// GRPCAddr serves synchronous calls from other services (GRPC_ADDR).
	GRPCAddr string        `koanf:"grpc_addr"`
	Postgres pg.Config     `koanf:",squash"`
	Redis    redisx.Config `koanf:",squash"`
	Kafka    kafka.Config  `koanf:",squash"`
	// NotificationAddr is notification-service's gRPC address (NOTIFICATION_GRPC_ADDR).
	NotificationAddr string `koanf:"notification_grpc_addr"`
	// OTPHMACKey is the base64 key of the code hashes (OTP_HMAC_KEY).
	OTPHMACKey string `koanf:"otp_hmac_key"`
	// SMSHourlyLimit and SMSDailyLimit cap all SMS sends (§6.3).
	SMSHourlyLimit int `koanf:"sms_hourly_limit"`
	SMSDailyLimit  int `koanf:"sms_daily_limit"`
	// Turnstile settings (TURNSTILE_SECRET, TURNSTILE_HOSTNAMES).
	TurnstileSecret    string   `koanf:"turnstile_secret"`
	TurnstileHostnames []string `koanf:"turnstile_hostnames"`
	// CaptchaBypassToken passes human verification outside production, for
	// end-to-end tests (CAPTCHA_BYPASS_TOKEN).
	CaptchaBypassToken string `koanf:"captcha_bypass_token"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Redis.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("auth-service", setup, app.WithDefaultOpsAddr(":9081"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr:         ":8081",
		GRPCAddr:         ":9181",
		Postgres:         pg.DefaultConfig(),
		NotificationAddr: "localhost:9183",
		SMSHourlyLimit:   200,
		SMSDailyLimit:    1000,
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	env := a.Config().Env
	hasher, err := codeHasher(a, cfg.OTPHMACKey)
	if err != nil {
		return err
	}
	human, err := humanCheck(env, cfg)
	if err != nil {
		return err
	}

	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "auth", migrations.Auth())
	if err != nil {
		return err
	}
	rdb, err := bootstrap.Redis(ctx, a, cfg.Redis)
	if err != nil {
		return err
	}
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	conn, err := bootstrap.GRPCClient(a, "notification", cfg.NotificationAddr)
	if err != nil {
		return err
	}

	tasks := app.NewTasks()
	a.Add("background tasks", tasks)
	otp := &application.OTPService{
		Store:    postgres.NewStore(db, events),
		Notifier: notifier.New(notificationv1.NewNotificationServiceClient(conn)),
		Captcha:  human,
		Limiter:  ratelimit.New(rdb, "auth:rl:"),
		Flags:    flagClient,
		Hasher:   hasher,
		SMS:      application.SMSBudget{Hourly: cfg.SMSHourlyLimit, Daily: cfg.SMSDailyLimit},
		Log:      a.Logger(),
		Now:      time.Now,
		Dispatch: func(fn func(context.Context)) {
			tasks.Go(func(ctx context.Context) {
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				fn(ctx)
			})
		},
	}

	if _, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr); err != nil {
		return err
	}
	r := a.NewRouter()
	(&httpapi.Handler{OTP: otp}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// codeHasher decodes OTP_HMAC_KEY. Only local development may run without
// one; it then uses a random key and pending codes die with the process.
func codeHasher(a *app.App, encoded string) (domain.CodeHasher, error) {
	if encoded == "" {
		if a.Config().Env != config.EnvLocal {
			return domain.CodeHasher{}, errors.New("OTP_HMAC_KEY is required outside local development")
		}
		a.Logger().Warn("OTP_HMAC_KEY is not set; using a random key for this process")
		return domain.NewCodeHasher(domain.RandomBytes(32))
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return domain.CodeHasher{}, errors.New("OTP_HMAC_KEY must be base64")
	}
	return domain.NewCodeHasher(key)
}

// humanCheck builds the Turnstile verifier and, outside production, the
// test bypass.
func humanCheck(env config.Env, cfg settings) (*humancheck.Verifier, error) {
	if cfg.CaptchaBypassToken != "" && env == config.EnvProd {
		return nil, errors.New("CAPTCHA_BYPASS_TOKEN must not be set in production")
	}
	var provider captcha.Verifier
	if cfg.TurnstileSecret != "" {
		t, err := captcha.NewTurnstile(captcha.TurnstileConfig{Secret: cfg.TurnstileSecret, Hostnames: cfg.TurnstileHostnames})
		if err != nil {
			return nil, err
		}
		provider = t
	}
	return humancheck.New(provider, cfg.CaptchaBypassToken), nil
}
