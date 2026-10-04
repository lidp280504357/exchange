// Command market-sim runs the simulated market of the platform coin ASTRA
// (ASTRA design §4, §5.1): a pool of bot accounts trades ASTRA-USDT
// around a target price that follows BTC's and ETH's returns with a
// deviation of its own. The bots are ordinary users trading through the
// platform's paths (the trading service, the ledger), so the coin's market
// data is what they print. One instance holds the lease (market-sim);
// another waits as a standby. sim.enabled switches the bots on.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/skill/exchange/internal/marketsim/adapters/api"
	"github.com/skill/exchange/internal/marketsim/adapters/instruments"
	"github.com/skill/exchange/internal/marketsim/adapters/postgres"
	"github.com/skill/exchange/internal/marketsim/application"
	"github.com/skill/exchange/internal/marketsim/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/svcsign"
	"github.com/skill/exchange/migrations"
)

type settings struct {
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// HTTPAddr is the internal management API (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// Symbol is the pair the bots trade (SIM_SYMBOL), Quote its quote
	// asset (SIM_QUOTE); Seed seeds a first start (SIM_SEED, 0: the clock).
	Symbol string `koanf:"sim_symbol"`
	Quote  string `koanf:"sim_quote"`
	Seed   uint64 `koanf:"sim_seed"`
	// Perp is the coin's perpetual (SIM_PERP_SYMBOL; empty: none), traded
	// through derivatives-service (DERIVATIVES_SERVICE_URL).
	Perp           string `koanf:"sim_perp_symbol"`
	DerivativesURL string `koanf:"derivatives_service_url"`
	// The platform's REST peers, and instrument-service's gRPC address
	// (INSTRUMENT_GRPC_ADDR) for the halts.
	TradingURL     string `koanf:"trading_service_url"`
	LedgerURL      string `koanf:"ledger_service_url"`
	MarketURL      string `koanf:"market_data_service_url"`
	InstrumentURL  string `koanf:"instrument_service_url"`
	InstrumentAddr string `koanf:"instrument_grpc_addr"`
	// The management API's changes are signed (ASTRA design §6.2), with
	// one key per caller, at least 32 characters each: APISecret
	// (SIM_API_SECRET, key ID "ops") is exchangectl's in this container,
	// AdminAPISecret (SIM_ADMIN_API_SECRET, key ID "admin") the admin
	// console's service's, the only caller that may name an approver.
	APISecret      string `koanf:"sim_api_secret"`
	AdminAPISecret string `koanf:"sim_admin_api_secret"`
}

func (s *settings) Validate() error {
	var errs []error
	if s.Symbol == "" || s.Quote == "" {
		errs = append(errs, errors.New("SIM_SYMBOL and SIM_QUOTE are required"))
	}
	if err := svcsign.CheckSecret(s.APISecret); err != nil {
		errs = append(errs, fmt.Errorf("SIM_API_SECRET: %w", err))
	}
	if err := svcsign.CheckSecret(s.AdminAPISecret); err != nil {
		errs = append(errs, fmt.Errorf("SIM_ADMIN_API_SECRET: %w", err))
	}
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate())...)
}

func main() {
	app.Main("market-sim", setup, app.WithDefaultOpsAddr(":9098"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		Postgres: pg.DefaultConfig(), HTTPAddr: ":8098", Symbol: "ASTRA-USDT", Quote: "USDT",
		TradingURL: "http://localhost:8088", LedgerURL: "http://localhost:8085", MarketURL: "http://localhost:8090",
		InstrumentURL: "http://localhost:8084", InstrumentAddr: "localhost:9184",
		Perp: "ASTRA-USDT-PERP", DerivativesURL: "http://localhost:8095",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "marketsim", migrations.MarketSim())
	if err != nil {
		return err
	}
	flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	// Audit records of the operators' actions go out through the outbox.
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	instrumentConn, err := bootstrap.GRPCClient(a, "instrument", cfg.InstrumentAddr)
	if err != nil {
		return err
	}
	client := &api.Client{
		TradingURL: cfg.TradingURL, LedgerURL: cfg.LedgerURL, MarketURL: cfg.MarketURL, InstrumentURL: cfg.InstrumentURL,
		DerivativesURL: cfg.DerivativesURL, HTTP: &http.Client{Timeout: 2 * time.Second},
	}
	sim := application.New(application.Config{Symbol: cfg.Symbol, Quote: cfg.Quote, Perp: cfg.Perp, Tick: 250 * time.Millisecond, Seed: cfg.Seed},
		client, client, instruments.Client{API: instrumentv1.NewInstrumentServiceClient(instrumentConn)}, postgres.NewStore(db, events),
		flagClient, a.Logger(), a.Metrics())
	sim.Derivatives = client
	r := a.NewRouter()
	signed := &svcsign.Verifier{Keys: map[string][]byte{
		httpapi.KeyOps: []byte(cfg.APISecret), httpapi.KeyAdmin: []byte(cfg.AdminAPISecret),
	}}
	(&httpapi.Handler{Sim: sim, Signed: signed}).Routes(r)
	if err := bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r); err != nil {
		return err
	}

	// The standby waits for the lease before it trades.
	a.Logger().Info("waiting for the market-sim lease")
	lease, err := pg.AcquireLease(ctx, db, "market-sim")
	if err != nil {
		return err
	}
	a.Cleanup("market-sim lease", lease.Release)
	a.Add("market-sim lease", app.Loop(func(ctx context.Context) error { return lease.Hold(ctx, 5*time.Second) }))
	if err := sim.Start(ctx); err != nil {
		return err
	}
	a.Add("simulated market", app.Loop(sim.Run))
	return nil
}
