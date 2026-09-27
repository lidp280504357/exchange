// Command notification-service delivers OTP codes and user notifications
// through mail and SMS providers, and keeps the in-app inbox
// (requirements §5.3, §6.2).
package main

import (
	"context"
	"errors"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/pg"
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
	app.Main("notification-service", setup, app.WithDefaultOpsAddr(":9083"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8083", GRPCAddr: ":9183", Postgres: pg.DefaultConfig()}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	if _, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "notify", nil); err != nil {
		return err
	}
	if _, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr); err != nil {
		return err
	}
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, a.NewRouter())
}
