package application

import (
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
)

// overlayCandles keeps the reference 1m candles a price event touched
// (design 2026-10-07, general price control): such a minute's prices
// follow the scaled closes as they come (about each second), so the spike
// stays in the stored candle; a minute no event touched is the reference
// market's as it is.
type overlayCandles struct {
	overlay *Overlay

	mu   sync.Mutex
	open map[string]*touched
}

// touched is a minute a factor other than 1 reached.
type touched struct {
	minute                 time.Time
	open, high, low, close decimal.Decimal
}

func newOverlayCandles(o *Overlay) *overlayCandles {
	return &overlayCandles{overlay: o, open: map[string]*touched{}}
}

// factor is the overlay's factor on symbol's candles: a pair's own, a
// perpetual's from its index pair while the overlay reaches risk; and
// when the event began.
func (oc *overlayCandles) factor(symbol string) (decimal.Decimal, time.Time) {
	pair := domain.IndexPairOf(symbol)
	f := oc.overlay.RiskFactor(pair)
	if pair == symbol {
		f, _ = oc.overlay.Factor(symbol)
	}
	since, _ := oc.overlay.Since(pair)
	return f, since
}

// apply is c (a reference 1m candle as it stands) as the platform shows
// it, and whether a price event touched its minute (then stored as such:
// the charts lay it over the reference market's candles). The minute an
// event begins in keeps the reference market's prices before it (its
// open, high and low); a minute that began within the event has the
// scaled prices only - the reference market's own range would show a wick
// back to it in every such minute (review C57 ①). When the event began
// is the overlay's to tell (the event's own start, across a restart of
// either service; review C58 ②).
func (oc *overlayCandles) apply(c domain.Candle) (domain.Candle, bool) {
	f, since := oc.factor(c.Symbol)
	tick := oc.overlay.Tick(c.Symbol)
	oc.mu.Lock()
	defer oc.mu.Unlock()
	t, ok := oc.open[c.Symbol]
	if ok && !t.minute.Equal(c.OpenTime) {
		delete(oc.open, c.Symbol)
		ok = false
	}
	if !ok {
		if f.Equal(one) {
			return c, false
		}
		t = &touched{minute: c.OpenTime, open: c.Open, high: c.High, low: c.Low}
		if !since.IsZero() && !since.After(c.OpenTime) {
			t.open = domain.ScalePrice(c.Open, f, tick)
			t.high, t.low = domain.ScalePrice(c.High, f, tick), domain.ScalePrice(c.Low, f, tick)
		}
		oc.open[c.Symbol] = t
	}
	p := domain.ScalePrice(c.Close, f, tick)
	t.high, t.low, t.close = decimal.Max(t.high, p), decimal.Min(t.low, p), p
	c.Open, c.High, c.Low, c.Close = t.open, t.high, t.low, t.close
	return c, true
}
