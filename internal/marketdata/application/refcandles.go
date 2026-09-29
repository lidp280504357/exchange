package application

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Reference K-lines (flag market.reference_kline, test environments only):
// a symbol's chart shows the reference source's candles instead of the
// platform's (the user's decision of 2026-09-30: a test environment trades
// too little for its own candles to move). History comes from the source
// and is cached for a few seconds; the open candles of every interval are
// aggregated from the feed's live 1m updates and pushed on
// market.candle.events like the platform's. A spot pair uses its own
// reference symbol, a contract its index symbol.

const (
	// referenceCacheTTL bounds how often the same chart request reaches
	// the source; a page that ended in the past keeps a minute.
	referenceCacheTTL     = 5 * time.Second
	referencePastCacheTTL = time.Minute
	// referenceListing is how long the symbol → reference mapping holds.
	referenceListing = 30 * time.Second
)

// ReferenceCandles serves reference K-lines.
type ReferenceCandles struct {
	history     ports.ReferenceHistory
	flags       Flags
	instruments ports.Instruments
	followed    map[string]bool // the reference symbols the feed follows
	log         *slog.Logger
	now         func() time.Time

	mu       sync.Mutex
	open     map[string]map[domain.Interval]*openCandle // by reference symbol
	closed   map[string][]domain.Candle                 // ended since the last push
	pending  map[string]bool                            // reference|interval being initialized
	cache    map[string]cachedCandles
	listed   map[string]string // platform symbol → reference symbol
	listedAt time.Time
	wg       sync.WaitGroup
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

// NewReferenceCandles serves the reference symbols followed by the feed
// from history.
func NewReferenceCandles(history ports.ReferenceHistory, fl Flags, instruments ports.Instruments, followed []string,
	log *slog.Logger,
) *ReferenceCandles {
	rc := &ReferenceCandles{
		history: history, flags: fl, instruments: instruments, followed: map[string]bool{}, log: log, now: time.Now,
		open: map[string]map[domain.Interval]*openCandle{}, closed: map[string][]domain.Candle{}, pending: map[string]bool{},
		cache: map[string]cachedCandles{},
	}
	for _, s := range followed {
		rc.followed[s] = true
	}
	return rc
}

// Serves reports whether symbol's chart shows reference candles now, and
// of which reference symbol.
func (rc *ReferenceCandles) Serves(ctx context.Context, symbol string) (string, bool) {
	ref, ok := rc.mapping(ctx)[symbol]
	return ref, ok && rc.on(symbol)
}

func (rc *ReferenceCandles) on(symbol string) bool {
	return rc.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{}) &&
		rc.flags.Enabled(flags.KeyReferenceKline, flags.Subject{Symbol: symbol})
}

// mapping returns the listed symbols that have a followed reference: a
// pair its own symbol, a contract its index symbol.
func (rc *ReferenceCandles) mapping(ctx context.Context) map[string]string {
	rc.mu.Lock()
	if rc.listed != nil && rc.now().Sub(rc.listedAt) < referenceListing {
		m := rc.listed
		rc.mu.Unlock()
		return m
	}
	stale := rc.listed
	rc.mu.Unlock()
	symbols, err := rc.instruments.Symbols(ctx)
	if err != nil {
		rc.log.WarnContext(ctx, "reference K-lines: listing unavailable", "error", err)
		return stale
	}
	contracts, err := rc.instruments.Contracts(ctx)
	if err != nil {
		rc.log.WarnContext(ctx, "reference K-lines: contracts unavailable", "error", err)
		return stale
	}
	m := map[string]string{}
	for _, s := range symbols {
		if rc.followed[s] {
			m[s] = s
		}
	}
	for _, c := range contracts {
		if rc.followed[c.IndexSymbol] {
			m[c.Symbol] = c.IndexSymbol
		}
	}
	rc.mu.Lock()
	rc.listed, rc.listedAt = m, rc.now()
	rc.mu.Unlock()
	return m
}

// Candles returns symbol's reference candles like Service.Candles: the
// latest limit intervals up to to (now when zero), not before from. The
// open candle is the one aggregated from the stream when there is one.
func (rc *ReferenceCandles) Candles(ctx context.Context, symbol, ref, interval string, from, to time.Time, limit int) ([]domain.Candle, error) {
	i, ok := domain.ParseInterval(interval)
	if !ok {
		return nil, apperr.Invalid(fmt.Sprintf("interval must be one of %v", domain.Intervals))
	}
	if limit <= 0 || limit > MaxCandles {
		limit = DefaultLimit
	}
	now := rc.now()
	key := fmt.Sprintf("%s|%s|%d|%d", ref, i, to.UnixMilli(), limit)
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
		if !to.IsZero() && !i.Next(i.Start(to)).After(now) {
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
	open := rc.open[ref][i]
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

// Observe takes a live 1m candle update of a reference symbol (the feed's
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

// initialize reads the open candle of an interval from the source (locked
// by the caller; the fetch runs apart). The earlier minutes' volumes are
// the source's total less the followed minute's.
func (rc *ReferenceCandles) initialize(ref string, i domain.Interval) {
	key := ref + "|" + string(i)
	if rc.pending[key] {
		return
	}
	rc.pending[key] = true
	rc.wg.Add(1)
	go func() {
		defer rc.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		got, err := rc.history.Klines(ctx, ref, i, time.Time{}, 1)
		rc.mu.Lock()
		defer rc.mu.Unlock()
		delete(rc.pending, key)
		if err != nil || len(got) == 0 {
			if err != nil {
				rc.log.WarnContext(ctx, "reference K-lines: open candle unavailable", "symbol", ref, "interval", i, "error", err)
			}
			return // the next update asks again
		}
		minute := rc.open[ref][domain.Minute1]
		b := got[len(got)-1]
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
	}()
}

// Push turns the platform's updates into what a push publishes: the
// candles of symbols in reference mode give way to their reference
// candles (tickers stay the platform's).
func (rc *ReferenceCandles) Push(ctx context.Context, updates []Update) []Update {
	served := map[string]string{}
	for symbol, ref := range rc.mapping(ctx) {
		if rc.on(symbol) {
			served[symbol] = ref
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
