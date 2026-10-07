// Package prices provides the anchor price of the price band (§11.2).
package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// None has no anchor: limit prices are not banded and market orders carry
// no protection price (tests).
type None struct{}

// Anchor returns zero.
func (None) Anchor(context.Context, string, bool) (decimal.Decimal, error) { return decimal.Zero, nil }

// LastTradeFunc returns the price and time of a symbol's latest trade,
// zero for none.
type LastTradeFunc func(ctx context.Context, symbol string) (decimal.Decimal, time.Time, error)

// ReferenceFunc returns a symbol's fresh reference price, zero for none.
type ReferenceFunc func(ctx context.Context, symbol string) (decimal.Decimal, error)

// RecentTrade is how old the last trade of a pair without a reference
// market may be and still anchor its band: an older one gives way to the
// fresh price market-data-service keeps for it (the platform's own, its
// book's mid, market-sim's target).
const RecentTrade = 5 * time.Minute

// LastTrade anchors the band (§11.2) of a pair that follows a reference
// market on its fresh reference price: HOUSE quotes around it (ADR-0015),
// and a price event moves it at once (the general price control's overlay,
// J1), while the platform's own trades only follow once one fills at the
// new level: anchored on the last trade, a limit order 16% up was refused
// as out of the band and a market buy's protection price could not reach
// HOUSE's asks (review B144). Without a fresh reference (stale, or
// unreachable) its latest trade anchors, however old. A pair without a
// reference market (the platform coin) anchors on its latest trade while
// it is at most RecentTrade old, then on the price market-data-service
// keeps for it, as market-sim's quotes do (its band.go; review B150: a 60 s
// TWAP anchor left the bots' quotes out of the band while the price
// walked). The trades are the ones the service records itself from
// trade.events. Answers are cached for ttl.
type LastTrade struct {
	last      LastTradeFunc
	reference ReferenceFunc
	ttl       time.Duration
	now       func() time.Time

	mu     sync.Mutex
	cached map[string]cachedPrice
}

type cachedPrice struct {
	price decimal.Decimal
	at    time.Time
}

// NewLastTrade caches the answers of last, and of reference (nil: none),
// for ttl.
func NewLastTrade(last LastTradeFunc, reference ReferenceFunc, ttl time.Duration) *LastTrade {
	return &LastTrade{last: last, reference: reference, ttl: ttl, now: time.Now, cached: map[string]cachedPrice{}}
}

// Anchor returns the anchor price of symbol, followed when the pair
// follows a reference market (zero for none).
func (l *LastTrade) Anchor(ctx context.Context, symbol string, followed bool) (decimal.Decimal, error) {
	now := l.now()
	l.mu.Lock()
	c, ok := l.cached[symbol]
	l.mu.Unlock()
	if ok && now.Sub(c.at) < l.ttl {
		return c.price, nil
	}
	// reference is the fresh reference price, zero for none; an
	// unreachable reference leaves the last trade, or no band.
	reference := func() decimal.Decimal {
		if l.reference != nil {
			if ref, err := l.reference(ctx, symbol); err == nil && ref.IsPositive() {
				return ref
			}
		}
		return decimal.Zero
	}
	var price decimal.Decimal
	if followed {
		price = reference()
	}
	if price.IsZero() {
		last, at, err := l.last(ctx, symbol)
		if err != nil {
			return decimal.Zero, err
		}
		price = last
		if !followed && (last.IsZero() || now.Sub(at) > RecentTrade) {
			if ref := reference(); ref.IsPositive() {
				price = ref
			}
		}
	}
	l.mu.Lock()
	l.cached[symbol] = cachedPrice{price: price, at: now}
	l.mu.Unlock()
	return price, nil
}

// ReferenceClient reads the reference price that market-data-service
// serves internally; a stale or missing one is zero.
type ReferenceClient struct {
	Base   string
	Client *http.Client
}

// Price returns the fresh reference price of symbol, or zero.
func (c ReferenceClient) Price(ctx context.Context, symbol string) (decimal.Decimal, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/internal/market/"+url.PathEscape(symbol)+"/reference", nil)
	if err != nil {
		return decimal.Zero, err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return decimal.Zero, fmt.Errorf("reference price: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, fmt.Errorf("reference price: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Price *string `json:"price"`
		Fresh bool    `json:"fresh"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return decimal.Zero, fmt.Errorf("reference price: %w", err)
	}
	if !body.Fresh || body.Price == nil {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(*body.Price)
}
