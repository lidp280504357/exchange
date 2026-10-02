// Command market-data-service builds the market data (requirements §5.11,
// §11.8; ADR-0010): candles, tickers, recent trades and depth of pairs and
// contracts over REST, candle and ticker updates on market.candle.events,
// and the public books and trades (market.depth, derivatives.market.depth,
// market.trades) for the WebSocket gateway: the reference market's for
// the symbols that show it, the platform's own (the engines' depth and
// trade.events) for the others. For perpetual contracts it computes the
// index and mark prices and the funding rates (§11.7).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/adapters/binance"
	"github.com/lidp280504357/exchange/internal/marketdata/adapters/instruments"
	"github.com/lidp280504357/exchange/internal/marketdata/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/marketdata/application"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
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
	// The reference source is Binance public data at BINANCE_REST_URL and
	// BINANCE_STREAM_URL (§11.9, ADR-0010: test environments only). It
	// follows the pairs whose reference_symbol is set while
	// market.reference_feed is on.
	BinanceREST   string `koanf:"binance_rest_url"`
	BinanceStream string `koanf:"binance_stream_url"`
	// The contracts' books and trades come from Binance USDⓈ-M futures at
	// BINANCE_FUTURES_REST_URL and BINANCE_FUTURES_STREAM_URL.
	BinanceFuturesREST   string `koanf:"binance_futures_rest_url"`
	BinanceFuturesStream string `koanf:"binance_futures_stream_url"`
	// IndexMinSources is the fewest reference sources an index price
	// needs (INDEX_MIN_SOURCES, §11.7: 2); test environments with Binance
	// alone set 1.
	IndexMinSources int `koanf:"index_min_sources"`
	// IndexSourceWeights weigh the sources in the index median
	// (INDEX_SOURCE_WEIGHTS, e.g. binance=1); unlisted sources weigh 1,
	// 0 leaves a source out.
	IndexSourceWeights []string `koanf:"index_source_weights"`
}

func (s *settings) Validate() error {
	var errs []error
	if s.IndexMinSources < 1 {
		errs = append(errs, errors.New("INDEX_MIN_SOURCES must be at least 1"))
	}
	if _, err := weights(s.IndexSourceWeights); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate())...)
}

// weights parses name=weight pairs.
func weights(list []string) (map[string]int32, error) {
	out := map[string]int32{}
	for _, item := range list {
		name, w, ok := strings.Cut(strings.TrimSpace(item), "=")
		n, err := strconv.ParseInt(w, 10, 32)
		if !ok || name == "" || err != nil || n < 0 {
			return nil, fmt.Errorf("INDEX_SOURCE_WEIGHTS: %q is not name=weight", item)
		}
		out[name] = int32(n)
	}
	return out, nil
}

func main() {
	app.Main("market-data-service", setup, app.WithDefaultOpsAddr(":9090"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8090", Postgres: pg.DefaultConfig(), InstrumentAddr: "localhost:9184",
		BinanceREST: "https://data-api.binance.vision", BinanceStream: "wss://data-stream.binance.vision",
		BinanceFuturesREST: "https://fapi.binance.com", BinanceFuturesStream: "wss://fstream.binance.com",
		IndexMinSources: 2,
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
	listed := instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 30*time.Second)
	svc := application.New(store, listed, a.Logger())
	if err := svc.Load(ctx); err != nil {
		return err
	}
	prod, err := bootstrap.Producer(ctx, a, cfg.Kafka)
	if err != nil {
		return err
	}
	// Business events (risk.events' degradations) go through the outbox;
	// derived market data (depth, candles, mark prices) straight out.
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	// Reference market data (ADR-0010): candles, tickers, prices, books and
	// trades of the symbols with a reference market, while
	// market.reference_feed is on.
	src := binance.New(cfg.BinanceREST, cfg.BinanceStream, &http.Client{Timeout: 15 * time.Second}).
		WithFutures(cfg.BinanceFuturesREST, cfg.BinanceFuturesStream)
	refs := application.NewReferenceMap(listed, a.Logger())
	books := application.NewBooks(src, refs, flagClient, prod, events, a.Logger(), a.Metrics())
	a.Add("reference books", app.Loop(books.Run))
	a.Add("public books", app.Loop(books.Push))
	// Pairs and contracts alike: their symbols differ. The platform's
	// trades are relayed as public trades where the reference market's are
	// not shown.
	if err := bootstrap.BatchConsumerWith(ctx, a, cfg.Kafka, kafka.BatchOptions{
		Group: consumer.Group, Topics: []string{event.TopicTrade, event.TopicDerivTrade}, Handler: consumer.Trades(svc, books),
		MaxBatch: 500, MaxWait: 50 * time.Millisecond,
	}); err != nil {
		return err
	}
	// Only the latest depth matters: read the engines' depth from the end,
	// relayed as the public book where the reference market's is not shown.
	if err := bootstrap.Tail(ctx, a, cfg.Kafka, []string{event.TopicMarketDepthInternal, event.TopicDerivMarketDepthInternal},
		consumer.Depth(svc, books)); err != nil {
		return err
	}
	pusher := application.NewPusher(svc, prod, events, a.Metrics())
	a.Add("market push", app.Loop(pusher.Run))
	feed := application.NewReferenceFeed(src, store, flagClient, listed, a.Logger(), a.Metrics())
	a.Add("reference feed", app.Loop(feed.Run))
	refKlines := application.NewReferenceCandles(src, flagClient, refs, a.Logger())
	feed.Observe(refKlines.Observe)
	tickers := application.NewTickers(svc, feed, refs, flagClient, listed)
	pusher.Use(refKlines.Push)
	pusher.Use(tickers.Push)
	guard := application.NewFeedGuard(feed, listed, store, flagClient, a.Logger(), a.Metrics())
	a.Add("feed guard", app.Loop(guard.Run))
	sourceWeights, _ := weights(cfg.IndexSourceWeights) // validated
	// An index pair the reference market does not follow (the platform
	// coin) is priced by the platform's own market.
	indexes := application.PlatformIndex{Feed: feed, Svc: svc, Refs: refs}
	marks := application.NewMarks(svc, listed, indexes, store, pusher, prod, events,
		application.MarksConfig{MinSources: cfg.IndexMinSources, Weights: sourceWeights}, a.Logger(), a.Metrics())
	marks.UseReferenceBooks(books.Levels) // HOUSE trades at the reference book's prices (ADR-0015)
	a.Add("contract prices", app.Loop(marks.Run))
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
			if n, err := feed.Purge(ctx); err != nil {
				a.Logger().WarnContext(ctx, "reference purge failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "reference candles purged", "candles", n)
			}
		}
	}))
	// The market lists' trend lines, each symbol's hourly closes kept five
	// minutes: from the chart's source, the reference market's or ours.
	sparks := &application.Sparklines{Now: time.Now, TTL: 5 * time.Minute, Closes: func(ctx context.Context, symbol string, hours int) ([]domain.Candle, error) {
		if ref, ok := refKlines.Serves(ctx, symbol); ok {
			return refKlines.Candles(ctx, symbol, ref, "1h", time.Time{}, time.Time{}, hours)
		}
		return svc.Candles(ctx, symbol, "1h", time.Time{}, time.Time{}, hours)
	}}
	r := a.NewRouter()
	(&httpapi.Handler{
		Svc: svc, Tickers: tickers, Ref: feed, Guard: guard, Marks: marks, RefKlines: refKlines, Books: books, Sparks: sparks, Now: time.Now,
	}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}
