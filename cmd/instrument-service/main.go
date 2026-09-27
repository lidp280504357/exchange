// Command instrument-service owns assets, networks and trading pairs
// (requirements §5.5).
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
	app.Main("instrument-service", setup, app.WithDefaultOpsAddr(":9084"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8084", GRPCAddr: ":9184", Postgres: pg.DefaultConfig()}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	if _, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "instrument", nil); err != nil {
		return err
	}
	if _, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr); err != nil {
		return err
	}
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, a.NewRouter())
}
