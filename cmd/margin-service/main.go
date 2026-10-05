// Command margin-service owns margin trading (design 2026-10-06): margin
// accounts, borrowing from HOUSE, repaying, the hourly interest, margin
// levels, warnings and liquidations. Skeleton of batch E0: it starts and
// reports ready; batch E1 adds its store, the ledger and price clients,
// the endpoints of api/openapi/margin.yaml and the compose service.
package main

import (
	"context"

	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls for
	// /v1/margin/* (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// GRPCAddr serves spot-trading-service's checks of orders on margin
	// accounts from batch E2 (GRPC_ADDR).
	GRPCAddr string `koanf:"grpc_addr"`
}

func main() {
	app.Main("margin-service", setup, app.WithDefaultOpsAddr(":9099"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8099", GRPCAddr: ":9199"}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, a.NewRouter())
}
