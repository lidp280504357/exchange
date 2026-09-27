// Package chx connects to ClickHouse, the analytics store (requirements
// §9). ClickHouse is eventually consistent and never on the write path of
// balances or orders.
package chx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Config holds the connection settings.
type Config struct {
	// Addr is host:port of the native protocol (CLICKHOUSE_ADDR).
	Addr string `koanf:"clickhouse_addr"`
	// Database (CLICKHOUSE_DB).
	Database string `koanf:"clickhouse_db"`
	// User (CLICKHOUSE_USER).
	User string `koanf:"clickhouse_user"`
	// Password (CLICKHOUSE_PASSWORD).
	Password string `koanf:"clickhouse_password"`
}

// Validate reports missing settings.
func (c *Config) Validate() error {
	var errs []error
	if c.Addr == "" {
		errs = append(errs, errors.New("CLICKHOUSE_ADDR is required"))
	}
	if c.Database == "" {
		errs = append(errs, errors.New("CLICKHOUSE_DB is required"))
	}
	return errors.Join(errs...)
}

func (c *Config) options() *clickhouse.Options {
	return &clickhouse.Options{
		Addr:        []string{c.Addr},
		Auth:        clickhouse.Auth{Database: c.Database, Username: c.User, Password: c.Password},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second,
		Compression: &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
	}
}

// Open connects over the native protocol and pings.
func Open(ctx context.Context, cfg Config) (driver.Conn, error) {
	conn, err := clickhouse.Open(cfg.options())
	if err != nil {
		return nil, fmt.Errorf("clickhouse: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse: ping %s: %w", cfg.Addr, err)
	}
	return conn, nil
}

// OpenDB returns a database/sql handle, which goose needs for migrations.
func OpenDB(cfg Config) *sql.DB {
	return clickhouse.OpenDB(cfg.options())
}
