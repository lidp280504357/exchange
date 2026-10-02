// Package application builds the platform market data (requirements
// §5.11, §11.8): candles of every interval and the recent trades from
// trade.events, the latest depth from market.depth, and the rolling
// ticker; it answers the public REST queries and pushes candle and ticker
// updates to market.candle.events for the WebSocket gateway.
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
)

// Limits of the queries and of what is kept in memory.
const (
	RecentTrades  = 100
	MaxDepth      = 200
	MaxCandles    = 1000
	DefaultLimit  = 500
	TradeKeepDays = 7
)

// ErrUnknownSymbol is returned for a symbol that is not a listed pair or
// contract.
var ErrUnknownSymbol = apperr.NotFound("no such trading pair or contract")

// Service holds the market state of every symbol in memory, backed by the
// store; it is safe for concurrent use.
type Service struct {
	store ports.Store
	pairs ports.Instruments
	log   *slog.Logger
	now   func() time.Time

	mu      sync.Mutex
	symbols map[string]*symbolState
	// dirty means memory may be ahead of the store (a failed write): the
	// state is reloaded before the next batch, which Kafka redelivers.
	dirty bool
}

type symbolState struct {
	seq    int64
	last   decimal.Decimal
	lastAt time.Time
	// current is the latest candle of each interval that had trades.
	current map[domain.Interval]domain.Candle
	// minutes are the 1m candles with trades from the ticker window on,
	// oldest first; before is the latest one older than the window.
	minutes []domain.Candle
	before  *domain.Candle
	// trades are the latest trades, oldest first.
	trades []domain.Trade
	depth  *marketv1.DepthSnapshot

	// What the publisher pushed last.
	updated        map[domain.Interval]bool
	pushedClosed   map[domain.Interval]time.Time
	pushedFlat     map[domain.Interval]time.Time
	pushedTicker   *domain.Ticker
	pushedTickerAt time.Time
}

func newSymbolState() *symbolState {
	return &symbolState{
		current: map[domain.Interval]domain.Candle{}, updated: map[domain.Interval]bool{},
		pushedClosed: map[domain.Interval]time.Time{}, pushedFlat: map[domain.Interval]time.Time{},
	}
}

// New returns a service; call Load before use.
func New(store ports.Store, pairs ports.Instruments, log *slog.Logger) *Service {
	return &Service{store: store, pairs: pairs, log: log, now: time.Now, symbols: map[string]*symbolState{}, dirty: true}
}

