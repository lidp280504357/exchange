// Command market-maker keeps the platform's pairs quoted around the
// reference price and its perpetual contracts around their mark price
// (requirements §11.10), trading as its own account through the order
// APIs and the ledger like any user, without fees.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lidp280504357/exchange/internal/marketmaker/adapters/api"
	"github.com/lidp280504357/exchange/internal/marketmaker/application"
	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

type settings struct {
	// Postgres reaches the config schema, for the market.maker flag.
	Postgres pg.Config `koanf:",squash"`
	// UserID is the market maker's account (MARKET_MAKER_USER_ID); without
	// one the service idles.
	UserID string `koanf:"market_maker_user_id"`
	// Symbols are the pairs to quote (MARKET_MAKER_SYMBOLS) and Contracts
	// the perpetual contracts (MARKET_MAKER_CONTRACTS), with the defaults
	// of §11.10 unless ParamsFile (MARKET_MAKER_PARAMS_FILE, a JSON list
	// of domain.Params) says otherwise.
	Symbols    []string `koanf:"market_maker_symbols"`
	Contracts  []string `koanf:"market_maker_contracts"`
	ParamsFile string   `koanf:"market_maker_params_file"`
	// Internal REST addresses (TRADING_SERVICE_URL, LEDGER_SERVICE_URL,
	// MARKET_DATA_SERVICE_URL, INSTRUMENT_SERVICE_URL,
	// DERIVATIVES_SERVICE_URL).
	TradingURL     string `koanf:"trading_service_url"`
	LedgerURL      string `koanf:"ledger_service_url"`
	MarketURL      string `koanf:"market_data_service_url"`
	InstrumentURL  string `koanf:"instrument_service_url"`
	DerivativesURL string `koanf:"derivatives_service_url"`
}

func (s *settings) Validate() error { return s.Postgres.Validate() }

func main() {
	app.Main("market-maker", setup, app.WithDefaultOpsAddr(":9091"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		Postgres: pg.DefaultConfig(), TradingURL: "http://localhost:8088", LedgerURL: "http://localhost:8085",
		MarketURL: "http://localhost:8090", InstrumentURL: "http://localhost:8084", DerivativesURL: "http://localhost:8095",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	pairs, err := loadParams(cfg.Symbols, cfg.ParamsFile, domain.Defaults)
	if err != nil {
		return err
	}
	contracts, err := loadParams(cfg.Contracts, cfg.ParamsFile, domain.ContractDefaults)
	if err != nil {
		return err
	}
	flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	if cfg.UserID == "" || len(pairs)+len(contracts) == 0 {
		a.Logger().Warn("market maker idle: set MARKET_MAKER_USER_ID and MARKET_MAKER_SYMBOLS or MARKET_MAKER_CONTRACTS")
		return nil
	}
	client := &api.Client{
		Trading: cfg.TradingURL, Ledger: cfg.LedgerURL, Market: cfg.MarketURL, Instrument: cfg.InstrumentURL,
		UserID: cfg.UserID, HTTP: &http.Client{Timeout: 5 * time.Second},
	}
	perp := &api.Contracts{Client: client, Derivatives: cfg.DerivativesURL}
	maker := application.New(pairs, application.Spot{Orders: client, Balances: client, Refs: client, Pairs: client},
		contracts, application.Contracts{Orders: perp, Positions: perp, Marks: perp, Specs: perp}, flagClient, a.Logger(), a.Metrics())
	a.Add("market maker", app.Loop(maker.Run))
	return nil
}

// loadParams returns the defaults for symbols, overridden by the file.
func loadParams(symbols []string, file string, defaults func(string) domain.Params) ([]domain.Params, error) {
	byName := map[string]domain.Params{}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var list []domain.Params
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		for _, p := range list {
			byName[p.Symbol] = p
		}
	}
	out := make([]domain.Params, 0, len(symbols))
	for _, s := range symbols {
		p, ok := byName[s]
		if !ok {
			p = defaults(s)
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if len(out) != len(symbols) {
		return nil, errors.New("market maker: duplicate symbols")
	}
	return out, nil
}
