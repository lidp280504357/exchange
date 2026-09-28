// Package pg opens a service's PostgreSQL pool and runs its transactions.
//
// Every service owns one schema (ADR-0006) and connects with search_path set
// to it, so SQL uses unqualified table names and cannot reach another
// service's tables by accident.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Config holds the connection settings shared by all services.
type Config struct {
	// DSN is the connection string (POSTGRES_DSN).
	DSN string `koanf:"postgres_dsn"`
	// MaxConns caps the pool (POSTGRES_MAX_CONNS).
	MaxConns int32 `koanf:"postgres_max_conns"`
}

// DefaultConfig keeps each service's pool small: eight services share one
// database with the default max_connections of 100.
func DefaultConfig() Config {
	return Config{MaxConns: 5}
}

// Validate reports missing settings.
func (c *Config) Validate() error {
	if c.DSN == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	if c.MaxConns <= 0 {
		return fmt.Errorf("POSTGRES_MAX_CONNS must be positive, got %d", c.MaxConns)
	}
	return nil
}

// Querier is satisfied by both the pool and a transaction, so repositories
// work inside and outside InTx.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, src pgx.CopyFromSource) (int64, error)
}

// DB is a connection pool bound to one schema.
type DB struct {
	*pgxpool.Pool
	schema string
}

// Open connects with search_path set to schema, creates the schema when it
// does not exist and checks the connection.
func Open(ctx context.Context, cfg Config, schema string) (*DB, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("pg: parse POSTGRES_DSN: %w", err)
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	pcfg.ConnConfig.RuntimeParams["search_path"] = schema
	pcfg.ConnConfig.RuntimeParams["application_name"] = schema
	// NUMERIC scans into and binds from shopspring/decimal (ADR-0008).
	pcfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("pg: %w", err)
	}
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg: create schema %s: %w", schema, err)
	}
	return &DB{Pool: pool, schema: schema}, nil
}

// Schema returns the schema the pool is bound to.
func (db *DB) Schema() string { return db.schema }

// maxTxAttempts bounds retries of serialization failures and deadlocks.
const maxTxAttempts = 3

// InTx runs fn in a read-committed transaction; see InTxOpts.
func (db *DB) InTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.InTxOpts(ctx, pgx.TxOptions{}, fn)
}

// InTxOpts runs fn in a transaction, commits when fn returns nil and rolls
// back otherwise. Serialization failures and deadlocks are retried, so fn
// must have no side effects outside the transaction.
func (db *DB) InTxOpts(ctx context.Context, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	for attempt := 1; ; attempt++ {
		err := pgx.BeginTxFunc(ctx, db.Pool, opts, fn)
		if err == nil || !retryable(err) || attempt == maxTxAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt*attempt) * 10 * time.Millisecond):
		}
	}
}

func retryable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

// IsNoRows reports whether a query found nothing.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// UniqueViolation returns the violated constraint when err is a unique
// violation.
func UniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

// RegisterMetrics exports the pool statistics, labeled with the schema.
func (db *DB) RegisterMetrics(reg prometheus.Registerer) {
	labels := prometheus.Labels{"schema": db.schema}
	gauge := func(name, help string, f func(*pgxpool.Stat) float64) prometheus.Collector {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help, ConstLabels: labels},
			func() float64 { return f(db.Stat()) })
	}
	counter := func(name, help string, f func(*pgxpool.Stat) float64) prometheus.Collector {
		return prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help, ConstLabels: labels},
			func() float64 { return f(db.Stat()) })
	}
	reg.MustRegister(
		gauge("pg_pool_acquired_conns", "Connections in use.", func(s *pgxpool.Stat) float64 { return float64(s.AcquiredConns()) }),
		gauge("pg_pool_idle_conns", "Idle connections.", func(s *pgxpool.Stat) float64 { return float64(s.IdleConns()) }),
		gauge("pg_pool_total_conns", "Open connections.", func(s *pgxpool.Stat) float64 { return float64(s.TotalConns()) }),
		gauge("pg_pool_max_conns", "Pool size limit.", func(s *pgxpool.Stat) float64 { return float64(s.MaxConns()) }),
		counter("pg_pool_acquires_total", "Connections acquired from the pool.", func(s *pgxpool.Stat) float64 { return float64(s.AcquireCount()) }),
		counter("pg_pool_waited_acquires_total", "Acquires that had to wait for a free connection.", func(s *pgxpool.Stat) float64 { return float64(s.EmptyAcquireCount()) }),
		counter("pg_pool_acquire_wait_seconds_total", "Time spent waiting for connections.", func(s *pgxpool.Stat) float64 { return s.AcquireDuration().Seconds() }),
	)
}
