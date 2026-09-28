// Command analytics-consumer copies every business event into ClickHouse
// in batches and reconciles the counts with the service outboxes
// (requirements §9, phase 1 acceptance criterion 8).
package main

import (
	"context"
	"errors"

	"github.com/lidp280504357/exchange/internal/analytics"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/chx"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	Kafka      kafka.Config `koanf:",squash"`
	ClickHouse chx.Config   `koanf:",squash"`
	Postgres   pg.Config    `koanf:",squash"`
	// ReconcileSchemas lists the schemas whose outboxes are compared with
	// ClickHouse (RECONCILE_SCHEMAS, comma-separated): the services', and
	// config, where exchangectl queues the audit events of flag changes.
	ReconcileSchemas []string `koanf:"reconcile_schemas"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Kafka.Validate(), s.ClickHouse.Validate(), s.Postgres.Validate())
}

func main() {
	app.Main("analytics-consumer", setup, app.WithDefaultOpsAddr(":9087"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		Postgres:         pg.DefaultConfig(),
		ReconcileSchemas: []string{"auth", "users", "notify", "instrument", "ledger", "risk", "trading", "matching", "wallet", "config"},
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	conn, err := bootstrap.ClickHouse(ctx, a, cfg.ClickHouse, migrations.ClickHouse())
	if err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "analytics", nil)
	if err != nil {
		return err
	}

	ingestor := analytics.NewIngestor(conn, a.Logger(), a.Metrics())
	if err := bootstrap.BatchConsumer(ctx, a, cfg.Kafka, "analytics-consumer", analytics.Topics, ingestor.Store); err != nil {
		return err
	}
	reconciler := analytics.NewReconciler(db, conn, cfg.ReconcileSchemas, a.Logger(), a.Metrics())
	a.Add("reconciler", app.Loop(reconciler.Run))
	return nil
}
