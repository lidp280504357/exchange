// Command spot-trading-service accepts spot orders, freezes their funds
// and hands them to the matching engine (requirements §5.6, §11.1).
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/trading/adapters/instruments"
	"github.com/lidp280504357/exchange/internal/trading/adapters/ledger"
	"github.com/lidp280504357/exchange/internal/trading/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/trading/adapters/prices"
	"github.com/lidp280504357/exchange/internal/trading/adapters/users"
	"github.com/lidp280504357/exchange/internal/trading/application"
	"github.com/lidp280504357/exchange/internal/trading/transport/consumer"
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
	// MarketURL is market-data-service, for reference prices
	// (MARKET_DATA_SERVICE_URL).
	MarketURL string `koanf:"market_data_service_url"`
	// MarketMakerUsers trade without fees (MARKET_MAKER_USER_IDS, §11.10).
	MarketMakerUsers []string `koanf:"market_maker_user_ids"`
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
		MarketURL: "http://localhost:8090",
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
	store := postgres.NewStore(db, events)
	svc := &application.Service{
		Store:       store,
		Ledger:      ledger.New(ledgerv1.NewLedgerServiceClient(ledgerConn)),
		Instruments: instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 5*time.Second),
		Eligibility: users.New(userv1.NewUserServiceClient(userConn)),
		// The latest trade anchors price bands and market protection.
		Prices: prices.NewLastTrade(store.Read().Fills().LastTrade,
			prices.ReferenceClient{Base: cfg.MarketURL, Client: &http.Client{Timeout: 2 * time.Second}}.Price, time.Second),
		FeeFree: cfg.MarketMakerUsers,
		Log:     a.Logger(),
		Now:     time.Now,
	}
	// The engine's updates and fills.
	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, application.Consumer, []string{event.TopicOrder, event.TopicTrade}, consumer.Handler(svc)); err != nil {
		return err
	}
	a.Add("recovery", app.Loop(recoverLoop(a, svc)))
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// recoverLoop finishes, every few seconds, orders whose freeze outcome
// was not recorded and finished orders whose unused funds were not
// released (a crash or a ledger outage midway).
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
			if n, err := svc.Recover(ctx); err != nil {
				a.Logger().WarnContext(ctx, "order freeze recovery failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "orders recovered", "orders", n)
			}
			if n, err := svc.RecoverReleases(ctx); err != nil {
				a.Logger().WarnContext(ctx, "order release recovery failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "order releases recovered", "orders", n)
			}
		}
	}
}
