// Command auth-service owns identities, passwords, OTP challenges, tokens,
// device sessions and login history (requirements §5.2, §5.3, §6).
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	notificationv1 "github.com/skill/exchange/api/gen/go/exchange/notification/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/auth/adapters/humancheck"
	"github.com/skill/exchange/internal/auth/adapters/metrics"
	"github.com/skill/exchange/internal/auth/adapters/notifier"
	"github.com/skill/exchange/internal/auth/adapters/postgres"
	"github.com/skill/exchange/internal/auth/adapters/redisstore"
	"github.com/skill/exchange/internal/auth/adapters/users"
	"github.com/skill/exchange/internal/auth/application"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/auth/transport/consumer"
	"github.com/skill/exchange/internal/auth/transport/grpcapi"
	"github.com/skill/exchange/internal/auth/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/authtoken"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/captcha"
	"github.com/skill/exchange/internal/platform/config"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/ratelimit"
	"github.com/skill/exchange/internal/platform/redisx"
	"github.com/skill/exchange/internal/platform/secretbox"
	"github.com/skill/exchange/migrations"
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
	// UserAddr is user-service's gRPC address (USER_GRPC_ADDR).
	UserAddr string `koanf:"user_grpc_addr"`
	// JWTSigningKey is the base64 32-byte Ed25519 seed of the access
	// tokens (JWT_SIGNING_KEY), published under JWTKeyID (JWT_KEY_ID).
	JWTSigningKey string `koanf:"jwt_signing_key"`
	JWTKeyID      string `koanf:"jwt_key_id"`
	// TOTPSecretKey seals authenticator app secrets: base64, 32 bytes
	// (TOTP_SECRET_KEY). Without it authenticator apps are unavailable.
	TOTPSecretKey string `koanf:"totp_secret_key"`
	// Versions of the terms and risk disclosure users accept at
	// registration (TERMS_VERSION, RISK_DISCLOSURE_VERSION).
	TermsVersion string `koanf:"terms_version"`
	RiskVersion  string `koanf:"risk_disclosure_version"`
	// LoginSilence is how long without a login triggers the OTP challenge
	// after a correct password (LOGIN_SILENCE, ADR-0009).
	LoginSilence time.Duration `koanf:"login_silence"`
	// AllowedOrigins may refresh tokens with the cookie (ALLOWED_ORIGINS).
	AllowedOrigins []string `koanf:"allowed_origins"`
	// PasswordHashConcurrency caps parallel Argon2id hashes of 64 MiB each
	// (PASSWORD_HASH_CONCURRENCY).
	PasswordHashConcurrency int `koanf:"password_hash_concurrency"`
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
		UserAddr:         "localhost:9182",
		TermsVersion:     "2026-09-28",
		RiskVersion:      "2026-09-28",
		LoginSilence:     domain.LoginSilence,
		AllowedOrigins:   []string{"https://astras.vip", "https://m.astras.vip", "http://localhost:5173", "http://localhost:5174"},

		PasswordHashConcurrency: 2,
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
	// The launch checklist's "human verification configured" (design
	// 2026-10-04 §4.6); only whether, never the secret.
	if err := bootstrap.ConfigPresent(a, map[string]bool{"turnstile": cfg.TurnstileSecret != ""}); err != nil {
		return err
	}
	signer, err := tokenSigner(a, cfg.JWTSigningKey, cfg.JWTKeyID)
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
	userConn, err := bootstrap.GRPCClient(a, "user", cfg.UserAddr)
	if err != nil {
		return err
	}
	store := postgres.NewStore(db, events)
	a.Add("auth janitor", app.Loop(func(ctx context.Context) error { return purge(ctx, a, store) }))

	tasks := app.NewTasks()
	a.Add("background tasks", tasks)
	otp := &application.OTPService{
		Store:    store,
		Notifier: notifier.New(notificationv1.NewNotificationServiceClient(conn)),
		Captcha:  human,
		Limiter:  ratelimit.New(rdb, "auth:rl:"),
		Flags:    flagClient,
		Hasher:   hasher,
		SMS:      application.SMSBudget{Hourly: cfg.SMSHourlyLimit, Daily: cfg.SMSDailyLimit},
		Log:      a.Logger(),
		Metrics:  metrics.NewOTP(a.Metrics()),
		Tester:   human.IsBypass,
		Now:      time.Now,
		Dispatch: func(fn func(context.Context)) {
			tasks.Go(func(ctx context.Context) {
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				fn(ctx)
			})
		},
	}

	accounts := &application.AccountService{
		Store:       store,
		Users:       users.New(userv1.NewUserServiceClient(userConn)),
		Passwords:   domain.NewPasswordHasher(cfg.PasswordHashConcurrency, domain.DefaultPasswordCost),
		Tokens:      signer,
		Revocations: redisstore.NewRevocations(rdb),
		Guard:       redisstore.NewGuard(rdb, "auth:"),
		Captcha:     human,
		Config: application.AccountConfig{
			TermsVersion: cfg.TermsVersion, RiskVersion: cfg.RiskVersion, LoginSilence: cfg.LoginSilence,
		},
		Log: a.Logger(),
		Now: time.Now,
	}
	if cfg.TOTPSecretKey != "" {
		box, err := secretbox.New(cfg.TOTPSecretKey)
		if err != nil {
			return fmt.Errorf("TOTP_SECRET_KEY: %w", err)
		}
		accounts.TOTP = box
	} else {
		a.Logger().Warn("authenticator apps unavailable: TOTP_SECRET_KEY is not set")
	}

	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, application.Consumer, []string{event.TopicUser}, consumer.Handler(accounts)); err != nil {
		return err
	}

	gsrv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	authv1.RegisterAuthServiceServer(gsrv, grpcapi.NewServer(accounts))
	r := a.NewRouter()
	(&httpapi.Handler{OTP: otp}).Routes(r)
	(&httpapi.Accounts{
		Svc: accounts, OTP: otp, JWKS: signer.JWKS(), AllowedOrigins: cfg.AllowedOrigins, SecureCookies: env != config.EnvLocal,
	}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// purge deletes expired codes, tickets and tokens every hour, a day after
// they expire.
func purge(ctx context.Context, a *app.App, store *postgres.Store) error {
	for {
		n, err := store.Purge(ctx, time.Now().Add(-24*time.Hour))
		switch {
		case err != nil && ctx.Err() == nil:
			a.Logger().WarnContext(ctx, "purging expired auth records failed", "error", err)
		case n > 0:
			a.Logger().InfoContext(ctx, "purged expired auth records", "rows", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Hour):
		}
	}
}

// tokenSigner decodes JWT_SIGNING_KEY. Only local development may run
// without one; tokens then die with the process.
func tokenSigner(a *app.App, encoded, kid string) (*authtoken.Signer, error) {
	if encoded == "" {
		if a.Config().Env != config.EnvLocal {
			return nil, errors.New("JWT_SIGNING_KEY is required outside local development")
		}
		a.Logger().Warn("JWT_SIGNING_KEY is not set; using a random key for this process")
		return authtoken.NewSigner(domain.RandomBytes(32), "local")
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, errors.New("JWT_SIGNING_KEY must be base64")
	}
	if kid == "" {
		return nil, errors.New("JWT_KEY_ID is required with JWT_SIGNING_KEY")
	}
	return authtoken.NewSigner(seed, kid)
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
