// Command user-service owns user profiles, account status and eligibility
// (requirements §5.4).
package main

import (
	"context"
	"errors"
	"time"
	_ "time/tzdata" // validate time zones without the OS database

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/user/adapters/authclient"
	"github.com/skill/exchange/internal/user/adapters/avatars"
	"github.com/skill/exchange/internal/user/adapters/postgres"
	"github.com/skill/exchange/internal/user/application"
	"github.com/skill/exchange/internal/user/transport/consumer"
	"github.com/skill/exchange/internal/user/transport/grpcapi"
	"github.com/skill/exchange/internal/user/transport/httpapi"
	"github.com/skill/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// GRPCAddr serves synchronous calls from other services (GRPC_ADDR).
	GRPCAddr string       `koanf:"grpc_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// AuthAddr is auth-service's gRPC address, for step-up tokens (AUTH_GRPC_ADDR).
	AuthAddr string `koanf:"auth_grpc_addr"`
	// AvatarDir is where the avatars' files go (AVATAR_DIR; nginx serves it
	// at /uploads/avatars/, design 2026-10-07, avatars and usernames);
	// without one avatar uploads are refused.
	AvatarDir string `koanf:"avatar_dir"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("user-service", setup, app.WithDefaultOpsAddr(":9082"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8082", GRPCAddr: ":9182", Postgres: pg.DefaultConfig(), AuthAddr: "localhost:9181"}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "users", migrations.Users())
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
	authConn, err := bootstrap.GRPCClient(a, "auth", cfg.AuthAddr)
	if err != nil {
		return err
	}
	svc := &application.Service{
		Store:   postgres.NewStore(db, events),
		Flags:   flagClient,
		StepUps: authclient.New(authv1.NewAuthServiceClient(authConn)),
		Now:     time.Now,
	}
	if cfg.AvatarDir != "" {
		dir, err := avatars.New(cfg.AvatarDir)
		if err != nil {
			return err
		}
		svc.Avatars = dir
	} else {
		a.Logger().Warn("avatar uploads are refused: set AVATAR_DIR")
	}
	// Reviews that risk rules enforce (risk.enforce).
	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, application.Consumer, []string{event.TopicRisk}, consumer.Handler(svc)); err != nil {
		return err
	}

	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	userv1.RegisterUserServiceServer(srv, grpcapi.NewServer(svc))
	r := a.NewRouter()
	h := &httpapi.Handler{Svc: svc}
	h.Routes(r)
	h.InternalRoutes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}
