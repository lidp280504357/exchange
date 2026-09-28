// Command matching-engine matches spot orders (requirements §5.7,
// ADR-0002): it consumes order.commands, keeps one book per symbol in
// memory, and publishes order.events and trade.events. Only the holder of
// the engine lease runs; another instance waits as a standby.
package main

import (
	"context"
	"errors"
	"time"

	"github.com/lidp280504357/exchange/internal/matching/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/matching/application"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// SnapshotEvery is how many commands of a partition may follow its last
	// snapshot (MATCHING_SNAPSHOT_EVERY).
	SnapshotEvery int `koanf:"matching_snapshot_every"`
	// WALRetention keeps applied commands this long after a snapshot covers
	// them (MATCHING_WAL_RETENTION).
	WALRetention time.Duration `koanf:"matching_wal_retention"`
}

func (s *settings) Validate() error {
	var errs []error
	if s.SnapshotEvery < 1 {
		errs = append(errs, errors.New("MATCHING_SNAPSHOT_EVERY must be at least 1"))
	}
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate())...)
}

func main() {
	app.Main("matching-engine", setup, app.WithDefaultOpsAddr(":9089"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{Postgres: pg.DefaultConfig(), SnapshotEvery: 1000, WALRetention: 24 * time.Hour}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "matching", migrations.Matching())
	if err != nil {
		return err
	}
	a.Logger().Info("waiting for the engine lease")
	lease, err := pg.AcquireLease(ctx, db, "matching-engine")
	if err != nil {
		return err
	}
	a.Cleanup("engine lease", lease.Release)
	// Losing the lease stops the process: a standby may take over.
	a.Add("engine lease", app.Loop(func(ctx context.Context) error { return lease.Hold(ctx, 5*time.Second) }))

	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	store := postgres.NewStore(db)
	engine := application.New(store, events, a.Logger(), a.Metrics(), cfg.SnapshotEvery)
	if err := engine.Recover(ctx); err != nil {
		return err
	}
	if err := bootstrap.BatchConsumerWith(ctx, a, cfg.Kafka, kafka.BatchOptions{
		Group: application.Group, Topics: []string{event.TopicOrderCommands}, Handler: engine.Handle,
		MaxBatch: 500, MaxWait: 20 * time.Millisecond,
	}); err != nil {
		return err
	}
	a.Add("wal purge", app.Loop(func(ctx context.Context) error {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			if n, err := store.PurgeWAL(ctx, time.Now().Add(-cfg.WALRetention)); err != nil {
				a.Logger().WarnContext(ctx, "wal purge failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "wal purged", "commands", n)
			}
		}
	}))
	return nil
}
