// Package migrate applies SQL migrations with goose when a service starts.
// Two sets run inside the service's schema: the platform set (outbox,
// inbox, idempotency keys; tracked in platform_db_version) and the
// service's own set from migrations/<service>/ (tracked in
// goose_db_version). Replicas serialize on advisory locks.
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/lidp280504357/exchange/internal/platform/pg"
)

//go:embed platform/*.sql
var platformFS embed.FS

const (
	serviceTable  = "goose_db_version"
	platformTable = "platform_db_version"
)

// UpPlatform applies the platform migrations.
func UpPlatform(ctx context.Context, db *pg.DB, log *slog.Logger) error {
	sub, err := fs.Sub(platformFS, "platform")
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return up(ctx, db, sub, platformTable, log)
}

// Up applies the service migrations found in fsys. A nil fsys, or one
// without migrations, is a no-op.
func Up(ctx context.Context, db *pg.DB, fsys fs.FS, log *slog.Logger) error {
	if fsys == nil {
		return nil
	}
	return up(ctx, db, fsys, serviceTable, log)
}

func up(ctx context.Context, db *pg.DB, fsys fs.FS, table string, log *slog.Logger) error {
	sqlDB := stdlib.OpenDBFromPool(db.Pool)
	defer sqlDB.Close()

	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(lockID(db.Schema() + "/" + table)))
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithTableName(table),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if errors.Is(err, goose.ErrNoMigrations) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	results, err := provider.Up(ctx)
	for _, r := range results {
		log.Info("migration applied", "schema", db.Schema(), "table", table, "version", r.Source.Version,
			"file", r.Source.Path, "took_ms", r.Duration.Milliseconds())
	}
	if err != nil {
		return fmt.Errorf("migrate %s (%s): %w", db.Schema(), table, err)
	}
	return nil
}

// UpClickHouse applies the ClickHouse migrations in fsys to db's database.
// ClickHouse has no transactions or advisory locks; only analytics-consumer
// runs these, as a single instance.
func UpClickHouse(ctx context.Context, db *sql.DB, fsys fs.FS, log *slog.Logger) error {
	provider, err := goose.NewProvider(goose.DialectClickHouse, db, fsys, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("migrate clickhouse: %w", err)
	}
	results, err := provider.Up(ctx)
	for _, r := range results {
		log.Info("migration applied", "store", "clickhouse", "version", r.Source.Version,
			"file", r.Source.Path, "took_ms", r.Duration.Milliseconds())
	}
	if err != nil {
		return fmt.Errorf("migrate clickhouse: %w", err)
	}
	return nil
}

// lockID derives an advisory lock per schema and migration set, so that
// services do not wait for each other's migrations.
func lockID(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("exchange/migrate/" + name))
	return int64(h.Sum64() & 0x7fffffffffffffff) //nolint:gosec // masked to 63 bits
}
