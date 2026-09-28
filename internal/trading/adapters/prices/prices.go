// Package prices provides the anchor price of the price band (§11.2).
package prices

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// None has no anchor: limit prices are not banded and market orders carry
// no protection price (tests).
type None struct{}

// Anchor returns zero.
func (None) Anchor(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }

// LastPriceFunc returns the price of a symbol's latest trade, zero for none.
type LastPriceFunc func(ctx context.Context, symbol string) (decimal.Decimal, error)

// LastTrade anchors on the symbol's latest trade, which the service records
// itself from trade.events, cached for ttl. A pair without trades has no
// anchor until reference prices arrive (plan §6.3 task 7).
type LastTrade struct {
	last LastPriceFunc
	ttl  time.Duration
	now  func() time.Time

	mu     sync.Mutex
	cached map[string]cachedPrice
}

type cachedPrice struct {
	price decimal.Decimal
	at    time.Time
}

// NewLastTrade caches last's answers for ttl.
func NewLastTrade(last LastPriceFunc, ttl time.Duration) *LastTrade {
	return &LastTrade{last: last, ttl: ttl, now: time.Now, cached: map[string]cachedPrice{}}
}

// Anchor returns the latest trade price of symbol.
func (l *LastTrade) Anchor(ctx context.Context, symbol string) (decimal.Decimal, error) {
	now := l.now()
	l.mu.Lock()
	c, ok := l.cached[symbol]
	l.mu.Unlock()
	if ok && now.Sub(c.at) < l.ttl {
		return c.price, nil
	}
	price, err := l.last(ctx, symbol)
	if err != nil {
		return decimal.Zero, err
	}
	l.mu.Lock()
	l.cached[symbol] = cachedPrice{price: price, at: now}
	l.mu.Unlock()
	return price, nil
}
