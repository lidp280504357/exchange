package main

import (
	"net/http"
	"time"

	"github.com/skill/exchange/internal/marketdata/adapters/binance"
	"github.com/skill/exchange/internal/marketdata/adapters/postgres"
	"github.com/skill/exchange/internal/marketdata/application"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/pg"
)

// futuresDataSettings are the reference market's futures endpoints the
// contracts' data panel reads (design 2026-10-06 §3.3): USDⓈ-M at
// BINANCE_FUTURES_REST_URL and BINANCE_FUTURES_STREAM_URL (the books'
// too), COIN-M at BINANCE_COINM_REST_URL and BINANCE_COINM_STREAM_URL.
type futuresDataSettings struct {
	USDMREST    string `koanf:"binance_futures_rest_url"`
	USDMStream  string `koanf:"binance_futures_stream_url"`
	CoinMREST   string `koanf:"binance_coinm_rest_url"`
	CoinMStream string `koanf:"binance_coinm_stream_url"`
}

// futuresData reads the reference market's statistics, open interest and
// liquidations of the contracts while market.futures_data is on.
func futuresData(a *app.App, db *pg.DB, listed ports.Instruments, fl application.Flags) (*application.FuturesStats, error) {
	cfg := futuresDataSettings{
		USDMREST: "https://fapi.binance.com", USDMStream: "wss://fstream.binance.com",
		CoinMREST: "https://dapi.binance.com", CoinMStream: "wss://dstream.binance.com",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return nil, err
	}
	src := binance.NewFutures(cfg.USDMREST, cfg.USDMStream, cfg.CoinMREST, cfg.CoinMStream, &http.Client{Timeout: 15 * time.Second})
	stats := application.NewFuturesStats(src, postgres.NewFuturesStats(db), listed, fl, a.Logger(), a.Metrics())
	a.Add("futures statistics", app.Loop(stats.Run))
	return stats, nil
}
