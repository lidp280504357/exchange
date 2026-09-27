// Package bootstrap wires the shared infrastructure into a service during
// setup: each helper opens a dependency and registers its readiness check,
// metrics and cleanup with the app, so service mains stay short.
package bootstrap

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/grpcx"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/redisx"
)

// Postgres opens the pool bound to the service's schema and applies the
// service's migrations (nil means none yet).
func Postgres(ctx context.Context, a *app.App, cfg pg.Config, schema string, migrations fs.FS) (*pg.DB, error) {
	db, err := pg.Open(ctx, cfg, schema)
	if err != nil {
		return nil, err
	}
	a.Cleanup("postgres", func(context.Context) error {
		db.Close()
		return nil
	})
	if err := migrate.Up(ctx, db, migrations, a.Logger()); err != nil {
		return nil, err
	}
	a.Health().Add("postgres", db.Ping)
	db.RegisterMetrics(a.Metrics())
	return db, nil
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
