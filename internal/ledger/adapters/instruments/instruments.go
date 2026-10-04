// Package instruments reads asset precision from instrument-service.
package instruments

import (
	"context"
	"sync"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
)

// Client implements ports.Assets. An asset's decimals never change once
// set (instrument-service refuses it), so known assets are cached for the
// life of the process.
type Client struct {
	c     instrumentv1.InstrumentServiceClient
	mu    sync.RWMutex
	known map[string]int32
}

// New wraps an InstrumentService client.
func New(c instrumentv1.InstrumentServiceClient) *Client {
	return &Client{c: c, known: map[string]int32{}}
}

// Decimals returns the asset's precision.
func (c *Client) Decimals(ctx context.Context, asset string) (int32, error) {
	c.mu.RLock()
	d, ok := c.known[asset]
	c.mu.RUnlock()
	if ok {
		return d, nil
	}
	resp, err := c.c.GetAsset(ctx, &instrumentv1.GetAssetRequest{AssetCode: asset})
	if err != nil {
		return 0, err
	}
	d = resp.GetAsset().GetDecimals()
	c.mu.Lock()
	c.known[asset] = d
	c.mu.Unlock()
	return d, nil
}

// Backed lists the assets with a network to deposit or withdraw them on,
// enabled or not: the assets HOUSE must hold to sell (ADR-0013).
func (c *Client) Backed(ctx context.Context) ([]string, error) {
	resp, err := c.c.ListAssets(ctx, &instrumentv1.ListAssetsRequest{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range resp.GetAssets() {
		if len(a.GetNetworks()) > 0 {
			out = append(out, a.GetAssetCode())
		}
	}
	return out, nil
}
