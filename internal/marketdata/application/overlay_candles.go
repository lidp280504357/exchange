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
// perpetual's from its index pair while the overlay reaches risk.
func (oc *overlayCandles) factor(symbol string) decimal.Decimal {
	pair := domain.IndexPairOf(symbol)
	if pair != symbol {
		return oc.overlay.RiskFactor(pair)
	}
	f, _ := oc.overlay.Factor(symbol)
	return f
}

// apply is c (a reference 1m candle as it stands) as the platform shows
// it.
func (oc *overlayCandles) apply(c domain.Candle) domain.Candle {
	f := oc.factor(c.Symbol)
	oc.mu.Lock()
	defer oc.mu.Unlock()
	t, ok := oc.open[c.Symbol]
	if ok && !t.minute.Equal(c.OpenTime) {
		delete(oc.open, c.Symbol)
		ok = false
	}
	if !ok {
		if f.Equal(one) {
			return c
		}
		// The minute's prices before the event, or (an event that was
		// already on) the whole minute's, scaled by the factor now.
		was := domain.ScalePrice(c.Open, f)
		t = &touched{
			minute: c.OpenTime, open: was, high: decimal.Max(c.High, domain.ScalePrice(c.High, f)),
			low: decimal.Min(c.Low, domain.ScalePrice(c.Low, f)),
		}
		oc.open[c.Symbol] = t
	}
	p := domain.ScalePrice(c.Close, f)
	t.high, t.low, t.close = decimal.Max(t.high, p), decimal.Min(t.low, p), p
	c.Open, c.High, c.Low, c.Close = t.open, t.high, t.low, t.close
	return c
}
