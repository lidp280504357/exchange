// Command auth-service owns identities, passwords, OTP challenges, tokens,
// device sessions and login history (requirements §5.2, §5.3, §6).
package main

import (
	"context"
	"errors"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/pg"
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
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Redis.Validate())
}

func main() {
	app.Main("auth-service", setup, app.WithDefaultOpsAddr(":9081"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8081", GRPCAddr: ":9181", Postgres: pg.DefaultConfig()}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	if _, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "auth", migrations.Auth()); err != nil {
		return err
	}
	if _, err := bootstrap.Redis(ctx, a, cfg.Redis); err != nil {
		return err
	}
	if _, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr); err != nil {
		return err
	}
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, a.NewRouter())
}
