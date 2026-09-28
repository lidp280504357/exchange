// Command spot-trading-service accepts spot orders, freezes their funds
// and hands them to the matching engine (requirements §5.6, §11.1).
package main

import (
	"context"
	"errors"
	"time"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/trading/adapters/instruments"
	"github.com/lidp280504357/exchange/internal/trading/adapters/ledger"
	"github.com/lidp280504357/exchange/internal/trading/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/trading/adapters/prices"
	"github.com/lidp280504357/exchange/internal/trading/adapters/users"
	"github.com/lidp280504357/exchange/internal/trading/application"
	"github.com/lidp280504357/exchange/internal/trading/transport/httpapi"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string       `koanf:"http_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// gRPC addresses of the services orders depend on.
	LedgerAddr     string `koanf:"ledger_grpc_addr"`
	InstrumentAddr string `koanf:"instrument_grpc_addr"`
	UserAddr       string `koanf:"user_grpc_addr"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("spot-trading-service", setup, app.WithDefaultOpsAddr(":9088"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8088", Postgres: pg.DefaultConfig(),
		LedgerAddr: "localhost:9185", InstrumentAddr: "localhost:9184", UserAddr: "localhost:9182",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "trading", migrations.Trading())
	if err != nil {
		return err
	}
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	ledgerConn, err := bootstrap.GRPCClient(a, "ledger", cfg.LedgerAddr)
	if err != nil {
		return err
	}
	instrumentConn, err := bootstrap.GRPCClient(a, "instrument", cfg.InstrumentAddr)
	if err != nil {
		return err
	}
	userConn, err := bootstrap.GRPCClient(a, "user", cfg.UserAddr)
	if err != nil {
		return err
	}
	svc := &application.Service{
		Store:       postgres.NewStore(db, events),
		Ledger:      ledger.New(ledgerv1.NewLedgerServiceClient(ledgerConn)),
		Instruments: instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 5*time.Second),
		Eligibility: users.New(userv1.NewUserServiceClient(userConn)),
		Prices:      prices.None{},
		Log:         a.Logger(),
		Now:         time.Now,
	}
	a.Add("freeze recovery", app.Loop(recoverLoop(a, svc)))
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// recoverLoop finishes orders whose freeze outcome was not recorded (a
// crash or a ledger outage mid-order) every few seconds.
func recoverLoop(a *app.App, svc *application.Service) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			n, err := svc.Recover(ctx)
			if err != nil {
				a.Logger().WarnContext(ctx, "order freeze recovery failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "orders recovered", "orders", n)
			}
		}
	}
}
