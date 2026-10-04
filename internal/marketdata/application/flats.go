package application

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
)

// Flat minutes (coordinator 2026-10-04: the platform coin's 1-minute
// candles broke up wherever a minute had no trade). For a symbol whose
// chart is the platform's own trades (no reference market follows it)
// with market.flat_minutes on, the minutes without a trade before one of
// its trades become stored candles of their own when that trade is
// applied: open, high, low and close the previous close, no volume and no
// trades. Every longer interval takes each as a trade of nothing at that
// price, as rolling up the one-minute candles does in ClickHouse's
// candles(): one whose first minute was flat opens at the previous close,
// and its high and low count it.
//
// Made from the trades, never from the clock (review AU): a minute is
// known to have had no trade only once a later trade of the symbol is
// applied, the symbol's trades coming in sequence order. A trade consumer
// behind by any time makes the same flats, only later, and ClickHouse gets
// each on market.candle.flats through the outbox, in the transaction that
// stores it. Until the next trade the WebSocket shows the quiet minutes as
// running flat candles (the publisher's) and REST fills them in reading.
const (
	// FlatCatchUp is how far back a trade fills: after a longer quiet
	// spell, the minutes before it stay gaps (read as flat, not stored).
	FlatCatchUp = time.Hour
	// FlatDecideEvery is how often the symbols getting flat minutes are
	// decided again.
	FlatDecideEvery = 10 * time.Second
)

// FlatPolicy is what the service needs to store flat minutes: whether it
// is known yet which symbols get them and whether a symbol does (asked
// under the service's lock: no I/O), the event of one (queued in the
// trades' transaction), and how many were stored (once it committed).
type FlatPolicy interface {
	Decided() bool
	Own(symbol string) bool
	Emit(ctx context.Context, r ports.Repos, c domain.Candle) error
	Stored(n int)
}

// ErrFlatsUndecided holds the trades back while it is not known which
// symbols get flat minutes (the listing not read since the start, review
// AV): applied without, their quiet minutes would never be stored. The
// batch is tried again.
var ErrFlatsUndecided = errors.New("flat minutes: the symbols that get them are not known before the listing is read; the trades wait")

// StoreFlats has the service store flat minutes as p decides, from the
// next trades it applies.
func (s *Service) StoreFlats(p FlatPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flats = p
}

// fillFlats folds the minutes without a trade between the symbol's last
// one-minute candle and the minute of its trade at at into every interval,
// at the last close, and returns the one-minute ones; touch is given every
// candle changed. The caller holds mu.
func (s *Service) fillFlats(st *symbolState, symbol string, at time.Time, touch func(domain.Candle)) []domain.Candle {
	last, ok := st.current[domain.Minute1]
	if !ok || s.flats == nil || !s.flats.Own(symbol) {
		return nil
	}
	upTo := domain.Minute1.Start(at)
	earliest := upTo.Add(-FlatCatchUp)
	var out []domain.Candle
	for m := domain.Minute1.Next(last.OpenTime); m.Before(upTo); m = domain.Minute1.Next(m) {
		if m.Before(earliest) {
			m = earliest
		}
		for _, i := range domain.Intervals {
			start := i.Start(m)
			c, ok := st.current[i]
			if !ok || start.After(c.OpenTime) {
				c = domain.Candle{Symbol: symbol, Interval: i, OpenTime: start}
			}
			c.AddFlat(last.Close)
			st.current[i] = c
			touch(c)
			if i == domain.Minute1 {
				st.minutes = append(st.minutes, c)
				out = append(out, c)
			}
		}
	}
	return out
}

// FlatMinutes is the flat minutes' policy: the listed pairs and contracts
// no reference market follows while market.flat_minutes is on for them,
// decided every FlatDecideEvery away from the trades; each flat's
// CandleClosed goes on market.candle.flats (analytics writes it to
// candles_1m).
type FlatMinutes struct {
	symbols func(ctx context.Context) ([]string, bool)
	flags   Flags
	events  *event.Factory
	log     *slog.Logger
	made    prometheus.Counter
	own     atomic.Pointer[map[string]bool]
}

// NewFlatMinutes registers its metric with reg; symbols lists the pairs
// and contracts no reference market follows, false while it cannot tell.
func NewFlatMinutes(symbols func(ctx context.Context) ([]string, bool), fl Flags, events *event.Factory, log *slog.Logger,
	reg prometheus.Registerer,
) *FlatMinutes {
	f := &FlatMinutes{
		symbols: symbols, flags: fl, events: events, log: log,
		made: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_flat_minutes_total",
			Help: "One-minute candles stored flat (no trade in the minute) for the symbols charting the platform's own trades.",
		}),
	}
	reg.MustRegister(f.made)
	return f
}

// Decide decides again which symbols get flat minutes; not before the
// listing was read.
func (f *FlatMinutes) Decide(ctx context.Context) {
	symbols, ok := f.symbols(ctx)
	if !ok {
		if !f.Decided() {
			f.log.WarnContext(ctx, "flat minutes: the listing not read yet; the trades wait for it")
		}
		return
	}
	own := map[string]bool{}
	for _, symbol := range symbols {
		if f.flags.Enabled(flags.KeyFlatMinutes, flags.Subject{Symbol: symbol}) {
			own[symbol] = true
		}
	}
	if old := f.own.Swap(&own); old == nil || !maps.Equal(*old, own) {
		f.log.InfoContext(ctx, "flat minutes: symbols decided", "symbols", slices.Sorted(maps.Keys(own)))
	}
}

// Run decides every FlatDecideEvery until ctx ends.
func (f *FlatMinutes) Run(ctx context.Context) error {
	t := time.NewTicker(FlatDecideEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		f.Decide(ctx)
	}
}

// Decided reports whether it was decided which symbols get flat minutes.
func (f *FlatMinutes) Decided() bool { return f.own.Load() != nil }

// Own reports whether symbol gets flat minutes, as last decided.
func (f *FlatMinutes) Own(symbol string) bool {
	own := f.own.Load()
	return own != nil && (*own)[symbol]
}

// Emit queues the flat minute's CandleClosed on market.candle.flats.
func (f *FlatMinutes) Emit(ctx context.Context, r ports.Repos, c domain.Candle) error {
	env, err := f.events.New(ctx, &marketv1.CandleClosed{Candle: candleProto(c, true)}, "symbol", c.Symbol)
	if err != nil {
		return err
	}
	return r.Emit(ctx, event.TopicMarketCandleFlats, env)
}

// Stored counts the flat minutes stored.
func (f *FlatMinutes) Stored(n int) { f.made.Add(float64(n)) }
