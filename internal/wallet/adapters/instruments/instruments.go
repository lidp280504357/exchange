// Package instruments reads the deposit networks of assets from
// instrument-service.
package instruments

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

// Client implements ports.Networks. The asset list is cached for TTL, so
// a switch flipped in instrument-service applies within seconds.
type Client struct {
	c   instrumentv1.InstrumentServiceClient
	ttl time.Duration

	mu   sync.Mutex
	at   time.Time
	nets []domain.Network
}

// New wraps an InstrumentService client.
func New(c instrumentv1.InstrumentServiceClient, ttl time.Duration) *Client {
	return &Client{c: c, ttl: ttl}
}

func (c *Client) all(ctx context.Context) ([]domain.Network, error) {
	c.mu.Lock()
	nets, at := c.nets, c.at
	c.mu.Unlock()
	if !at.IsZero() && time.Since(at) < c.ttl {
		return nets, nil
	}
	resp, err := c.c.ListAssets(ctx, &instrumentv1.ListAssetsRequest{})
	if err != nil {
		return nil, err
	}
	nets = nets[:0:0]
	for _, a := range resp.GetAssets() {
		for _, n := range a.GetNetworks() {
			minDeposit, err := decimal.NewFromString(n.GetMinDeposit())
			if err != nil {
				return nil, fmt.Errorf("network %s/%s: bad min_deposit %q: %w", a.GetAssetCode(), n.GetNetwork(), n.GetMinDeposit(), err)
			}
			nets = append(nets, domain.Network{
				Asset: a.GetAssetCode(), Network: n.GetNetwork(), Chain: n.GetChain(), Contract: strings.ToLower(n.GetContractAddress()),
				Decimals: a.GetDecimals(), Confirmations: uint32(max(n.GetConfirmations(), 0)), MinDeposit: minDeposit,
				Enabled: a.GetDepositEnabled() && n.GetDepositEnabled(),
			})
		}
	}
	c.mu.Lock()
	c.nets, c.at = nets, time.Now()
	c.mu.Unlock()
	return nets, nil
}

// Network returns an asset's network.
func (c *Client) Network(ctx context.Context, asset, network string) (domain.Network, error) {
	nets, err := c.all(ctx)
	if err != nil {
		return domain.Network{}, err
	}
	for _, n := range nets {
		if n.Asset == asset && n.Network == network {
			return n, nil
		}
	}
	return domain.Network{}, domain.ErrUnknownNetwork
}

// OnNetwork lists the assets configured on a network, taking deposits or
// not.
func (c *Client) OnNetwork(ctx context.Context, network string) ([]domain.Network, error) {
	nets, err := c.all(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.Network
	for _, n := range nets {
		if n.Network == network {
			out = append(out, n)
		}
	}
	return out, nil
}
