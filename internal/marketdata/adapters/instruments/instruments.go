// Package instruments tells market-data-service which pairs and contracts
// exist.
package instruments

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
)

// Client implements ports.Instruments from ListTradingPairs and
// ListContracts, cached for ttl.
type Client struct {
	c   instrumentv1.InstrumentServiceClient
	ttl time.Duration

	mu        sync.Mutex
	symbols   []string
	contracts []ports.Contract
	at        time.Time
}

// New wraps an InstrumentService client.
func New(c instrumentv1.InstrumentServiceClient, ttl time.Duration) *Client {
	return &Client{c: c, ttl: ttl}
}

// refresh reloads both lists when the cache is older than ttl; c.mu is
// held.
func (c *Client) refresh(ctx context.Context) error {
	if c.symbols != nil && time.Since(c.at) < c.ttl {
		return nil
	}
	pairs, err := c.c.ListTradingPairs(ctx, &instrumentv1.ListTradingPairsRequest{})
	if err != nil {
		return err
	}
	list, err := c.c.ListContracts(ctx, &instrumentv1.ListContractsRequest{})
	if err != nil {
		return err
	}
	symbols := []string{}
	for _, p := range pairs.GetPairs() {
		if p.GetStatus() != "DELISTED" {
			symbols = append(symbols, p.GetSymbol())
		}
	}
	contracts := []ports.Contract{}
	for _, k := range list.GetContracts() {
		if k.GetStatus() == "DELISTED" {
			continue
		}
		ct, err := contract(k)
		if err != nil {
			return err
		}
		contracts = append(contracts, ct)
		symbols = append(symbols, k.GetSymbol())
	}
	slices.Sort(symbols)
	c.symbols, c.contracts, c.at = symbols, contracts, time.Now()
	return nil
}

func contract(k *instrumentv1.Contract) (ports.Contract, error) {
	var nums [3]decimal.Decimal
	for i, s := range []string{k.GetInterestRate(), k.GetFundingCap(), k.GetImpactNotional()} {
		d, err := decimal.NewFromString(s)
		if err != nil {
			return ports.Contract{}, fmt.Errorf("contract %s: bad funding parameter %q", k.GetSymbol(), s)
		}
		nums[i] = d
	}
	return ports.Contract{
		Symbol: k.GetSymbol(), IndexSymbol: k.GetIndexSymbol(), Status: k.GetStatus(),
		FundingIntervalHours: k.GetFundingIntervalHours(), InterestRate: nums[0], FundingCap: nums[1], ImpactNotional: nums[2],
	}, nil
}

// Symbols lists the pairs and contracts that are not delisted, sorted.
func (c *Client) Symbols(ctx context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	return c.symbols, nil
}

// Listed reports whether symbol is a pair or contract that is not
// delisted.
func (c *Client) Listed(ctx context.Context, symbol string) (bool, error) {
	list, err := c.Symbols(ctx)
	if err != nil {
		return false, err
	}
	_, found := slices.BinarySearch(list, symbol)
	return found, nil
}

// Contracts lists the contracts that are not delisted.
func (c *Client) Contracts(ctx context.Context) ([]ports.Contract, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	return c.contracts, nil
}
