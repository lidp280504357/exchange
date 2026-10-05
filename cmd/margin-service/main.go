// Command margin-service runs margin trading (design 2026-10-06): margin
// accounts, transfers between them and SPOT, borrowing from HOUSE,
// repaying, the hourly interest and the accounts' margin levels, with the
// ledger keeping every balance and debt (ADR-0001); batch E2 adds orders
// on margin accounts and E3 warnings and liquidations.
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/margin/adapters/instruments"
	"github.com/skill/exchange/internal/margin/adapters/ledger"
	"github.com/skill/exchange/internal/margin/adapters/postgres"
	"github.com/skill/exchange/internal/margin/adapters/prices"
	"github.com/skill/exchange/internal/margin/adapters/users"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls for
	// /v1/margin/* (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// GRPCAddr serves spot-trading-service's checks of orders on margin
	// accounts from batch E2 (GRPC_ADDR).
	GRPCAddr string       `koanf:"grpc_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// gRPC addresses of the services margin trading depends on.
	LedgerAddr     string `koanf:"ledger_grpc_addr"`
	InstrumentAddr string `koanf:"instrument_grpc_addr"`
	UserAddr       string `koanf:"user_grpc_addr"`
	// MarketURL is market-data-service, whose tickers value the accounts
	// (MARKET_DATA_SERVICE_URL).
	MarketURL string `koanf:"market_data_service_url"`
	// ReconcileInterval is how often invariant 7 is checked
	// (RECONCILE_INTERVAL).
	ReconcileInterval time.Duration `koanf:"reconcile_interval"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("margin-service", setup, app.WithDefaultOpsAddr(":9099"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8099", GRPCAddr: ":9199", Postgres: pg.DefaultConfig(),
		LedgerAddr: "localhost:9185", InstrumentAddr: "localhost:9184", UserAddr: "localhost:9182",
		MarketURL: "http://localhost:8090", ReconcileInterval: time.Hour,
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "margin", migrations.Margin())
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
	features, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	book := &prices.Poller{
		Base: cfg.MarketURL, Client: &http.Client{Timeout: 5 * time.Second}, Log: a.Logger(), Now: time.Now,
	}
	a.Add("prices", app.Loop(book.Run))
	svc := &application.Service{
		Store:       postgres.NewStore(db, events),
		Ledger:      ledger.New(ledgerv1.NewLedgerServiceClient(ledgerConn)),
		Prices:      book,
		Instruments: instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 5*time.Second),
		Eligibility: users.New(userv1.NewUserServiceClient(userConn)),
		Features:    features,
		Log:         a.Logger(),
		Now:         time.Now,
		Metrics:     application.NewMetrics(a.Metrics()),
	}
	a.Add("recovery", app.Loop(every(a, 5*time.Second, "recovery", svc.Recover)))
	a.Add("interest", app.Loop(interestLoop(a, db, svc)))
	a.Add("reconcile", app.Loop(reconcileLoop(a, svc, cfg.ReconcileInterval)))

	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// every runs step every interval, logging what it did and its failures.
func every(a *app.App, interval time.Duration, name string, step func(context.Context) (int, error)) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			if n, err := step(ctx); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, name+" failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, name, "count", n)
			}
		}
	}
}

// interestLoop charges the hourly interest while it holds the lease
// margin-interest (one charging process at a time, design §4.3), every
// 15 seconds: an hour is charged shortly after it starts, and retried
// until every loan of it is charged.
func interestLoop(a *app.App, db *pg.DB, svc *application.Service) func(context.Context) error {
	return func(ctx context.Context) error {
		lease, err := pg.AcquireLease(ctx, db, "margin-interest")
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		defer func() { _ = lease.Release(context.WithoutCancel(ctx)) }()
		held, cancel := context.WithCancel(ctx)
		defer cancel()
		lost := make(chan error, 1)
		go func() {
			lost <- lease.Hold(held, 5*time.Second)
			cancel()
		}()
		err = every(a, 15*time.Second, "interest charged", svc.ChargeInterest)(held)
		if ctx.Err() == nil {
			if err := <-lost; err != nil {
				return err // the lease is lost: stop, the app restarts the process
			}
		}
		return err
	}
}

func reconcileLoop(a *app.App, svc *application.Service, interval time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		wait := time.Minute // the first run a minute after the start
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
			}
			wait = interval
			results, err := svc.Reconcile(ctx)
			if err != nil {
				a.Logger().WarnContext(ctx, "margin reconciliation failed", "error", err)
				continue
			}
			for _, r := range results {
				if len(r.Mismatches) > 0 {
					a.Logger().ErrorContext(ctx, "margin invariant broken", "check", r.Check, "mismatches", len(r.Mismatches),
						"first", r.Mismatches[0].Detail)
				}
			}
		}
	}
}
