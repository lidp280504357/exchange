package application

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// SourceSimulation names the price the simulated market reports for a
// pair as a reference source.
const SourceSimulation = "simulation"

// SimulatedPriceAge is how long a price the simulated market reported
// stands in for its pair's reference.
const SimulatedPriceAge = 30 * time.Second

// ErrFollowed refuses a simulated price for anything but a listed pair the
// reference market does not follow.
var ErrFollowed = apperr.New(apperr.KindConflict, "MARKET_NOT_SIMULATED",
	"only a listed pair that follows no reference market takes a simulated price")

// PlatformReference is the reference price of a listed pair the reference
// market does not follow (the platform coin's ASTRA-USDT, ASTRA design §4):
// the pair's own market (PlatformPrice, its index price; else the middle
// of its book, PlatformMid), else the target the simulated market reported
// in the last SimulatedPriceAge. The trading service anchors such a pair's
// price band on it once the pair has gone five minutes without a trade, so
// that an old trade cannot lock the market.
type PlatformReference struct {
	Svc  *Service
	Refs *ReferenceMap
	Now  func() time.Time

	mu        sync.Mutex
	simulated map[string]Reference
}

func (p *PlatformReference) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Report keeps the simulated market's price of symbol.
func (p *PlatformReference) Report(ctx context.Context, symbol string, price decimal.Decimal) error {
	if !price.IsPositive() {
		return apperr.Invalid("price must be positive")
	}
	if !p.Refs.Unfollowed(ctx, symbol) {
		return ErrFollowed
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.simulated == nil {
		p.simulated = map[string]Reference{}
	}
	p.simulated[symbol] = Reference{Symbol: symbol, Source: SourceSimulation, Price: price, At: p.now()}
	return nil
}

// Reports returns when the simulated market last reported each pair: its
// heartbeat (SimGuard).
func (p *PlatformReference) Reports() map[string]time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]time.Time, len(p.simulated))
	for symbol, r := range p.simulated {
		out[symbol] = r.At
	}
	return out
}

// Price returns the reference of symbol, false when a reference market
// follows it or nothing prices it.
func (p *PlatformReference) Price(ctx context.Context, symbol string) (Reference, bool) {
	if !p.Refs.Unfollowed(ctx, symbol) {
		return Reference{}, false
	}
	if price, ok := p.Svc.PlatformPrice(symbol); ok {
		return Reference{Symbol: symbol, Source: SourcePlatform, Price: price, At: p.now()}, true
	}
	if price, ok := p.Svc.PlatformMid(symbol); ok {
		return Reference{Symbol: symbol, Source: SourcePlatform, Price: price, At: p.now()}, true
	}
	p.mu.Lock()
	r, ok := p.simulated[symbol]
	p.mu.Unlock()
	if ok && p.now().Sub(r.At) <= SimulatedPriceAge {
		return r, true
	}
	return Reference{}, false
}
