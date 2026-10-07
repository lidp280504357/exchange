// Command spot-trading-service accepts spot orders, freezes their funds
// and hands them to the matching engine (requirements §5.6, §11.1).
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/trading/adapters/instruments"
	"github.com/skill/exchange/internal/trading/adapters/ledger"
	"github.com/skill/exchange/internal/trading/adapters/margin"
	"github.com/skill/exchange/internal/trading/adapters/postgres"
	"github.com/skill/exchange/internal/trading/adapters/prices"
	"github.com/skill/exchange/internal/trading/adapters/users"
	"github.com/skill/exchange/internal/trading/application"
	"github.com/skill/exchange/internal/trading/transport/consumer"
	"github.com/skill/exchange/internal/trading/transport/httpapi"
	"github.com/skill/exchange/migrations"
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
	// MarginAddr is margin-service, which checks and funds orders on
	// margin accounts; it is only called while margin.enabled is on.
	MarginAddr string `koanf:"margin_grpc_addr"`
	// MarketURL is market-data-service, for reference prices
	// (MARKET_DATA_SERVICE_URL).
	MarketURL string `koanf:"market_data_service_url"`
	// MarketMakerUsers are the accounts whose orders pay no fees
	// (MARKET_MAKER_USER_IDS): the simulated market's bots (ASTRA design
	// §4). HOUSE needs no entry: its side of a trade has no order.
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
		MarginAddr: "localhost:9199", MarketURL: "http://localhost:8090",
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
	marginConn, err := bootstrap.GRPCClient(a, "margin", cfg.MarginAddr)
	if err != nil {
		return err
	}
	// Flags decide whether a pair's orders trade only with HOUSE (ADR-0015).
	features, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	store := postgres.NewStore(db, events)
	a.Logger().Info("fee-free accounts", "users", cfg.MarketMakerUsers)
	svc := &application.Service{
		Store:       store,
		Ledger:      ledger.New(ledgerv1.NewLedgerServiceClient(ledgerConn)),
		Margin:      margin.New(marginv1.NewMarginServiceClient(marginConn)),
		Instruments: instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 5*time.Second),
		Eligibility: users.New(userv1.NewUserServiceClient(userConn)),
		// The latest trade anchors price bands and market protection.
		Prices: prices.NewLastTrade(store.Read().Fills().LastTrade,
			prices.ReferenceClient{Base: cfg.MarketURL, Client: &http.Client{Timeout: 2 * time.Second}}.Price, time.Second),
		Features: features,
		Products: features,
		FeeFree:  cfg.MarketMakerUsers,
		Log:      a.Logger(),
		Now:      time.Now,
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
// released (a crash or a ledger or margin-service outage midway), and
// reports how many orders still wait and for how long the oldest has: a
// recovery pass takes the 100 oldest, so orders stuck at the front would
// starve the rest (alert TradingOrdersPendingFreeze).
func recoverLoop(a *app.App, svc *application.Service) func(context.Context) error {
	pending := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "trading_orders_pending_freeze",
		Help: "Orders whose freeze outcome is not recorded yet, the ones placed in the last 10 seconds (in flight, not yet recovery's) included.",
	})
	oldest := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "trading_orders_pending_freeze_oldest_seconds", Help: "How long the oldest of them has waited; 0 without any.",
	})
	a.Metrics().MustRegister(pending, oldest)
	return func(ctx context.Context) error {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			// Counted first and on its own deadline: the gauges keep moving
			// (and the alert keeps its say) while a dependency hangs.
			count, cancel := context.WithTimeout(ctx, 5*time.Second)
			if n, age, err := svc.Pending(count); err != nil {
				a.Logger().WarnContext(ctx, "counting pending orders failed", "error", err)
			} else {
				pending.Set(float64(n))
				oldest.Set(age.Seconds())
			}
			cancel()
			// Each pass ends within 30 seconds; every call in it within the
			// service's call timeout.
			pass, cancel := context.WithTimeout(ctx, 30*time.Second)
			if n, err := svc.Recover(pass); err != nil {
				a.Logger().WarnContext(ctx, "order freeze recovery failed", "error", err, "orders_finished", n)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "orders recovered", "orders", n)
			}
			cancel()
			// Releases have their own 30 seconds: orders stuck at the front of
			// the freezes (margin-service hanging) do not starve them.
			pass, cancel = context.WithTimeout(ctx, 30*time.Second)
			if n, err := svc.RecoverReleases(pass); err != nil {
				a.Logger().WarnContext(ctx, "order release recovery failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "order releases recovered", "orders", n)
			}
			cancel()
			// While spot trading is closed: the orders that slipped in as it
			// closed (design 2026-10-07, product switches §1 #3).
			pass, cancel = context.WithTimeout(ctx, 30*time.Second)
			if n, err := svc.SweepClosed(pass); err != nil {
				a.Logger().WarnContext(ctx, "closed spot sweep failed", "error", err, "orders_canceled", n)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "orders canceled: spot trading is closed", "orders", n)
			}
			cancel()
		}
	}
}
