// Command market-maker keeps the platform's pairs quoted around the
// reference price (requirements §11.10), trading as its own account through
// the order API and the ledger like any user, without fees.
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
	// Symbols are the pairs to quote (MARKET_MAKER_SYMBOLS), with the
	// defaults of §11.10 unless ParamsFile (MARKET_MAKER_PARAMS_FILE, a
	// JSON list of domain.Params) says otherwise.
	Symbols    []string `koanf:"market_maker_symbols"`
	ParamsFile string   `koanf:"market_maker_params_file"`
	// Internal REST addresses (TRADING_SERVICE_URL, LEDGER_SERVICE_URL,
	// MARKET_DATA_SERVICE_URL, INSTRUMENT_SERVICE_URL).
	TradingURL    string `koanf:"trading_service_url"`
	LedgerURL     string `koanf:"ledger_service_url"`
	MarketURL     string `koanf:"market_data_service_url"`
	InstrumentURL string `koanf:"instrument_service_url"`
}

func (s *settings) Validate() error { return s.Postgres.Validate() }

func main() {
	app.Main("market-maker", setup, app.WithDefaultOpsAddr(":9091"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		Postgres: pg.DefaultConfig(), TradingURL: "http://localhost:8088", LedgerURL: "http://localhost:8085",
		MarketURL: "http://localhost:8090", InstrumentURL: "http://localhost:8084",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	params, err := loadParams(cfg.Symbols, cfg.ParamsFile)
	if err != nil {
		return err
	}
	flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	if cfg.UserID == "" || len(params) == 0 {
		a.Logger().Warn("market maker idle: set MARKET_MAKER_USER_ID and MARKET_MAKER_SYMBOLS")
		return nil
	}
	client := &api.Client{
		Trading: cfg.TradingURL, Ledger: cfg.LedgerURL, Market: cfg.MarketURL, Instrument: cfg.InstrumentURL,
		UserID: cfg.UserID, HTTP: &http.Client{Timeout: 5 * time.Second},
	}
	maker := application.New(params, client, client, client, client, flagClient, a.Logger(), a.Metrics())
	a.Add("market maker", app.Loop(maker.Run))
	return nil
}

// loadParams returns the defaults for symbols, overridden by the file.
func loadParams(symbols []string, file string) ([]domain.Params, error) {
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
			p = domain.Defaults(s)
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
