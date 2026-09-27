// Command user-service owns user profiles, account status and eligibility
// (requirements §5.4).
package main

import (
	"context"
	"errors"
	_ "time/tzdata" // validate time zones without the OS database

	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/user/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/user/application"
	"github.com/lidp280504357/exchange/internal/user/transport/grpcapi"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// GRPCAddr serves synchronous calls from other services (GRPC_ADDR).
	GRPCAddr string    `koanf:"grpc_addr"`
	Postgres pg.Config `koanf:",squash"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate())
}

func main() {
	app.Main("user-service", setup, app.WithDefaultOpsAddr(":9082"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8082", GRPCAddr: ":9182", Postgres: pg.DefaultConfig()}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "users", migrations.Users())
	if err != nil {
		return err
	}
	svc := &application.Service{Users: postgres.NewStore(db)}

	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	userv1.RegisterUserServiceServer(srv, grpcapi.NewServer(svc))
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, a.NewRouter())
}
