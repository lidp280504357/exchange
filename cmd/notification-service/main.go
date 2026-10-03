// Command notification-service delivers OTP codes and user notifications
// through mail and SMS providers, and keeps the in-app inbox
// (requirements §5.3, §6.2).
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/notification/adapters/mock"
	"github.com/lidp280504357/exchange/internal/notification/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/notification/adapters/recipients"
	"github.com/lidp280504357/exchange/internal/notification/adapters/resend"
	"github.com/lidp280504357/exchange/internal/notification/application"
	"github.com/lidp280504357/exchange/internal/notification/ports"
	"github.com/lidp280504357/exchange/internal/notification/transport/consumer"
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
	// gRPC addresses of the services that know the recipients
	// (USER_GRPC_ADDR, AUTH_GRPC_ADDR).
	UserAddr string `koanf:"user_grpc_addr"`
	AuthAddr string `koanf:"auth_grpc_addr"`
	// How long in-app notices (and the broadcasts that made them) and
	// delivery records are kept (NOTICE_RETENTION, DELIVERY_RETENTION;
	// 180 and 90 days when zero), and how many queued mails go out each
	// second (NOTICE_MAIL_RATE, 2 when zero; C5.5 ⑫).
	NoticeRetention   time.Duration `koanf:"notice_retention"`
	DeliveryRetention time.Duration `koanf:"delivery_retention"`
	MailRate          int           `koanf:"notice_mail_rate"`
}

func (s *settings) Validate() error {
	var keep error
	for name, d := range map[string]time.Duration{"NOTICE_RETENTION": s.NoticeRetention, "DELIVERY_RETENTION": s.DeliveryRetention} {
		// A short keep would delete the inbox on the next round (C5.5 ㉓).
		if d != 0 && d < application.MinRetention {
			keep = errors.Join(keep, fmt.Errorf("%s must be at least %s (or unset)", name, application.MinRetention))
		}
	}
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate(), keep)
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
		UserAddr:         "localhost:9182",
		AuthAddr:         "localhost:9181",
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
	store := postgres.NewStore(db, events)
	dispatcher := application.NewDispatcher(routes, store, a.Logger(), a.Metrics())
	a.Add("dispatcher", dispatcher)

	userConn, err := bootstrap.GRPCClient(a, "user", cfg.UserAddr)
	if err != nil {
		return err
	}
	authConn, err := bootstrap.GRPCClient(a, "auth", cfg.AuthAddr)
	if err != nil {
		return err
	}
	people := recipients.New(userv1.NewUserServiceClient(userConn), authv1.NewAuthServiceClient(authConn))
	notices := &application.Notices{
		Store:      store,
		Recipients: people,
		Dispatcher: dispatcher,
		Log:        a.Logger(),
		Now:        time.Now,
	}
	// Operations content (design 2026-10-02 §4.5): articles for the sites,
	// the operators' in-app messages delivered in rounds.
	content := &application.Content{Store: store, Now: time.Now}
	broadcasts := &application.Broadcasts{Store: store, Notices: notices, Directory: people, Log: a.Logger(), Now: time.Now}
	a.Add("broadcasts", app.Loop(func(ctx context.Context) error {
		for {
			if n, err := broadcasts.Round(ctx); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, "broadcast round failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "broadcast notices created", "count", n)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
	}))
	// A broadcast's mails, queued with its notices, go out at the
	// providers' pace and are tried again later (C5.5 ⑫).
	queue := &application.MailQueue{
		Queue: store, Notices: store, Recipients: people, Dispatcher: dispatcher, Log: a.Logger(), Now: time.Now, PerRound: cfg.MailRate,
	}
	a.Add("mail queue", app.Loop(func(ctx context.Context) error {
		for {
			if _, err := queue.Round(ctx); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, "mail queue round failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}))
	retention := &application.Retention{Store: store, NoticeKeep: cfg.NoticeRetention, DeliveryKeep: cfg.DeliveryRetention, Now: time.Now}
	a.Add("retention", app.Loop(func(ctx context.Context) error {
		for {
			if n, b, d, err := retention.Purge(ctx); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, "retention failed", "error", err)
			} else if n+b+d > 0 {
				a.Logger().InfoContext(ctx, "retention deleted old rows", "notices", n, "broadcasts", b, "deliveries", d)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Hour):
			}
		}
	}))
	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, application.Consumer, consumer.Topics, consumer.Handler(notices)); err != nil {
		return err
	}

	gsrv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	notificationv1.RegisterNotificationServiceServer(gsrv, grpcapi.NewServer(dispatcher))

	r := a.NewRouter()
	(&httpapi.Notices{Svc: notices}).Routes(r)
	(&httpapi.Content{Svc: content, Broadcasts: broadcasts}).Routes(r)
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
