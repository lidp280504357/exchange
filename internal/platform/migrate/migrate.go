// Package migrate applies a service's SQL migrations with goose when the
// service starts. Migrations live in migrations/<service>/ and run inside
// the service's schema; replicas serialize on an advisory lock.
package migrate

import (
	"context"
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

// Up applies the pending migrations found in fsys. A nil fsys, or one
// without migrations, is a no-op.
func Up(ctx context.Context, db *pg.DB, fsys fs.FS, log *slog.Logger) error {
	if fsys == nil {
		return nil
	}
	sqlDB := stdlib.OpenDBFromPool(db.Pool)
	defer sqlDB.Close()

	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(lockID(db.Schema())))
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
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
		log.Info("migration applied", "schema", db.Schema(), "version", r.Source.Version,
			"file", r.Source.Path, "took_ms", r.Duration.Milliseconds())
	}
	if err != nil {
		return fmt.Errorf("migrate %s: %w", db.Schema(), err)
	}
	return nil
}

// lockID derives a per-schema advisory lock, so services do not wait for
// each other's migrations.
func lockID(schema string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("exchange/migrate/" + schema))
	return int64(h.Sum64() & 0x7fffffffffffffff)
}
