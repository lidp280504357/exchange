package application

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// Reference K-lines (flag market.reference_kline, ADR-0010): a symbol's
// chart shows the reference market's candles instead of the platform's.
// History comes from the source and is cached for a few seconds; the open
// candles of every interval are aggregated from the feed's live 1m
// updates and pushed on market.candle.events like the platform's. A spot
// pair follows its own reference market, a contract its index pair's.

const (
	// referenceCacheTTL bounds how often the same chart request reaches
	// the source; a page that ended in the past keeps a minute.
	referenceCacheTTL     = 5 * time.Second
	referencePastCacheTTL = time.Minute
	// referenceListing is how long the symbol → reference mapping holds.
	referenceListing = 30 * time.Second
)

// ReferenceCandles serves reference K-lines. Open candles are kept by the
// followed pair's symbol, which the feed's updates carry.
type ReferenceCandles struct {
	history ports.ReferenceHistory
	flags   Flags
	refs    *ReferenceMap
	log     *slog.Logger
	now     func() time.Time

	mu      sync.Mutex
	open    map[string]map[domain.Interval]*openCandle // by followed pair
	closed  map[string][]domain.Candle                 // ended since the last push
	pending map[string]bool                            // pair|interval queued or being initialized
	queue   []initRequest                              // waiting for the source, see initialize
	working bool                                       // the initializer is running
	cache   map[string]cachedCandles
	wg      sync.WaitGroup
}

// initRequest is an open candle to read from the source.
type initRequest struct {
	ref      string
	interval domain.Interval
}

// initRank orders the open candles read from the source: the intervals
// charted most first (the terminals open on 15m).
var initRank = map[domain.Interval]int{
	domain.Minute15: 0, domain.Hour1: 1, domain.Hour4: 2, domain.Day1: 3, domain.Minute5: 4, domain.Minute30: 5, domain.Week1: 6,
	domain.Minute3: 7, domain.Hour2: 8, domain.Hour6: 9, domain.Hour12: 10, domain.Month1: 11,
}

// initTimeout bounds one read of an open candle, the wait for its turn at
// the source included.
const initTimeout = 30 * time.Second

// ReferenceMap tells which listed symbols show which reference market: a
// pair its own (its reference_symbol), a contract its own perpetual on
// its futures market (its reference_symbol: USDⓈ-M, or COIN-M for a
// coin-margined one; coin-M design §3.2). The mapping is cached for
// referenceListing; the stale one serves while the listing is
// unavailable.
type ReferenceMap struct {
	instruments ports.Instruments
	log         *slog.Logger
	now         func() time.Time

	mu        sync.Mutex
	m         map[string]ports.Reference
	pairs     map[string]bool // the listed pairs
	contracts map[string]bool // the listed contracts
	at        time.Time
}

// NewReferenceMap reads the mapping from instruments.
func NewReferenceMap(instruments ports.Instruments, log *slog.Logger) *ReferenceMap {
	return &ReferenceMap{instruments: instruments, log: log, now: time.Now}
}

// Get returns the mapping, refreshed when older than referenceListing.
func (r *ReferenceMap) Get(ctx context.Context) map[string]ports.Reference {
	r.mu.Lock()
	if r.m != nil && r.now().Sub(r.at) < referenceListing {
		m := r.m
		r.mu.Unlock()
		return m
	}
	stale := r.m
	r.mu.Unlock()
	pairs, err := r.instruments.Pairs(ctx)
	if err != nil {
		r.log.WarnContext(ctx, "reference mapping: listing unavailable", "error", err)
		return stale
	}
	contracts, err := r.instruments.Contracts(ctx)
	if err != nil {
		r.log.WarnContext(ctx, "reference mapping: contracts unavailable", "error", err)
		return stale
	}
	m, listed, perps := map[string]ports.Reference{}, map[string]bool{}, map[string]bool{}
	for _, p := range pairs {
		listed[p.Symbol] = true
		if p.Reference.Remote != "" {
			m[p.Symbol] = p.Reference
		}
	}
	for _, c := range contracts {
		perps[c.Symbol] = true
		if ref, ok := c.Reference(); ok {
			m[c.Symbol] = ref // its own perpetual (coin-M design §3.2)
		}
	}
	r.mu.Lock()
	r.m, r.pairs, r.contracts, r.at = m, listed, perps, r.now()
	r.mu.Unlock()
	return m
}

