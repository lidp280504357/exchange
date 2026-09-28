// Command market-data-service builds the platform market data from the
// platform's own trades and the engine's depth (requirements §5.11,
// §11.8): candles, tickers, recent trades and depth over REST, and candle
// and ticker updates on market.candle.events for the WebSocket gateway.
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/adapters/binance"
	"github.com/lidp280504357/exchange/internal/marketdata/adapters/instruments"
	"github.com/lidp280504357/exchange/internal/marketdata/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/marketdata/application"
	"github.com/lidp280504357/exchange/internal/marketdata/transport/consumer"
	"github.com/lidp280504357/exchange/internal/marketdata/transport/httpapi"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string       `koanf:"http_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// InstrumentAddr is instrument-service's gRPC address, for the listed
	// pairs (INSTRUMENT_GRPC_ADDR).
	InstrumentAddr string `koanf:"instrument_grpc_addr"`
	// ReferenceSymbols are the pairs that get reference prices while
	// market.reference_feed is on (REFERENCE_SYMBOLS, e.g. BTC-USDT);
	// the source is Binance public data at BINANCE_REST_URL and
	// BINANCE_STREAM_URL (§11.9: test environments only).
	ReferenceSymbols []string `koanf:"reference_symbols"`
	BinanceREST      string   `koanf:"binance_rest_url"`
	BinanceStream    string   `koanf:"binance_stream_url"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("market-data-service", setup, app.WithDefaultOpsAddr(":9090"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8090", Postgres: pg.DefaultConfig(), InstrumentAddr: "localhost:9184",
		BinanceREST: "https://data-api.binance.vision", BinanceStream: "wss://data-stream.binance.vision",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "market", migrations.Market())
	if err != nil {
		return err
	}
	instrumentConn, err := bootstrap.GRPCClient(a, "instrument", cfg.InstrumentAddr)
	if err != nil {
		return err
	}
	store := postgres.NewStore(db)
	svc := application.New(store, instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 30*time.Second), a.Logger())
	if err := svc.Load(ctx); err != nil {
		return err
	}
	if err := bootstrap.BatchConsumerWith(ctx, a, cfg.Kafka, kafka.BatchOptions{
		Group: consumer.Group, Topics: []string{event.TopicTrade}, Handler: consumer.Trades(svc),
		MaxBatch: 500, MaxWait: 50 * time.Millisecond,
	}); err != nil {
		return err
	}
	// Only the latest depth matters: read market.depth from its end.
	if err := bootstrap.Tail(ctx, a, cfg.Kafka, []string{event.TopicMarketDepth}, consumer.Depth(svc)); err != nil {
		return err
	}
	prod, err := bootstrap.Producer(ctx, a, cfg.Kafka)
	if err != nil {
		return err
	}
	pusher := application.NewPusher(svc, prod, event.NewFactory(a.Name(), a.Config().InstanceID), a.Metrics())
	a.Add("market push", app.Loop(pusher.Run))
	var feed *application.ReferenceFeed
	if len(cfg.ReferenceSymbols) > 0 {
		flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
		if err != nil {
			return err
		}
		src := binance.New(cfg.BinanceREST, cfg.BinanceStream, &http.Client{Timeout: 15 * time.Second})
		feed = application.NewReferenceFeed(src, store, flagClient, cfg.ReferenceSymbols, a.Logger(), a.Metrics())
		a.Add("reference feed", app.Loop(feed.Run))
	}
	a.Add("purge", app.Loop(func(ctx context.Context) error {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			if n, err := svc.PurgeTrades(ctx); err != nil {
				a.Logger().WarnContext(ctx, "trade purge failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "trades purged", "trades", n)
			}
			if feed != nil {
				if n, err := feed.Purge(ctx); err != nil {
					a.Logger().WarnContext(ctx, "reference purge failed", "error", err)
				} else if n > 0 {
					a.Logger().InfoContext(ctx, "reference candles purged", "candles", n)
				}
			}
		}
	}))
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc, Ref: feed, Now: time.Now}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}
