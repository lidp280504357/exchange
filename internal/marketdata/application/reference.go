package application

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

// Reference data (requirements §5.11, §11.9, ADR-0010): the latest price,
// 1m candles and rolling 24-hour ticker of every listed pair that has a
// reference market, from an external source.
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

// ReferenceFeed keeps the reference data while market.reference_feed is
// on. It follows the listed pairs that have a reference market (the
// pairs' reference_symbol) and the contracts that have one (their own
// perpetual, coin-M design §3.2), one connection per market (spot,
// USDⓈ-M, COIN-M): each starts the stream first, then loads the tickers
// and backfills the 1m candles missed since the latest stored one (at
// most a day) up to the stream's first minute. A failure reconnects that
// market with backoff; a change of the followed symbols restarts them
// all. When the flag goes off the streams stop and the data is dropped,
// so everything built on it sees none.
type ReferenceFeed struct {
	src         ports.ReferenceSource
	store       ports.Store
	flags       Flags
	instruments ports.Instruments
	log         *slog.Logger
	now         func() time.Time
	// recheck is how often the flag is looked at, remap how often the
	// followed pairs are.
	recheck time.Duration
	remap   time.Duration

	mu        sync.Mutex
	followed  []ports.Reference
	latest    map[string]Reference
	tickers   map[string]domain.Ticker
	received  map[string]time.Time // each market's latest message
	observers []func(domain.Candle)

	updates *prometheus.CounterVec
	errors  prometheus.Counter
}

// NewReferenceFeed follows the pairs instruments lists with a reference
// market on src, and registers the feed metrics with reg:
// market_reference_age_seconds per followed symbol is -1 while there is no
// price.
func NewReferenceFeed(src ports.ReferenceSource, store ports.Store, fl Flags, instruments ports.Instruments, log *slog.Logger,
	reg prometheus.Registerer,
) *ReferenceFeed {
	f := &ReferenceFeed{
		src: src, store: store, flags: fl, instruments: instruments, log: log, now: time.Now,
		recheck: 10 * time.Second, remap: time.Minute,
		latest: map[string]Reference{}, tickers: map[string]domain.Ticker{}, received: map[string]time.Time{},
		updates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_reference_updates_total", Help: "Reference candle and ticker updates received, by symbol.",
		}, []string{"symbol"}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_reference_errors_total", Help: "Reference feed connections that failed.",
		}),
	}
	reg.MustRegister(f.updates, f.errors, ageCollector{f})
	return f
}

// ageCollector reports market_reference_age_seconds of the followed
// symbols.
type ageCollector struct{ f *ReferenceFeed }

var ageDesc = prometheus.NewDesc("market_reference_age_seconds", "Age of the symbol's reference price; -1 while there is none.",
	[]string{"symbol"}, nil)

func (c ageCollector) Describe(ch chan<- *prometheus.Desc) { ch <- ageDesc }

func (c ageCollector) Collect(ch chan<- prometheus.Metric) {
	for _, ref := range c.f.Followed() {
		age := -1.0
		if r, ok := c.f.get(ref.Symbol); ok {
			age = c.f.now().Sub(r.At).Seconds()
		}
		ch <- prometheus.MustNewConstMetric(ageDesc, prometheus.GaugeValue, age, ref.Symbol)
	}
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

// Ticker returns the symbol's latest reference ticker, however old: a
// client tells a stalled feed by its time.
func (f *ReferenceFeed) Ticker(symbol string) (domain.Ticker, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tickers[symbol]
	return t, ok
}

// Followed returns the pairs and contracts the feed follows (or last
// followed).
func (f *ReferenceFeed) Followed() []ports.Reference {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.followed
}

// Received returns when the spot stream (the pairs') last sent anything;
// zero while the feed is off or has not connected.
func (f *ReferenceFeed) Received() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.received[ports.MarketSpot]
}

func (f *ReferenceFeed) setPrice(symbol string, price decimal.Decimal, at time.Time) {
	if !price.IsPositive() {
		return
	}
	f.mu.Lock()
	f.latest[symbol] = Reference{Symbol: symbol, Source: f.src.Name(), Price: price, At: at}
	f.mu.Unlock()
}

func (f *ReferenceFeed) setTicker(t domain.Ticker) {
	f.mu.Lock()
	if old, ok := f.tickers[t.Symbol]; !ok || !t.At.Before(old.At) {
		f.tickers[t.Symbol] = t
	}
	f.mu.Unlock()
}

// Observe has fn called with every live 1m candle update, on the feed's
// goroutine (reference K-lines follow the stream this way).
func (f *ReferenceFeed) Observe(fn func(domain.Candle)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observers = append(f.observers, fn)
}

func (f *ReferenceFeed) notify(c domain.Candle) {
	f.mu.Lock()
	observers := slices.Clone(f.observers)
	f.mu.Unlock()
	for _, fn := range observers {
		fn(c)
	}
}

// drop forgets everything received, when the feed goes off.
func (f *ReferenceFeed) drop() {
	f.mu.Lock()
	clear(f.latest)
	clear(f.tickers)
	clear(f.received)
	f.mu.Unlock()
}

// follow reads which pairs and contracts have a reference market, sorted
// by symbol.
func (f *ReferenceFeed) follow(ctx context.Context) ([]ports.Reference, error) {
	pairs, err := f.instruments.Pairs(ctx)
	if err != nil {
		return nil, err
	}
	contracts, err := f.instruments.Contracts(ctx)
	if err != nil {
		return nil, err
	}
	var refs []ports.Reference
	for _, p := range pairs {
		if p.Reference.Remote != "" {
			refs = append(refs, p.Reference)
		}
	}
	for _, c := range contracts {
		if ref, ok := c.Reference(); ok {
			refs = append(refs, ref)
		}
	}
	slices.SortFunc(refs, func(a, b ports.Reference) int {
		switch {
		case a.Symbol < b.Symbol:
			return -1
		case a.Symbol > b.Symbol:
			return 1
		}
		return 0
	})
	f.mu.Lock()
	f.followed = refs
	f.mu.Unlock()
	return refs, nil
}