// Unreferenced lists the listed pairs and contracts no reference market
// follows (the platform coin's and its perpetual): their charts are the
// platform's own trades. Not known (false) while the listing was never
// read (reviews AU, AV); the last one read stands in while it cannot be.
func (r *ReferenceMap) Unreferenced(ctx context.Context) ([]string, bool) {
	m := r.Get(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if m == nil {
		return nil, false
	}
	out := []string{}
	for _, listed := range []map[string]bool{r.pairs, r.contracts} {
		for symbol := range listed {
			if _, followed := m[symbol]; !followed {
				out = append(out, symbol)
			}
		}
	}
	return out, true
}

// Unfollowed reports whether symbol is a listed pair that no reference
// market follows (the platform coin's); false while the listing was never
// read.
func (r *ReferenceMap) Unfollowed(ctx context.Context, symbol string) bool {
	m := r.Get(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	_, followed := m[symbol]
	return m != nil && r.pairs[symbol] && !followed
}

// FollowsPair reports whether symbol is a spot pair a reference market
// follows, as last read: the pairs a price event may overlay (design
// 2026-10-07, general price control).
func (r *ReferenceMap) FollowsPair(symbol string) bool {
	ref, ok := r.cached()[symbol]
	return ok && ref.Market == ports.MarketSpot
}

// cached returns the mapping as last read, without reading it again.
func (r *ReferenceMap) cached() map[string]ports.Reference {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m
}

// openCandle is the open candle of one interval, built from 1m updates:
// base holds the volumes and trades of the interval's earlier minutes,
// minute the latest update of the minute being followed.
type openCandle struct {
	c       domain.Candle
	base    domain.Candle
	minute  domain.Candle
	changed bool
}

type cachedCandles struct {
	candles []domain.Candle
	until   time.Time
}

// NewReferenceCandles serves the reference markets refs maps from
// history.
func NewReferenceCandles(history ports.ReferenceHistory, fl Flags, refs *ReferenceMap, log *slog.Logger) *ReferenceCandles {
	return &ReferenceCandles{
		history: history, flags: fl, refs: refs, log: log, now: time.Now,
		open: map[string]map[domain.Interval]*openCandle{}, closed: map[string][]domain.Candle{}, pending: map[string]bool{},
		cache: map[string]cachedCandles{},
	}
}

// Serves reports whether symbol's chart shows reference candles now, and
// of which reference market.
func (rc *ReferenceCandles) Serves(ctx context.Context, symbol string) (ports.Reference, bool) {
	ref, ok := rc.refs.Get(ctx)[symbol]
	return ref, ok && rc.on(symbol)
}

func (rc *ReferenceCandles) on(symbol string) bool {
	return rc.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{}) &&
		rc.flags.Enabled(flags.KeyReferenceKline, flags.Subject{Symbol: symbol})
}

// Candles returns symbol's reference candles like Service.Candles: the
// latest limit intervals up to to (now when zero), not before from. The
// open candle is the one aggregated from the stream when there is one.
func (rc *ReferenceCandles) Candles(ctx context.Context, symbol string, ref ports.Reference, interval string, from, to time.Time,
	limit int,
) ([]domain.Candle, error) {
	i, ok := domain.ParseInterval(interval)
	if !ok {
		return nil, apperr.Invalid(fmt.Sprintf("interval must be one of %v", domain.Intervals))
	}
	if limit <= 0 || limit > MaxCandles {
		limit = DefaultLimit
	}
	now := rc.now()
	key := fmt.Sprintf("%s|%s|%d|%d", ref.Symbol, i, to.UnixMilli(), limit)
	rc.mu.Lock()
	hit, cached := rc.cache[key]
	rc.mu.Unlock()
	list := hit.candles
	if !cached || !now.Before(hit.until) {
		got, err := rc.history.Klines(ctx, ref, i, to, limit)
		if err != nil {
			return nil, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "reference candles are unavailable")
		}
		ttl := referenceCacheTTL
		if !to.IsZero() && !to.After(now) {
			ttl = referencePastCacheTTL
		}
		rc.mu.Lock()
		for k, v := range rc.cache {
			if !now.Before(v.until) {
				delete(rc.cache, k)
			}
		}
		rc.cache[key] = cachedCandles{candles: got, until: now.Add(ttl)}
		rc.mu.Unlock()
		list = got
	}
	earliest := time.Time{}
	if !from.IsZero() {
		earliest = i.Start(from)
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if to.IsZero() && len(list) > 0 && i != domain.Minute1 {
		rc.seedLocked(ref.Symbol, i, list[len(list)-1]) // the latest page ends with the open candle
	}
	open := rc.open[ref.Symbol][i]
	out := make([]domain.Candle, 0, len(list))
	for _, c := range list {
		if c.OpenTime.Before(earliest) {
			continue
		}
		if open != nil && c.OpenTime.Equal(open.c.OpenTime) {
			c = open.c
		}
		c.Symbol, c.Interval = symbol, i
		out = append(out, c)
	}
	return out, nil
}

// Observe takes a live 1m candle update of a followed pair (the feed's
// hook) into the open candle of every interval. An interval seen for the
// first time in the middle is initialized from the source.
func (rc *ReferenceCandles) Observe(k domain.Candle) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	ref := k.Symbol
	byInterval := rc.open[ref]
	if byInterval == nil {
		byInterval = map[domain.Interval]*openCandle{}
		rc.open[ref] = byInterval
	}
	for _, i := range domain.Intervals {
		start := i.Start(k.OpenTime)
		st := byInterval[i]
		switch {
		case st != nil && st.c.OpenTime.Equal(start):
			st.add(k)
		case st != nil && st.c.OpenTime.After(start):
			// an update older than the open candle: ignored
		default:
			if st != nil { // the interval ended
				rc.closed[ref] = append(rc.closed[ref], st.c)
				delete(byInterval, i)
			}
			if i == domain.Minute1 || k.OpenTime.Equal(start) {
				byInterval[i] = newOpenCandle(k, i, start)
			} else {
				rc.initialize(ref, i)
			}
		}
	}
}

