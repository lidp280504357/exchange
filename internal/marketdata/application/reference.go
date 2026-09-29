package application

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Reference prices (requirements §5.11, §11.9): the latest price of each
// configured symbol from an external source, and its 1m candles.
const (
	// ReferenceStale is how old a reference may be before quotes built on
	// it stop (§11.10: 5 seconds).
	ReferenceStale    = 5 * time.Second
	referenceBackfill = 24 * time.Hour
	referenceKeep     = 7 * 24 * time.Hour
)

// Reference is a symbol's latest external price.
type Reference struct {
	Symbol string
	Source string
	Price  decimal.Decimal
	At     time.Time
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}

// ReferenceFeed keeps the reference prices while market.reference_feed is
// on: each connection first backfills the candles missed since the latest
// stored one (at most a day), then streams; a failure reconnects with
// backoff. When the flag goes off the stream stops and the prices are
// dropped, so everything built on them sees none.
type ReferenceFeed struct {
	src     ports.ReferenceSource
	store   ports.Store
	flags   Flags
	symbols []string
	log     *slog.Logger
	now     func() time.Time
	// recheck is how often the flag is looked at.
	recheck time.Duration

	mu     sync.Mutex
	latest map[string]Reference

	updates *prometheus.CounterVec
	errors  prometheus.Counter
}

// NewReferenceFeed follows symbols on src and registers the feed metrics
// with reg: market_reference_age_seconds per symbol is -1 while there is
// no price.
func NewReferenceFeed(src ports.ReferenceSource, store ports.Store, fl Flags, symbols []string, log *slog.Logger, reg prometheus.Registerer) *ReferenceFeed {
	f := &ReferenceFeed{
		src: src, store: store, flags: fl, symbols: symbols, log: log, now: time.Now, recheck: 10 * time.Second,
		latest: map[string]Reference{},
		updates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_reference_updates_total", Help: "Reference candle updates received, by symbol.",
		}, []string{"symbol"}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_reference_errors_total", Help: "Reference feed connections that failed.",
		}),
	}
	reg.MustRegister(f.updates, f.errors)
	for _, s := range symbols {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "market_reference_age_seconds", Help: "Age of the symbol's reference price; -1 while there is none.",
			ConstLabels: prometheus.Labels{"symbol": s},
		}, func() float64 {
			r, ok := f.get(s)
			if !ok {
				return -1
			}
			return f.now().Sub(r.At).Seconds()
		}))
	}
	return f
}

func (f *ReferenceFeed) enabled() bool {
	return f.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{})
}

func (f *ReferenceFeed) get(symbol string) (Reference, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.latest[symbol]
	return r, ok
}

// Latest returns the symbol's reference and whether it is fresh (younger
// than ReferenceStale).
func (f *ReferenceFeed) Latest(symbol string) (Reference, bool) {
	r, ok := f.get(symbol)
	return r, ok && f.now().Sub(r.At) < ReferenceStale
}

func (f *ReferenceFeed) set(c domain.Candle, at time.Time) {
	f.mu.Lock()
	f.latest[c.Symbol] = Reference{Symbol: c.Symbol, Source: f.src.Name(), Price: c.Close, At: at}
	f.mu.Unlock()
}

// Run feeds until ctx ends (an app.Loop body).
func (f *ReferenceFeed) Run(ctx context.Context) error {
	backoff := time.Second
	for ctx.Err() == nil {
		if !f.enabled() {
			f.mu.Lock()
			clear(f.latest)
			f.mu.Unlock()
			sleep(ctx, f.recheck)
			continue
		}
		session, stop := context.WithCancel(ctx)
		go func() { // the flag may go off while streaming
			for session.Err() == nil {
				sleep(session, f.recheck)
				if !f.enabled() {
					stop()
				}
			}
		}()
		started := f.now()
		err := f.session(session)
		stop()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && f.enabled() {
			f.errors.Inc()
			f.log.WarnContext(ctx, "reference feed failed", "source", f.src.Name(), "error", err)
		}
		if f.now().Sub(started) > time.Minute {
			backoff = time.Second
		}
		sleep(ctx, backoff)
		backoff = min(2*backoff, time.Minute)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// session backfills every symbol, then streams until the connection ends.
func (f *ReferenceFeed) session(ctx context.Context) error {
	for _, symbol := range f.symbols {
		if err := f.backfill(ctx, symbol); err != nil {
			return err
		}
	}
	return f.src.Stream(ctx, f.symbols, func(c domain.Candle) {
		f.set(c, f.now())
		f.updates.WithLabelValues(c.Symbol).Inc()
		if err := f.store.Read().References().Upsert(ctx, f.src.Name(), []domain.Candle{c}); err != nil && ctx.Err() == nil {
			f.log.WarnContext(ctx, "reference candle not stored", "symbol", c.Symbol, "error", err)
		}
	})
}

// backfill fetches the candles since the latest stored one; the current
// minute's candle also sets the price.
func (f *ReferenceFeed) backfill(ctx context.Context, symbol string) error {
	r := f.store.Read().References()
	from := f.now().Add(-referenceBackfill)
	if last, err := r.Latest(ctx, f.src.Name(), symbol); err != nil {
		return err
	} else if last != nil && last.OpenTime.After(from) {
		from = last.OpenTime // redone: it may have been open
	}
	candles, err := f.src.Backfill(ctx, symbol, from)
	if err != nil {
		return err
	}
	if err := r.Upsert(ctx, f.src.Name(), candles); err != nil {
		return err
	}
	if n := len(candles); n > 0 && f.now().Sub(candles[n-1].OpenTime) < 2*time.Minute {
		f.set(candles[n-1], f.now())
	}
	f.log.InfoContext(ctx, "reference backfilled", "source", f.src.Name(), "symbol", symbol, "candles", len(candles))
	return nil
}

// Purge deletes reference candles older than the retention.
func (f *ReferenceFeed) Purge(ctx context.Context) (int64, error) {
	return f.store.Read().References().Purge(ctx, f.now().Add(-referenceKeep))
}

// Prices returns the symbol's fresh reference price as an index source
// (application.IndexSources); none while it is stale.
func (f *ReferenceFeed) Prices(symbol string) []domain.SourcePrice {
	r, fresh := f.Latest(symbol)
	if !fresh {
		return nil
	}
	return []domain.SourcePrice{{Source: r.Source, Price: r.Price}}
}