func sameRefs(a, b []ports.Reference) bool {
	return slices.EqualFunc(a, b, ports.SameReference)
}

// Run feeds until ctx ends (an app.Loop body).
func (f *ReferenceFeed) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if !f.enabled() {
			f.drop()
			sleep(ctx, f.recheck)
			continue
		}
		refs, err := f.follow(ctx)
		if err != nil || len(refs) == 0 {
			if err != nil && ctx.Err() == nil {
				f.log.WarnContext(ctx, "reference feed: listing unavailable", "error", err)
			}
			sleep(ctx, f.recheck)
			continue
		}
		session, stop := context.WithCancel(ctx)
		go f.watch(session, stop, refs)
		byMarket := map[string][]ports.Reference{}
		for _, r := range refs {
			byMarket[r.Market] = append(byMarket[r.Market], r)
		}
		var wg sync.WaitGroup
		for _, set := range byMarket {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.market(session, set)
			}()
		}
		wg.Wait() // until the watcher stops the session: the flag went off or the followed symbols changed
		stop()
	}
	return nil
}

// market keeps one market's stream up until ctx ends, with backoff after
// failures.
func (f *ReferenceFeed) market(ctx context.Context, refs []ports.Reference) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := f.now()
		err := f.session(ctx, refs)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			f.errors.Inc()
			f.log.WarnContext(ctx, "reference feed failed", "source", f.src.Name(), "market", refs[0].Market, "error", err)
		}
		if f.now().Sub(started) > time.Minute {
			backoff = time.Second
		}
		sleep(ctx, backoff)
		backoff = min(2*backoff, time.Minute)
	}
}

// watch ends a session when the flag goes off or the followed pairs
// change.
func (f *ReferenceFeed) watch(session context.Context, stop context.CancelFunc, refs []ports.Reference) {
	mapped := f.now()
	for session.Err() == nil {
		sleep(session, f.recheck)
		if session.Err() != nil {
			return
		}
		if !f.enabled() {
			stop()
			return
		}
		if f.now().Sub(mapped) < f.remap {
			continue
		}
		mapped = f.now()
		if now, err := f.follow(session); err == nil && !sameRefs(now, refs) {
			f.log.InfoContext(session, "reference feed: followed symbols changed", "symbols", len(now))
			stop()
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// session streams refs, all of one market, until the connection ends,
// loading the tickers and backfilling the candles alongside.
func (f *ReferenceFeed) session(ctx context.Context, refs []ports.Reference) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.catchUp(ctx, refs, domain.Minute1.Start(f.now()))
	}()
	err := f.src.Stream(ctx, refs, ports.StreamHandlers{
		Candle: func(c domain.Candle) {
			at := f.now()
			f.mu.Lock()
			f.received[refs[0].Market] = at
			f.mu.Unlock()
			f.setPrice(c.Symbol, c.Close, at)
			f.updates.WithLabelValues(c.Symbol).Inc()
			f.notify(c)
			if err := f.store.Read().References().Upsert(ctx, f.src.Name(), []domain.Candle{c}); err != nil && ctx.Err() == nil {
				f.log.WarnContext(ctx, "reference candle not stored", "symbol", c.Symbol, "error", err)
			}
		},
		Ticker: func(t domain.Ticker) {
			at := f.now()
			f.mu.Lock()
			f.received[refs[0].Market] = at
			f.mu.Unlock()
			f.setTicker(t)
			f.setPrice(t.Symbol, t.Last, at)
			f.updates.WithLabelValues(t.Symbol).Inc()
		},
	})
	cancel()
	wg.Wait()
	return err
}

// catchUp loads the current tickers and the candles missed before
// streaming (the minute the stream started in).
func (f *ReferenceFeed) catchUp(ctx context.Context, refs []ports.Reference, streaming time.Time) {
	if tickers, err := f.src.Tickers(ctx, refs); err != nil {
		if ctx.Err() == nil {
			f.log.WarnContext(ctx, "reference tickers not loaded", "error", err)
		}
	} else {
		for _, t := range tickers {
			f.setTicker(t)
		}
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		if err := f.backfill(ctx, ref, streaming); err != nil && ctx.Err() == nil {
			f.log.WarnContext(ctx, "reference backfill failed", "symbol", ref.Symbol, "error", err)
		}
	}
}

// backfill fetches the candles since the latest stored one up to the
// minute the stream started in (the stream has everything from there).
func (f *ReferenceFeed) backfill(ctx context.Context, ref ports.Reference, to time.Time) error {
	r := f.store.Read().References()
	from := to.Add(-referenceBackfill)
	if last, err := r.Latest(ctx, f.src.Name(), ref.Symbol); err != nil {
		return err
	} else if last != nil && last.OpenTime.After(from) {
		from = last.OpenTime // redone: it may have been open
	}
	if !from.Before(to) {
		return nil
	}
	candles, err := f.src.Backfill(ctx, ref, from, to)
	if err != nil {
		return err
	}
	if err := r.Upsert(ctx, f.src.Name(), candles); err != nil {
		return err
	}
	f.log.InfoContext(ctx, "reference backfilled", "source", f.src.Name(), "symbol", ref.Symbol, "candles", len(candles))
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
