// Command market-maker publishes HOUSE's virtual liquidity (ADR-0015): it
// follows the reference market's public books and, for every pair and
// contract where market.house_liquidity allows it, sends the engines a
// reference book of the levels HOUSE offers and how much it may still buy
// and sell, from HOUSE's inventory (ledger) and positions (derivatives)
// and its caps (ADR-0013). It no longer places orders: the quoting market
// maker of requirements §11.10 retired with ADR-0015.
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/marketmaker/adapters/api"
	"github.com/lidp280504357/exchange/internal/marketmaker/adapters/ledger"
	"github.com/lidp280504357/exchange/internal/marketmaker/application"
	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/marketmaker/transport/consumer"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

type settings struct {
	// Postgres reaches the config schema, for the flags.
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// HouseUser is HOUSE's user ID on the trades and its contract account
	// (HOUSE_USER_ID); without one the service idles.
	HouseUser string `koanf:"house_user_id"`
	// Caps in USDT: a level (HOUSE_LEVEL_CAP), a pair's position
	// (HOUSE_SYMBOL_CAP), all spot positions (HOUSE_TOTAL_CAP), a
	// contract's position (HOUSE_CONTRACT_CAP), and the backed inventory
	// kept back (HOUSE_SAFETY); HOUSE_BACKED_ASSETS must be held to be
	// sold (ADR-0013). All contract positions together may be worth
	// HOUSE_CONTRACT_LEVERAGE times HOUSE's contract equity.
	LevelCap         string   `koanf:"house_level_cap"`
	SymbolCap        string   `koanf:"house_symbol_cap"`
	TotalCap         string   `koanf:"house_total_cap"`
	ContractCap      string   `koanf:"house_contract_cap"`
	Safety           string   `koanf:"house_safety"`
	ContractLeverage string   `koanf:"house_contract_leverage"`
	Backed           []string `koanf:"house_backed_assets"`
	// LedgerAddr is ledger-service's gRPC address (LEDGER_GRPC_ADDR);
	// INSTRUMENT_SERVICE_URL and DERIVATIVES_SERVICE_URL its REST peers.
	LedgerAddr     string `koanf:"ledger_grpc_addr"`
	InstrumentURL  string `koanf:"instrument_service_url"`
	DerivativesURL string `koanf:"derivatives_service_url"`
}

func (s *settings) Validate() error {
	var errs []error
	for name, v := range map[string]string{
		"HOUSE_LEVEL_CAP": s.LevelCap, "HOUSE_SYMBOL_CAP": s.SymbolCap, "HOUSE_TOTAL_CAP": s.TotalCap,
		"HOUSE_CONTRACT_CAP": s.ContractCap, "HOUSE_SAFETY": s.Safety, "HOUSE_CONTRACT_LEVERAGE": s.ContractLeverage,
	} {
		if d, err := decimal.NewFromString(v); err != nil || d.IsNegative() {
			errs = append(errs, errors.New(name+" must be a decimal not below zero"))
		}
	}
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate())...)
}

func main() {
	app.Main("market-maker", setup, app.WithDefaultOpsAddr(":9091"))
}

func setup(ctx context.Context, a *app.App) error {
	def := application.DefaultConfig()
	cfg := settings{
		Postgres: pg.DefaultConfig(), LevelCap: def.Caps.Level.String(), SymbolCap: def.Caps.Symbol.String(),
		TotalCap: def.Caps.Total.String(), ContractCap: def.Caps.Contract.String(), Safety: def.Caps.Safety.String(),
		ContractLeverage: def.Caps.ContractLeverage.String(), Backed: def.Backed, LedgerAddr: "localhost:9185", InstrumentURL: "http://localhost:8084",
		DerivativesURL: "http://localhost:8095",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	if cfg.HouseUser == "" {
		a.Logger().Warn("house liquidity idle: set HOUSE_USER_ID")
		return nil
	}
	flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	ledgerConn, err := bootstrap.GRPCClient(a, "ledger", cfg.LedgerAddr)
	if err != nil {
		return err
	}
	prod, err := bootstrap.Producer(ctx, a, cfg.Kafka)
	if err != nil {
		return err
	}
	conf := def
	conf.HouseUser, conf.Backed = cfg.HouseUser, cfg.Backed
	conf.Caps = domain.Caps{
		Level: decimal.RequireFromString(cfg.LevelCap), Symbol: decimal.RequireFromString(cfg.SymbolCap),
		Total: decimal.RequireFromString(cfg.TotalCap), Contract: decimal.RequireFromString(cfg.ContractCap),
		Safety: decimal.RequireFromString(cfg.Safety), ContractLeverage: decimal.RequireFromString(cfg.ContractLeverage),
	}
	a.Logger().Info("house liquidity caps", "level", conf.Caps.Level.String(), "symbol", conf.Caps.Symbol.String(),
		"total", conf.Caps.Total.String(), "contract", conf.Caps.Contract.String(), "safety", conf.Caps.Safety.String(),
		"contract_leverage", conf.Caps.ContractLeverage.String(), "backed", conf.Backed)
	client := &api.Client{
		Instrument: cfg.InstrumentURL, Derivatives: cfg.DerivativesURL, HouseUser: cfg.HouseUser, HTTP: &http.Client{Timeout: 5 * time.Second},
	}
	house := struct {
		ledger.Inventory
		*api.Client
	}{ledger.Inventory{Client: ledgerv1.NewLedgerServiceClient(ledgerConn)}, client}
	pub := application.New(conf, client, house, flagClient, prod, event.NewFactory(a.Name(), a.Config().InstanceID), a.Logger(), a.Metrics())
	// Only the latest books matter: read the public depth from its end.
	if err := bootstrap.Tail(ctx, a, cfg.Kafka, []string{event.TopicMarketDepth, event.TopicDerivMarketDepth}, consumer.Depth(pub)); err != nil {
		return err
	}
	a.Add("house liquidity", app.Loop(pub.Run))
	return nil
}
