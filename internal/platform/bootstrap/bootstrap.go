// Package bootstrap wires the shared infrastructure into a service during
// setup: each helper opens a dependency and registers its readiness check,
// metrics, background work and cleanup with the app, so service mains stay
// short.
package bootstrap

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/chx"
	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/grpcx"
	"github.com/lidp280504357/exchange/internal/platform/idempotency"
	"github.com/lidp280504357/exchange/internal/platform/inbox"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/redisx"
	"github.com/lidp280504357/exchange/migrations"
)

// Retention of the platform tables in every service schema.
const (
	outboxRetention = 7 * 24 * time.Hour  // published events, for reconciliation
	inboxRetention  = 35 * 24 * time.Hour // longer than the 30-day topic retention
	janitorInterval = time.Hour
)

// Postgres opens the pool bound to the service's schema, applies the
// platform and service migrations (nil means none yet) and starts the
// janitor that purges old outbox, inbox and idempotency rows.
func Postgres(ctx context.Context, a *app.App, cfg pg.Config, schema string, migrations fs.FS) (*pg.DB, error) {
	db, err := pg.Open(ctx, cfg, schema)
	if err != nil {
		return nil, err
	}
	a.Cleanup("postgres", func(context.Context) error {
		db.Close()
		return nil
	})
	if err := migrate.UpPlatform(ctx, db, a.Logger()); err != nil {
		return nil, err
	}
	if err := migrate.Up(ctx, db, migrations, a.Logger()); err != nil {
		return nil, err
	}
	a.Health().Add("postgres", db.Ping)
	db.RegisterMetrics(a.Metrics())
	a.Add("janitor", app.Loop(func(ctx context.Context) error { return janitor(ctx, a, db) }))
	return db, nil
}

func janitor(ctx context.Context, a *app.App, db *pg.DB) error {
	for {
		now := time.Now()
		purged := map[string]func() (int64, error){
			"outbox":           func() (int64, error) { return outbox.Purge(ctx, db, now.Add(-outboxRetention)) },
			"inbox":            func() (int64, error) { return inbox.Purge(ctx, db, now.Add(-inboxRetention)) },
			"idempotency_keys": func() (int64, error) { return idempotency.Purge(ctx, db, now.Add(-idempotency.TTL)) },
		}
		for table, purge := range purged {
			n, err := purge()
			switch {
			case err != nil && ctx.Err() == nil:
				a.Logger().WarnContext(ctx, "purge failed", "table", table, "error", err)
			case n > 0:
				a.Logger().InfoContext(ctx, "purged old rows", "table", table, "rows", n)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(janitorInterval):
		}
	}
}

// Flags opens the shared config schema, applies its migrations and keeps a
// local copy of the feature flags that refreshes every 5 seconds.
func Flags(ctx context.Context, a *app.App, cfg pg.Config) (*flags.Client, error) {
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.DSN, MaxConns: 2}, "config")
	if err != nil {
		return nil, err
	}
	a.Cleanup("config db", func(context.Context) error {
		db.Close()
		return nil
	})
	if err := migrate.Up(ctx, db, migrations.Config(), a.Logger()); err != nil {
		return nil, err
	}
	client := flags.NewClient(db, a.Logger(), a.Metrics())
	if err := client.Refresh(ctx); err != nil {
		return nil, err
	}
	a.Add("flags", app.Loop(client.Run))
	return client, nil
}

// Redis opens the client.
func Redis(ctx context.Context, a *app.App, cfg redisx.Config) (*redis.Client, error) {
	client, err := redisx.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	a.Cleanup("redis", func(context.Context) error { return client.Close() })
	a.Health().Add("redis", redisx.Ping(client))
	redisx.RegisterMetrics(a.Metrics(), client)
	return client, nil
}

// Events connects the producer and starts the outbox relay for db. It
// returns the factory the service uses to build envelopes; add them with
// outbox.Add inside the business transaction.
func Events(ctx context.Context, a *app.App, db *pg.DB, cfg kafka.Config) (*event.Factory, error) {
	prod, err := kafka.NewProducer(ctx, cfg, a.Name())
	if err != nil {
		return nil, err
	}
	a.Cleanup("kafka producer", func(context.Context) error {
		prod.Close()
		return nil
	})
	a.Health().Add("kafka", prod.Ping)
	relay := outbox.NewRelay(db, prod, a.Logger(), a.Metrics())
	a.Add("outbox relay", app.Loop(relay.Run))
	return event.NewFactory(a.Name(), a.Config().InstanceID), nil
}