func newOpenCandle(k domain.Candle, i domain.Interval, start time.Time) *openCandle {
	c := k
	c.Interval, c.OpenTime = i, start
	return &openCandle{c: c, minute: k, changed: true}
}

// add folds a 1m update into the open candle.
func (o *openCandle) add(k domain.Candle) {
	switch {
	case k.OpenTime.Before(o.minute.OpenTime):
		return
	case k.OpenTime.After(o.minute.OpenTime): // the followed minute ended
		o.base.Volume = o.base.Volume.Add(o.minute.Volume)
		o.base.QuoteVolume = o.base.QuoteVolume.Add(o.minute.QuoteVolume)
		o.base.Trades += o.minute.Trades
	}
	o.minute = k
	o.c.High, o.c.Low, o.c.Close = decimal.Max(o.c.High, k.High), decimal.Min(o.c.Low, k.Low), k.Close
	o.c.Volume = o.base.Volume.Add(k.Volume)
	o.c.QuoteVolume = o.base.QuoteVolume.Add(k.QuoteVolume)
	o.c.Trades = o.base.Trades + k.Trades
	o.changed = true
}

// initialize queues the open candle of an interval to be read from the
// source (locked by the caller). One reader works through the queue, the
// most charted intervals first: after a restart every followed pair finds
// most intervals midway (50 pairs, some 600 candles), and reading them all
// at once would crowd the reference books' snapshots out of the source's
// request budget. A chart request seeds its interval sooner (Candles).
func (rc *ReferenceCandles) initialize(ref string, i domain.Interval) {
	key := ref + "|" + string(i)
	if rc.pending[key] {
		return
	}
	if _, ok := rc.refs.cached()[ref]; !ok {
		return // the next update asks again once the mapping is known
	}
	rc.pending[key] = true
	rc.queue = append(rc.queue, initRequest{ref: ref, interval: i})
	if !rc.working {
		rc.working = true
		rc.wg.Add(1)
		go rc.initializer()
	}
}

