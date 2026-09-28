// Package instruments tells market-data-service which pairs exist.
package instruments

import (
	"context"
	"slices"
	"sync"
	"time"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
)

// Client implements ports.Pairs from ListTradingPairs, cached for ttl.
type Client struct {
	c   instrumentv1.InstrumentServiceClient
	ttl time.Duration

	mu      sync.Mutex
	symbols []string
	at      time.Time
}

// New wraps an InstrumentService client.
func New(c instrumentv1.InstrumentServiceClient, ttl time.Duration) *Client {
	return &Client{c: c, ttl: ttl}
}

// Symbols lists the pairs that are not delisted, sorted.
func (c *Client) Symbols(ctx context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.symbols != nil && time.Since(c.at) < c.ttl {
		return c.symbols, nil
	}
	resp, err := c.c.ListTradingPairs(ctx, &instrumentv1.ListTradingPairsRequest{})
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, p := range resp.GetPairs() {
		if p.GetStatus() != "DELISTED" {
			out = append(out, p.GetSymbol())
		}
	}
	slices.Sort(out)
	c.symbols, c.at = out, time.Now()
	return out, nil
}

// Listed reports whether symbol is a pair that is not delisted.
func (c *Client) Listed(ctx context.Context, symbol string) (bool, error) {
	list, err := c.Symbols(ctx)
	if err != nil {
		return false, err
	}
	_, found := slices.BinarySearch(list, symbol)
	return found, nil
}
