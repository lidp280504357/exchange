// Package instruments reads trading pairs and their assets from
// instrument-service.
package instruments

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/trading/domain"
)

// Client implements ports.Instruments. Pairs are cached for TTL: every
// order needs one, and a status change (a halt) still applies within
// seconds.
type Client struct {
	c   instrumentv1.InstrumentServiceClient
	ttl time.Duration

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	pair domain.Pair
	at   time.Time
}

// New wraps an InstrumentService client.
func New(c instrumentv1.InstrumentServiceClient, ttl time.Duration) *Client {
	return &Client{c: c, ttl: ttl, cache: map[string]cached{}}
}

// Pair returns the trading pair with its assets' decimals and switches.
func (c *Client) Pair(ctx context.Context, symbol string) (domain.Pair, error) {
	c.mu.Lock()
	hit, ok := c.cache[symbol]
	c.mu.Unlock()
	if ok && time.Since(hit.at) < c.ttl {
		return hit.pair, nil
	}
	resp, err := c.c.GetTradingPair(ctx, &instrumentv1.GetTradingPairRequest{Symbol: symbol})
	if err != nil {
		return domain.Pair{}, err
	}
	p := resp.GetPair()
	base, err := c.c.GetAsset(ctx, &instrumentv1.GetAssetRequest{AssetCode: p.GetBaseAsset()})
	if err != nil {
		return domain.Pair{}, err
	}
	quote, err := c.c.GetAsset(ctx, &instrumentv1.GetAssetRequest{AssetCode: p.GetQuoteAsset()})
	if err != nil {
		return domain.Pair{}, err
	}
	pair := domain.Pair{
		Symbol: p.GetSymbol(), Base: p.GetBaseAsset(), Quote: p.GetQuoteAsset(), Status: p.GetStatus(),
		BaseDecimals: base.GetAsset().GetDecimals(), QuoteDecimals: quote.GetAsset().GetDecimals(),
		Tradable: tradable(base.GetAsset()) && tradable(quote.GetAsset()),
	}
	for _, f := range []struct {
		dst *decimal.Decimal
		src string
	}{
		{&pair.TickSize, p.GetTickSize()},
		{&pair.LotSize, p.GetLotSize()},
		{&pair.MinQuantity, p.GetMinQuantity()},
		{&pair.MaxQuantity, p.GetMaxQuantity()},
		{&pair.MinNotional, p.GetMinNotional()},
		{&pair.PriceBand, p.GetPriceBand()},
		{&pair.MakerFeeRate, p.GetMakerFeeRate()},
		{&pair.TakerFeeRate, p.GetTakerFeeRate()},
	} {
		v, err := decimal.NewFromString(f.src)
		if err != nil {
			return domain.Pair{}, fmt.Errorf("pair %s: bad decimal %q: %w", symbol, f.src, err)
		}
		*f.dst = v
	}
	c.mu.Lock()
	c.cache[symbol] = cached{pair: pair, at: time.Now()}
	c.mu.Unlock()
	return pair, nil
}

func tradable(a *instrumentv1.Asset) bool { return a.GetTradingEnabled() && !a.GetRiskRestricted() }
