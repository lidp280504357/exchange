package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

// Flat minutes (coordinator 2026-10-04: the platform coin's 1-minute
// candles broke up wherever a minute had no trade). For a symbol whose
// chart is the platform's own trades (no reference market shown), a minute
// that ended without a trade becomes a stored candle of its own: open,
// high, low and close the previous close, no volume and no trades. Every
// longer interval takes it as a trade of nothing at that price, as rolling
// up the one-minute candles does in ClickHouse's candles(): its open is
// that price when its first minute was flat, and its high and low count
// it. The publisher then closes the flat minute like any other, and an
// event on market.candle.flats has analytics store it in candles_1m.
const (
	// FlatGrace is how long after a minute ends its flat waits for a late
	// trade: one stamped in the minute but arriving after its flat counts
	// in the next minute here (candles never reopen) and in its own in
	// ClickHouse, where the trade's candle replaces the flat.
	FlatGrace = 10 * time.Second
	// FlatCatchUp is how far back flats are made, after a restart say:
	// forward only, never the history before they existed.
	FlatCatchUp = time.Hour
)

// FlatMinutes stores a flat one-minute candle for every minute that ended
// FlatGrace or more before now without a trade, for the symbols platform
// says chart the platform's trades, with each longer interval taking it
// in; emit queues each flat's event in the same transaction. It returns
// the flat minutes stored. A symbol that never traded has none.
func (s *Service) FlatMinutes(ctx context.Context, now time.Time, platform func(symbol string) bool,
	emit func(ctx context.Context, r ports.Repos, c domain.Candle) error,
) ([]domain.Candle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		if err := s.load(ctx); err != nil {
			return nil, err
		}
	}
	end := domain.Minute1.Start(now.Add(-FlatGrace)) // minutes opening before it have ended FlatGrace ago
	earliest := domain.Minute1.Start(now.Add(-FlatCatchUp))
	var flats, touched []domain.Candle
	for symbol, st := range s.symbols {
		last, ok := st.current[domain.Minute1]
		if !ok || !platform(symbol) {
			continue
		}
		price := last.Close
		for m := domain.Minute1.Next(last.OpenTime); m.Before(end); m = domain.Minute1.Next(m) {
			if m.Before(earliest) {
				m = earliest
			}
			for _, i := range domain.Intervals {
				start := i.Start(m)
				c, ok := st.current[i]
				if !ok || start.After(c.OpenTime) {
					c = domain.Candle{Symbol: symbol, Interval: i, OpenTime: start}
				}
				c.AddFlat(price)
				st.current[i] = c
				st.updated[i] = true
				if i == domain.Minute1 {
					st.minutes = append(st.minutes, c)
					flats = append(flats, c)
				}
				touched = append(touched, c)
			}
		}
	}
	if len(flats) == 0 {
		return nil, nil
	}
	// The same candle of a longer interval, taken in by several minutes,
	// is stored as it ended up.
	latest := map[domain.Interval]map[string]map[time.Time]domain.Candle{}
	for _, c := range touched {
		if latest[c.Interval] == nil {
			latest[c.Interval] = map[string]map[time.Time]domain.Candle{}
		}
		if latest[c.Interval][c.Symbol] == nil {
			latest[c.Interval][c.Symbol] = map[time.Time]domain.Candle{}
		}
		latest[c.Interval][c.Symbol][c.OpenTime] = c
	}
	var candles []domain.Candle
	for _, bySymbol := range latest {
		for _, byOpen := range bySymbol {
			for _, c := range byOpen {
				candles = append(candles, c)
			}
		}
	}
	err := s.store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Candles().Upsert(ctx, candles); err != nil {
			return err
		}
		for _, c := range flats {
			if err := emit(ctx, r, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.dirty = true
		return nil, err
	}
	return flats, nil
}

// FlatRunner stores the flat minutes every second and queues their
// events on market.candle.flats (analytics writes them to candles_1m).
type FlatRunner struct {
	svc      *Service
	events   *event.Factory
	platform func(ctx context.Context, symbol string) bool
	log      *slog.Logger
	made     prometheus.Counter
}

// NewFlatRunner registers its metric with reg; platform says whether a
// symbol's chart is the platform's trades (no reference market shown).
func NewFlatRunner(svc *Service, events *event.Factory, platform func(ctx context.Context, symbol string) bool, log *slog.Logger,
	reg prometheus.Registerer,
) *FlatRunner {
	f := &FlatRunner{
		svc: svc, events: events, platform: platform, log: log,
		made: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_flat_minutes_total",
			Help: "One-minute candles stored flat (no trade in the minute) for the symbols charting the platform's own trades.",
		}),
	}
	reg.MustRegister(f.made)
	return f
}

// Run stores the flat minutes every second until ctx ends.
func (f *FlatRunner) Run(ctx context.Context) error {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		flats, err := f.svc.FlatMinutes(ctx, f.svc.now(), func(symbol string) bool { return f.platform(ctx, symbol) }, f.emit)
		if err != nil {
			f.log.WarnContext(ctx, "flat minutes not stored: tried again in a second", "error", err)
			continue
		}
		f.made.Add(float64(len(flats)))
	}
}

func (f *FlatRunner) emit(ctx context.Context, r ports.Repos, c domain.Candle) error {
	env, err := f.events.New(ctx, &marketv1.CandleClosed{Candle: candleProto(c, true)}, "symbol", c.Symbol)
	if err != nil {
		return err
	}
	return r.Emit(ctx, event.TopicMarketCandleFlats, env)
}
