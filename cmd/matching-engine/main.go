// Command matching-engine matches orders (requirements §5.7, ADR-0002):
// it consumes order commands and HOUSE's reference books (ADR-0015), keeps
// one book per symbol in memory, and publishes order and trade events and
// the books' own depth. Only the holder of the
// engine lease runs; another instance waits as a standby. MATCHING_SHARD
// picks the shard: spot (order.commands, schema matching) or derivatives,
// the perpetual contracts (derivatives.order.commands, schema
// deriv_matching; implementation plan §7.3 task 2), which runs as
// derivatives-engine.
package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/lidp280504357/exchange/internal/matching/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/matching/application"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
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
	// WALRetention keeps applied commands and reference books this long
	// after a snapshot covers them (MATCHING_WAL_RETENTION; reference books
	// make the spot WAL grow fast, so keep it to hours there).
	WALRetention time.Duration `koanf:"matching_wal_retention"`
	// Shard is spot or derivatives (MATCHING_SHARD).
	Shard string `koanf:"matching_shard"`
}

// shard is what sets one engine apart from another.
type shard struct {
	name   string // the service's name
	topics application.Topics
	schema string
	lease  string
	group  string
}

var shards = map[string]shard{
	"spot": {name: "matching-engine", topics: application.SpotTopics, schema: "matching", lease: "matching-engine", group: application.Group},
	"derivatives": {
		name: "derivatives-engine", topics: application.DerivativesTopics, schema: "deriv_matching", lease: "derivatives-engine",
		group: "derivatives-engine",
	},
}

func (s *settings) Validate() error {
	var errs []error
	if s.SnapshotEvery < 1 {
		errs = append(errs, errors.New("MATCHING_SNAPSHOT_EVERY must be at least 1"))
	}
	if _, ok := shards[s.Shard]; !ok {
		errs = append(errs, errors.New("MATCHING_SHARD must be spot or derivatives"))
	}
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate())...)
}

func main() {
	// The name is needed before the settings are read: logs, metrics and
	// event producers carry it.
	name, ops := "matching-engine", ":9089"
	if os.Getenv("MATCHING_SHARD") == "derivatives" {
		name, ops = shards["derivatives"].name, ":9096"
	}
	app.Main(name, setup, app.WithDefaultOpsAddr(ops))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{Postgres: pg.DefaultConfig(), SnapshotEvery: 1000, WALRetention: 24 * time.Hour, Shard: "spot"}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	sh := shards[cfg.Shard]
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, sh.schema, migrations.Matching())
	if err != nil {
		return err
	}
	a.Logger().Info("waiting for the engine lease", "shard", cfg.Shard)
	lease, err := pg.AcquireLease(ctx, db, sh.lease)
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
	engine.Topics = sh.topics
	if err := engine.Recover(ctx); err != nil {
		return err
	}
	readRefs := func(ctx context.Context, from map[int32]int64, handle kafka.BatchHandler) (int, error) {
		return kafka.ReadToEnd(ctx, cfg.Kafka, sh.topics.References, from, 500, handle)
	}
	if err := engine.CatchUp(ctx, readRefs); err != nil {
		return err
	}
	if err := bootstrap.BatchConsumerWith(ctx, a, cfg.Kafka, kafka.BatchOptions{
		Group: sh.group, Topics: []string{sh.topics.Commands, sh.topics.References}, Handler: engine.Handle,
		MaxBatch: 500, MaxWait: 20 * time.Millisecond,
		// Back in the group after it put the engine out (a broker restart),
		// the books are read to their end again before the commands: the
		// commands published meanwhile would otherwise meet books older than
		// RefMaxAge and trade nothing with HOUSE.
		OnAssigned: func(ctx context.Context) {
			if err := engine.CatchUp(ctx, readRefs); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, "reference catch-up on rejoining failed; the consumer reads the books in order", "error", err)
			}
		},
	}); err != nil {
		return err
	}
	prod, err := bootstrap.Producer(ctx, a, cfg.Kafka)
	if err != nil {
		return err
	}
	a.Add("depth export", app.Loop(application.NewDepthExporter(engine, prod, events, a.Metrics()).Run))
	// Every ten minutes: the spot WAL takes in a few hundred reference
	// books a second, so its retention is short.
	a.Add("wal purge", app.Loop(func(ctx context.Context) error {
		ticker := time.NewTicker(10 * time.Minute)
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