// initializer reads the queued open candles one by one until the queue is
// empty.
func (rc *ReferenceCandles) initializer() {
	defer rc.wg.Done()
	for {
		rc.mu.Lock()
		if len(rc.queue) == 0 {
			rc.working = false
			rc.mu.Unlock()
			return
		}
		next := 0
		for k, q := range rc.queue {
			if initRank[q.interval] < initRank[rc.queue[next].interval] {
				next = k
			}
		}
		req := rc.queue[next]
		rc.queue = slices.Delete(rc.queue, next, next+1)
		mapped, known := rc.refs.cached()[req.ref]
		done := rc.open[req.ref][req.interval] != nil // seeded by a chart request meanwhile
		rc.mu.Unlock()
		if !known || done {
			rc.forget(req)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
		got, err := rc.history.Klines(ctx, mapped, req.interval, time.Time{}, 1)
		cancel()
		rc.mu.Lock()
		delete(rc.pending, req.ref+"|"+string(req.interval))
		if err != nil || len(got) == 0 {
			rc.mu.Unlock()
			if err != nil {
				rc.log.Warn("reference K-lines: open candle unavailable", "symbol", req.ref, "interval", req.interval, "error", err)
			}
			continue // the next update asks again
		}
		rc.seedLocked(req.ref, req.interval, got[len(got)-1])
		rc.mu.Unlock()
	}
}

func (rc *ReferenceCandles) forget(req initRequest) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	delete(rc.pending, req.ref+"|"+string(req.interval))
}

// seedLocked starts the open candle of an interval from the source's
// candle b when it is the one the followed minute belongs to and the
// interval has none yet: the earlier minutes' volumes are the source's
// total less the followed minute's. rc.mu is held.
func (rc *ReferenceCandles) seedLocked(ref string, i domain.Interval, b domain.Candle) {
	minute := rc.open[ref][domain.Minute1]
	if minute == nil || !b.OpenTime.Equal(i.Start(minute.minute.OpenTime)) || rc.open[ref][i] != nil {
		return
	}
	m := minute.minute
	o := &openCandle{c: b, minute: m, changed: true}
	o.c.Symbol, o.c.Interval = ref, i
	o.base.Volume = decimal.Max(b.Volume.Sub(m.Volume), decimal.Zero)
	o.base.QuoteVolume = decimal.Max(b.QuoteVolume.Sub(m.QuoteVolume), decimal.Zero)
	o.base.Trades = max(b.Trades-m.Trades, 0)
	o.c.High, o.c.Low, o.c.Close = decimal.Max(b.High, m.High), decimal.Min(b.Low, m.Low), m.Close
	rc.open[ref][i] = o
}

// Push turns the platform's updates into what a push publishes: the
// candles of symbols in reference mode give way to their reference
// candles (tickers stay the platform's).
func (rc *ReferenceCandles) Push(ctx context.Context, updates []Update) []Update {
	served := map[string]string{}
	for symbol, ref := range rc.refs.Get(ctx) {
		if rc.on(symbol) {
			served[symbol] = ref.Symbol
		}
	}
	out := make([]Update, 0, len(updates))
	for _, u := range updates {
		if _, ok := served[u.Symbol]; ok {
			switch u.Message.(type) {
			case *marketv1.CandleUpdated, *marketv1.CandleClosed:
				continue
			}
		}
		out = append(out, u)
	}
	symbols := make([]string, 0, len(served))
	for s := range served {
		symbols = append(symbols, s)
	}
	slices.Sort(symbols)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, symbol := range symbols {
		ref := served[symbol]
		for _, c := range rc.closed[ref] {
			c.Symbol = symbol
			out = append(out, Update{symbol, &marketv1.CandleClosed{Candle: candleProto(c, true)}})
		}
		for _, i := range domain.Intervals {
			if st := rc.open[ref][i]; st != nil && st.changed {
				c := st.c
				c.Symbol = symbol
				out = append(out, Update{symbol, &marketv1.CandleUpdated{Candle: candleProto(c, false)}})
			}
		}
	}
	clear(rc.closed)
	for _, byInterval := range rc.open {
		for _, st := range byInterval {
			st.changed = false
		}
	}
	return out
}
