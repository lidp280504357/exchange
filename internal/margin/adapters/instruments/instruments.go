// Package instruments reads asset precision and trading pairs from
// instrument-service.
package instruments

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/skill/exchange/internal/margin/ports"
)

// Client implements ports.Instruments. An asset's decimals never change
// once set (instrument-service refuses it), so they are kept for the life
// of the process; pairs are cached for ttl, so a halt applies within
// seconds.
type Client struct {
	c   instrumentv1.InstrumentServiceClient
	ttl time.Duration

	mu       sync.Mutex
	decimals map[string]int32
	pairs    map[string]cachedPair
}

type cachedPair struct {
	pair ports.PairInfo
	at   time.Time
}

// New wraps an InstrumentService client.
func New(c instrumentv1.InstrumentServiceClient, ttl time.Duration) *Client {
	return &Client{c: c, ttl: ttl, decimals: map[string]int32{}, pairs: map[string]cachedPair{}}
}

// Decimals returns the asset's precision.
func (c *Client) Decimals(ctx context.Context, asset string) (int32, error) {
	c.mu.Lock()
	d, ok := c.decimals[asset]
	c.mu.Unlock()
	if ok {
		return d, nil
	}
	resp, err := c.c.GetAsset(ctx, &instrumentv1.GetAssetRequest{AssetCode: asset})
	if err != nil {
		return 0, err
	}
	d = resp.GetAsset().GetDecimals()
	c.mu.Lock()
	c.decimals[asset] = d
	c.mu.Unlock()
	return d, nil
}

// Pair returns the trading pair.
func (c *Client) Pair(ctx context.Context, symbol string) (ports.PairInfo, error) {
	c.mu.Lock()
	hit, ok := c.pairs[symbol]
	c.mu.Unlock()
	if ok && time.Since(hit.at) < c.ttl {
		return hit.pair, nil
	}
	resp, err := c.c.GetTradingPair(ctx, &instrumentv1.GetTradingPairRequest{Symbol: symbol})
	if err != nil {
		return ports.PairInfo{}, err
	}
	p := resp.GetPair()
	pair := ports.PairInfo{
		Symbol: p.GetSymbol(), Base: p.GetBaseAsset(), Quote: p.GetQuoteAsset(), Status: p.GetStatus(),
		TickDecimals: decimalsOf(p.GetTickSize()), Lot: decimal.Zero,
	}
	if lot, err := decimal.NewFromString(p.GetLotSize()); err == nil {
		pair.Lot = lot
	}
	if fewest, err := decimal.NewFromString(p.GetMinQuantity()); err == nil {
		pair.MinQuantity = fewest
	}
	if most, err := decimal.NewFromString(p.GetMaxQuantity()); err == nil {
		pair.MaxQuantity = most
	}
	c.mu.Lock()
	c.pairs[symbol] = cachedPair{pair: pair, at: time.Now()}
	c.mu.Unlock()
	return pair, nil
}

// decimalsOf is the number of decimals of a step such as "0.01".
func decimalsOf(step string) int32 {
	_, frac, ok := strings.Cut(step, ".")
	if !ok {
		return 0
	}
	return int32(min(len(strings.TrimRight(frac, "0")), 18)) //nolint:gosec // at most 18
}
