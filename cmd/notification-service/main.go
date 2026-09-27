// Command notification-service delivers OTP codes and user notifications
// through mail and SMS providers, and keeps the in-app inbox
// (requirements §5.3, §6.2).
package main

import (
	"context"
	"errors"
	"time"

	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
	"github.com/lidp280504357/exchange/internal/notification/adapters/mock"
	"github.com/lidp280504357/exchange/internal/notification/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/notification/adapters/resend"
	"github.com/lidp280504357/exchange/internal/notification/application"
	"github.com/lidp280504357/exchange/internal/notification/ports"
	"github.com/lidp280504357/exchange/internal/notification/transport/grpcapi"
	"github.com/lidp280504357/exchange/internal/notification/transport/httpapi"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// GRPCAddr serves SendOtp to auth-service (GRPC_ADDR).
	GRPCAddr string        `koanf:"grpc_addr"`
	Postgres pg.Config     `koanf:",squash"`
	Kafka    kafka.Config  `koanf:",squash"`
	Resend   resend.Config `koanf:",squash"`
	// MockEmailDomains receive mail through the mock provider, so tests
	// never reach a real mailbox (MOCK_EMAIL_DOMAINS).
	MockEmailDomains []string `koanf:"mock_email_domains"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("notification-service", setup, app.WithDefaultOpsAddr(":9083"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr:         ":8083",
		GRPCAddr:         ":9183",
		Postgres:         pg.DefaultConfig(),
		Resend:           resend.Config{From: "Exchange <noreply@astras.vip>"},
		MockEmailDomains: []string{"example.com", "example.org", "example.net"},
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	prod := a.Config().Env == config.EnvProd
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "notify", migrations.Notify())
	if err != nil {
		return err
	}
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}

	// Providers, primary first (§5.3: one primary and one backup). SMS has
	// no contracted vendor yet, so it goes to the mock provider.
	mockProvider := mock.New("mock", db)
	routes := application.Routes{
		SMS:              []ports.Provider{mockProvider},
		Mock:             mockProvider,
		MockEmailDomains: cfg.MockEmailDomains,
	}
	if cfg.Resend.APIKey != "" {
		routes.Email = append(routes.Email, resend.New(cfg.Resend, nil))
	}
	if !prod {
		routes.Email = append(routes.Email, mockProvider)
	}
	dispatcher := application.NewDispatcher(routes, postgres.NewStore(db, events), a.Logger(), a.Metrics())
	a.Add("dispatcher", dispatcher)

	gsrv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	notificationv1.RegisterNotificationServiceServer(gsrv, grpcapi.NewServer(dispatcher))

	r := a.NewRouter()
	if !prod {
		r.Get("/v1/dev/messages", httpapi.DevInbox(mockProvider))
		a.Add("mock inbox purge", app.Loop(func(ctx context.Context) error {
			for {
				if n, err := mockProvider.Purge(ctx, time.Now().Add(-24*time.Hour)); err == nil && n > 0 {
					a.Logger().InfoContext(ctx, "purged mock messages", "rows", n)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Hour):
				}
			}
		}))
	}
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}