// Load reads the state of every symbol from the store, keeping the depth
// already received.
func (s *Service) Load(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

func (s *Service) load(ctx context.Context) error {
	r := s.store.Read()
	states, err := r.Symbols().All(ctx)
	if err != nil {
		return err
	}
	windowStart := domain.WindowStart(s.now())
	loaded := make(map[string]*symbolState, len(states))
	for _, st := range states {
		m := newSymbolState()
		m.seq, m.last, m.lastAt = st.Sequence, st.LastPrice, st.LastAt
		latest, err := r.Candles().Latest(ctx, st.Symbol)
		if err != nil {
			return err
		}
		for _, c := range latest {
			m.current[c.Interval] = c
		}
		if m.minutes, err = r.Candles().Range(ctx, st.Symbol, domain.Minute1, windowStart, windowStart.Add(2*domain.Window)); err != nil {
			return err
		}
		if m.before, err = r.Candles().Before(ctx, st.Symbol, domain.Minute1, windowStart); err != nil {
			return err
		}
		recent, err := r.Trades().Recent(ctx, st.Symbol, RecentTrades)
		if err != nil {
			return err
		}
		slices.Reverse(recent)
		m.trades = recent
		if old, ok := s.symbols[st.Symbol]; ok {
			m.depth = old.depth
		}
		loaded[st.Symbol] = m
	}
	for symbol, old := range s.symbols {
		if _, ok := loaded[symbol]; !ok && old.depth != nil {
			m := newSymbolState()
			m.depth = old.depth
			loaded[symbol] = m
		}
	}
	s.symbols, s.dirty = loaded, false
	s.log.InfoContext(ctx, "market state loaded", "symbols", len(states))
	return nil
}

func (s *Service) state(symbol string) *symbolState {
	st, ok := s.symbols[symbol]
	if !ok {
		st = newSymbolState()
		s.symbols[symbol] = st
	}
	return st
}

// OnTrades applies trades in the order given (each symbol's in sequence
// order, as its partition delivers them) and stores the result in one
// transaction. Trades at or below a symbol's applied sequence are skipped;
// it returns the others, which nothing had seen before.
func (s *Service) OnTrades(ctx context.Context, trades []domain.Trade) ([]domain.Trade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		if err := s.load(ctx); err != nil {
			return nil, err
		}
	}
	type key struct {
		symbol   string
		interval domain.Interval
		open     time.Time
	}
	touched := map[key]domain.Candle{}
	var fresh []domain.Trade
	changed := map[string]*symbolState{}
	for _, t := range trades {
		st := s.state(t.Symbol)
		if t.Sequence <= st.seq {
			continue
		}
		for _, i := range domain.Intervals {
			start := i.Start(t.At)
			c, ok := st.current[i]
			// A trade stamped before the open candle (clock skew between
			// engine instances) still counts in it; candles never reopen.
			if !ok || start.After(c.OpenTime) {
				c = domain.Candle{Symbol: t.Symbol, Interval: i, OpenTime: start}
			}
			c.Add(t.Price, t.Quantity, t.Quote)
			st.current[i] = c
			st.updated[i] = true
			touched[key{t.Symbol, i, c.OpenTime}] = c
			if i == domain.Minute1 {
				if n := len(st.minutes); n > 0 && st.minutes[n-1].OpenTime.Equal(c.OpenTime) {
					st.minutes[n-1] = c
				} else {
					st.minutes = append(st.minutes, c)
				}
			}
		}
		st.seq, st.last, st.lastAt = t.Sequence, t.Price, t.At
		st.trades = append(st.trades, t)
		if len(st.trades) > RecentTrades {
			st.trades = slices.Clone(st.trades[len(st.trades)-RecentTrades:])
		}
		fresh = append(fresh, t)
		changed[t.Symbol] = st
	}
	if len(fresh) == 0 {
		return nil, nil
	}
	candles := make([]domain.Candle, 0, len(touched))
	for _, c := range touched {
		candles = append(candles, c)
	}
	err := s.store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Candles().Upsert(ctx, candles); err != nil {
			return err
		}
		if err := r.Trades().Insert(ctx, fresh); err != nil {
			return err
		}
		for symbol, st := range changed {
			if err := r.Symbols().Save(ctx, ports.SymbolState{Symbol: symbol, Sequence: st.seq, LastPrice: st.last, LastAt: st.lastAt}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.dirty = true
		return nil, err
	}
	return fresh, nil
}

// OnDepth keeps a symbol's latest depth; an older snapshot (a redelivery)
// is ignored.
func (s *Service) OnDepth(d *marketv1.DepthSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(d.GetSymbol())
	if st.depth == nil || d.GetSequence() >= st.depth.GetSequence() {
		st.depth = d
	}
}

// PurgeTrades deletes stored trades older than the retention.
func (s *Service) PurgeTrades(ctx context.Context) (int64, error) {
	return s.store.Read().Trades().Purge(ctx, s.now().AddDate(0, 0, -TradeKeepDays))
}

func (s *Service) checkListed(ctx context.Context, symbol string) error {
	ok, err := s.pairs.Listed(ctx, symbol)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnknownSymbol
	}
	return nil
}

