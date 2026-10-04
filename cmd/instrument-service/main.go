// Command instrument-service owns assets, networks, trading pairs and fee
// schedules (requirements §5.5).
package main

import (
	"context"
	"errors"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/skill/exchange/internal/instrument/adapters/postgres"
	"github.com/skill/exchange/internal/instrument/application"
	"github.com/skill/exchange/internal/instrument/transport/grpcapi"
	"github.com/skill/exchange/internal/instrument/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// GRPCAddr serves synchronous calls from other services (GRPC_ADDR).
	GRPCAddr string       `koanf:"grpc_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("instrument-service", setup, app.WithDefaultOpsAddr(":9084"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8084", GRPCAddr: ":9184", Postgres: pg.DefaultConfig()}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "instrument", migrations.Instrument())
	if err != nil {
		return err
	}
	// Publishes the events exchangectl queues in the instrument outbox.
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	svc := &application.Service{Store: postgres.NewStore(db, events)}

	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	instrumentv1.RegisterInstrumentServiceServer(srv, grpcapi.NewServer(svc))
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}
