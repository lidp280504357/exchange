// Command analytics-consumer copies every business event into ClickHouse
// in batches and reconciles the counts with the service outboxes
// (requirements §9, phase 1 acceptance criterion 8).
package main

import (
	"context"
	"errors"
	"time"

	"github.com/skill/exchange/internal/analytics"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/chx"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/migrations"
)

type settings struct {
	Kafka      kafka.Config `koanf:",squash"`
	ClickHouse chx.Config   `koanf:",squash"`
	Postgres   pg.Config    `koanf:",squash"`
	// ReconcileSchemas lists the schemas whose outboxes are compared with
	// ClickHouse (RECONCILE_SCHEMAS, comma-separated): the services', and
	// config, where exchangectl and the admin console queue the audit
	// events of flag changes.
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
		ReconcileSchemas: []string{"auth", "users", "notify", "instrument", "ledger", "risk", "trading", "matching", "deriv_matching", "derivatives", "market", "wallet", "admin", "marketsim", "config"},
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
	// Events stored before the read models existed are projected once.
	a.Add("read model backfill", app.Loop(func(ctx context.Context) error {
		for {
			err := ingestor.BackfillReadModels(ctx, 5000)
			if err == nil {
				<-ctx.Done()
				return ctx.Err()
			}
			a.Logger().WarnContext(ctx, "read model backfill failed; retrying in a minute", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Minute):
			}
		}
	}))
	reconciler := analytics.NewReconciler(db, conn, cfg.ReconcileSchemas, a.Logger(), a.Metrics())
	reconciler.SetRetention(a.Config().OutboxRetention)
	a.Add("reconciler", app.Loop(reconciler.Run))
	return nil
}
