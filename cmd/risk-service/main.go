// Command risk-service applies anti-abuse rules and publishes risk events
// (requirements §5.13).
package main

import (
	"context"
	"errors"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/redisx"
)

type settings struct {
	// GRPCAddr serves synchronous calls from other services (GRPC_ADDR).
	GRPCAddr string        `koanf:"grpc_addr"`
	Postgres pg.Config     `koanf:",squash"`
	Redis    redisx.Config `koanf:",squash"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Redis.Validate())
}

func main() {
	app.Main("risk-service", setup, app.WithDefaultOpsAddr(":9086"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{GRPCAddr: ":9186", Postgres: pg.DefaultConfig()}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	if _, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "risk", nil); err != nil {
		return err
	}
	if _, err := bootstrap.Redis(ctx, a, cfg.Redis); err != nil {
		return err
	}
	if _, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr); err != nil {
		return err
	}
	return nil
}