var (
	consumerMetricsMu sync.Mutex
	consumerMetrics   = map[prometheus.Registerer]*kafka.ConsumerMetrics{}
)

// sharedConsumerMetrics returns the consumer metrics of a's registry,
// registering them on first use.
func sharedConsumerMetrics(a *app.App) *kafka.ConsumerMetrics {
	consumerMetricsMu.Lock()
	defer consumerMetricsMu.Unlock()
	m, ok := consumerMetrics[a.Metrics()]
	if !ok {
		m = kafka.NewConsumerMetrics(a.Metrics())
		consumerMetrics[a.Metrics()] = m
	}
	return m
}

// Consumer joins group on topics and handles their events with h, with
// retry and dead-letter topics. Call it once per group.
func Consumer(ctx context.Context, a *app.App, cfg kafka.Config, group string, topics []string, h kafka.Handler) error {
	c, err := kafka.NewConsumer(ctx, cfg, kafka.ConsumerOptions{
		Group:   group,
		Topics:  topics,
		Handler: h,
		Logger:  a.Logger(),
		Metrics: sharedConsumerMetrics(a),
	})
	if err != nil {
		return err
	}
	a.Add("consumer "+group, c)
	return nil
}

// Tail follows topics from their end without a consumer group and hands
// every record to h (fan-out to live connections).
func Tail(ctx context.Context, a *app.App, cfg kafka.Config, topics []string, h kafka.Handler) error {
	t, err := kafka.NewTail(ctx, cfg, a.Name()+"-tail", topics, h, a.Logger())
	if err != nil {
		return err
	}
	a.Add("kafka tail", t)
	return nil
}

// BatchConsumer joins group on topics and hands batches to h.
func BatchConsumer(ctx context.Context, a *app.App, cfg kafka.Config, group string, topics []string, h kafka.BatchHandler) error {
	c, err := kafka.NewBatchConsumer(ctx, cfg, kafka.BatchOptions{
		Group:   group,
		Topics:  topics,
		Handler: h,
		Logger:  a.Logger(),
		Metrics: sharedConsumerMetrics(a),
	})
	if err != nil {
		return err
	}
	a.Add("batch consumer "+group, c)
	return nil
}

// ClickHouse connects and applies the ClickHouse migrations in fsys.
func ClickHouse(ctx context.Context, a *app.App, cfg chx.Config, migrations fs.FS) (driver.Conn, error) {
	conn, err := chx.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	a.Cleanup("clickhouse", func(context.Context) error { return conn.Close() })
	db := chx.OpenDB(cfg)
	defer db.Close()
	if err := migrate.UpClickHouse(ctx, db, migrations, a.Logger()); err != nil {
		return nil, err
	}
	a.Health().Add("clickhouse", conn.Ping)
	return conn, nil
}

// GRPCServer binds the service's gRPC server and adds it as a component.
// Register the service implementations on it before setup returns.
func GRPCServer(ctx context.Context, a *app.App, addr string) (*grpcx.Server, error) {
	srv, err := grpcx.NewServer(ctx, addr, grpcx.ServerOptions{
		Logger:     a.Logger().With("server", "grpc"),
		Metrics:    grpcx.NewMetrics(a.Metrics()),
		Reflection: a.Config().Env != config.EnvProd,
	})
	if err != nil {
		return nil, err
	}
	a.Add("grpc", srv)
	return srv, nil
}

// GRPCClient connects to another service; the connection closes on shutdown.
func GRPCClient(a *app.App, name, target string) (*grpc.ClientConn, error) {
	conn, err := grpcx.Dial(target)
	if err != nil {
		return nil, fmt.Errorf("%s client: %w", name, err)
	}
	a.Cleanup(name+" client", func(context.Context) error { return conn.Close() })
	return conn, nil
}

// HTTPServer binds the service's REST server and adds it as a component.
func HTTPServer(ctx context.Context, a *app.App, addr string, h http.Handler) error {
	srv, err := app.NewHTTPServer(ctx, addr, h, a.Logger().With("server", "http"))
	if err != nil {
		return err
	}
	a.Add("http", srv)
	return nil
}
