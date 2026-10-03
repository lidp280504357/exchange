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
			var amounts [3]decimal.Decimal
			for i, f := range []struct{ name, value string }{
				{"min_deposit", n.GetMinDeposit()}, {"min_withdraw", n.GetMinWithdraw()}, {"withdraw_fee", n.GetWithdrawFee()},
			} {
				v, err := decimal.NewFromString(f.value)
				if err != nil {
					return nil, fmt.Errorf("network %s/%s: bad %s %q: %w", a.GetAssetCode(), n.GetNetwork(), f.name, f.value, err)
				}
				amounts[i] = v
			}
			// EVM contracts are compared in lower case; other chains' are
			// case-sensitive (TRON's Base58).
			contract := n.GetContractAddress()
			if f := n.GetAddressFormat(); f == "" || f == domain.FormatEVM {
				contract = strings.ToLower(contract)
			}
			nets = append(nets, domain.Network{
				Asset: a.GetAssetCode(), Network: n.GetNetwork(), Chain: n.GetChain(), Contract: contract,
				Decimals: a.GetDecimals(), Confirmations: uint32(max(n.GetConfirmations(), 0)), //nolint:gosec // non-negative
				MinDeposit: amounts[0], Enabled: a.GetDepositEnabled() && n.GetDepositEnabled(),
				WithdrawEnabled: a.GetWithdrawEnabled() && n.GetWithdrawEnabled(), MinWithdraw: amounts[1], WithdrawFee: amounts[2],
				MemoRequired: n.GetMemoRequired(), DisplayName: n.GetDisplayName(), AddressFormat: n.GetAddressFormat(),
				ETAMinutes: n.GetEtaMinutes(), ExplorerTxURL: n.GetExplorerTxUrl(), ExplorerAddressURL: n.GetExplorerAddressUrl(),
				Provider: n.GetProvider(), ProviderCoin: n.GetProviderCoin(), Hidden: a.GetHidden(),
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

// ForAsset lists an asset's networks, every one when asset is empty.
func (c *Client) ForAsset(ctx context.Context, asset string) ([]domain.Network, error) {
	nets, err := c.all(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.Network
	for _, n := range nets {
		if asset == "" || n.Asset == asset {
			out = append(out, n)
		}
	}
	return out, nil
}