// Ticker returns the symbol's rolling 24-hour ticker.
func (s *Service) Ticker(ctx context.Context, symbol string) (domain.Ticker, error) {
	if err := s.checkListed(ctx, symbol); err != nil {
		return domain.Ticker{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticker(symbol, s.now()), nil
}

// Tickers returns the tickers of every listed pair and contract.
func (s *Service) Tickers(ctx context.Context) ([]domain.Ticker, error) {
	symbols, err := s.pairs.Symbols(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := make([]domain.Ticker, 0, len(symbols))
	for _, symbol := range symbols {
		out = append(out, s.ticker(symbol, now))
	}
	return out, nil
}

// ticker computes a ticker under s.mu, moving 1m candles that left the
// window out of it.
func (s *Service) ticker(symbol string, now time.Time) domain.Ticker {
	st, ok := s.symbols[symbol]
	if !ok {
		return domain.Ticker{Symbol: symbol}
	}
	start := domain.WindowStart(now)
	n := 0
	for n < len(st.minutes) && st.minutes[n].OpenTime.Before(start) {
		n++
	}
	if n > 0 {
		c := st.minutes[n-1]
		st.before = &c
		st.minutes = slices.Clone(st.minutes[n:])
	}
	var bid, ask decimal.Decimal
	if st.depth != nil {
		if b := st.depth.GetBids(); len(b) > 0 {
			bid, _ = decimal.NewFromString(b[0].GetPrice())
		}
		if a := st.depth.GetAsks(); len(a) > 0 {
			ask, _ = decimal.NewFromString(a[0].GetPrice())
		}
	}
	return domain.ComputeTicker(symbol, st.minutes, st.before, st.last, bid, ask)
}

// Depth returns up to limit levels per side of the symbol's latest depth;
// an empty book before the engine's first snapshot.
func (s *Service) Depth(ctx context.Context, symbol string, limit int) (*marketv1.DepthSnapshot, error) {
	if err := s.checkListed(ctx, symbol); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxDepth {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.symbols[symbol]
	if !ok || st.depth == nil {
		return &marketv1.DepthSnapshot{Symbol: symbol}, nil
	}
	d := st.depth
	return &marketv1.DepthSnapshot{
		Symbol: d.GetSymbol(), Sequence: d.GetSequence(), TakenAt: d.GetTakenAt(),
		Bids: d.GetBids()[:min(limit, len(d.GetBids()))], Asks: d.GetAsks()[:min(limit, len(d.GetAsks()))],
	}, nil
}

// Trades returns up to limit of the symbol's latest trades, newest first.
func (s *Service) Trades(ctx context.Context, symbol string, limit int) ([]domain.Trade, error) {
	if err := s.checkListed(ctx, symbol); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > RecentTrades {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.symbols[symbol]
	if !ok {
		return []domain.Trade{}, nil
	}
	out := make([]domain.Trade, 0, min(limit, len(st.trades)))
	for i := len(st.trades) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, st.trades[i])
	}
	return out, nil
}

// Candles returns up to limit candles of interval opening in [from, to),
// gaps filled (domain.Fill). A zero to means now; a zero from means limit
// intervals before to.
func (s *Service) Candles(ctx context.Context, symbol, interval string, from, to time.Time, limit int) ([]domain.Candle, error) {
	i, ok := domain.ParseInterval(interval)
	if !ok {
		return nil, apperr.Invalid(fmt.Sprintf("interval must be one of %v", domain.Intervals))
	}
	if err := s.checkListed(ctx, symbol); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxCandles {
		limit = DefaultLimit
	}
	if to.IsZero() {
		to = s.now()
	}
	// Only the latest limit intervals opening before to can be returned:
	// start there (to at an open time pages back by exactly limit).
	earliest := i.Start(to.Add(-time.Nanosecond))
	for range limit - 1 {
		earliest = previous(i, earliest)
	}
	if from.Before(earliest) {
		from = earliest
	}
	if !from.Before(to) {
		return []domain.Candle{}, nil
	}
	r := s.store.Read().Candles()
	before, err := r.Before(ctx, symbol, i, i.Start(from))
	if err != nil {
		return nil, err
	}
	stored, err := r.Range(ctx, symbol, i, i.Start(from), to)
	if err != nil {
		return nil, err
	}
	return domain.Fill(symbol, i, from, to, before, stored, limit), nil
}

// previous returns the open time of the interval before the one opening at
// start.
func previous(i domain.Interval, start time.Time) time.Time {
	switch i {
	case domain.Month1:
		return start.AddDate(0, -1, 0)
	case domain.Week1:
		return start.AddDate(0, 0, -7)
	}
	return i.Start(start.Add(-time.Nanosecond))
}

// Book returns the levels of the symbol's latest depth, best first; none
// before the engine's first snapshot.
func (s *Service) Book(symbol string) (bids, asks []domain.Level) {
	s.mu.Lock()
	var d *marketv1.DepthSnapshot
	if st, ok := s.symbols[symbol]; ok {
		d = st.depth // snapshots are replaced, never changed
	}
	s.mu.Unlock()
	if d == nil {
		return nil, nil
	}
	return bookLevels(d.GetBids()), bookLevels(d.GetAsks())
}

func bookLevels(in []*marketv1.PriceLevel) []domain.Level {
	out := make([]domain.Level, 0, len(in))
	for _, l := range in {
		p, err1 := decimal.NewFromString(l.GetPrice())
		q, err2 := decimal.NewFromString(l.GetQuantity())
		if err1 == nil && err2 == nil {
			out = append(out, domain.Level{Price: p, Quantity: q})
		}
	}
	return out
}
