// Package instruments tells market-data-service which pairs and contracts
// exist, what they follow, and halts pairs for it.
package instruments

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/skill/exchange/internal/marketdata/ports"
)

// Actor is who market-data-service's own status changes are recorded as.
const Actor = "market-data-service"

// Client implements ports.Instruments from ListTradingPairs, ListContracts
// and ListAssets, cached for ttl.
type Client struct {
	c   instrumentv1.InstrumentServiceClient
	ttl time.Duration

	mu        sync.Mutex
	symbols   []string
	contracts []ports.Contract
	pairs     []ports.Pair
	ranks     map[string]int32
	at        time.Time
}

// New wraps an InstrumentService client.
func New(c instrumentv1.InstrumentServiceClient, ttl time.Duration) *Client {
	return &Client{c: c, ttl: ttl}
}

// refresh reloads the lists when the cache is older than ttl; c.mu is
// held.
func (c *Client) refresh(ctx context.Context) error {
	if c.symbols != nil && time.Since(c.at) < c.ttl {
		return nil
	}
	pairs, err := c.c.ListTradingPairs(ctx, &instrumentv1.ListTradingPairsRequest{})
	if err != nil {
		return err
	}
	// Every contract, the coin-margined ones too (the list leaves them out
	// unless asked, design 2026-10-06 §2.5).
	list, err := c.c.ListContracts(ctx, &instrumentv1.ListContractsRequest{MarginType: "ALL"})
	if err != nil {
		return err
	}
	assets, err := c.c.ListAssets(ctx, &instrumentv1.ListAssetsRequest{})
	if err != nil {
		return err
	}
	assetRank := map[string]int32{}
	for _, a := range assets.GetAssets() {
		assetRank[a.GetAssetCode()] = a.GetRank()
	}
	symbols := []string{}
	listed := []ports.Pair{}
	ranks := map[string]int32{}
	for _, p := range pairs.GetPairs() {
		if p.GetStatus() == "DELISTED" {
			continue
		}
		pair, err := toPair(p, assetRank[p.GetBaseAsset()])
		if err != nil {
			return err
		}
		listed = append(listed, pair)
		symbols = append(symbols, p.GetSymbol())
		ranks[p.GetSymbol()] = pair.Rank
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
		ranks[k.GetSymbol()] = assetRank[k.GetBaseAsset()]
	}
	slices.Sort(symbols)
	c.symbols, c.contracts, c.pairs, c.ranks, c.at = symbols, contracts, listed, ranks, time.Now()
	return nil
}

func toPair(p *instrumentv1.TradingPair, rank int32) (ports.Pair, error) {
	out := ports.Pair{Symbol: p.GetSymbol(), Base: p.GetBaseAsset(), Quote: p.GetQuoteAsset(), Status: p.GetStatus(), Rank: rank}
	if p.GetReferenceSymbol() == "" {
		return out, nil
	}
	m := decimal.NewFromInt(1)
	if s := p.GetReferenceMultiplier(); s != "" {
		d, err := decimal.NewFromString(s)
		if err != nil || !d.IsPositive() {
			return ports.Pair{}, fmt.Errorf("pair %s: bad reference multiplier %q", p.GetSymbol(), s)
		}
		m = d
	}
	out.Reference = ports.Reference{Symbol: p.GetSymbol(), Remote: p.GetReferenceSymbol(), Multiplier: m}
	return out, nil
}

func contract(k *instrumentv1.Contract) (ports.Contract, error) {
	var nums [4]decimal.Decimal
	size := k.GetContractSize()
	if size == "" {
		size = "0"
	}
	for i, s := range []string{k.GetInterestRate(), k.GetFundingCap(), k.GetImpactNotional(), size} {
		d, err := decimal.NewFromString(s)
		if err != nil {
			return ports.Contract{}, fmt.Errorf("contract %s: bad parameter %q", k.GetSymbol(), s)
		}
		nums[i] = d
	}
	margin := k.GetMarginType()
	if margin == "" {
		margin = "USDT"
	}
	out := ports.Contract{
		Symbol: k.GetSymbol(), IndexSymbol: k.GetIndexSymbol(), Status: k.GetStatus(),
		FundingIntervalHours: k.GetFundingIntervalHours(), InterestRate: nums[0], FundingCap: nums[1], ImpactNotional: nums[2],
		MarginType: margin, ContractSize: nums[3], ReferenceSymbol: k.GetReferenceSymbol(),
	}
	if out.Inverse() && !out.ContractSize.IsPositive() {
		return ports.Contract{}, fmt.Errorf("contract %s: an inverse contract without a contract size", k.GetSymbol())
	}
	return out, nil
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

// Pairs lists the spot pairs that are not delisted.
func (c *Client) Pairs(ctx context.Context) ([]ports.Pair, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	return c.pairs, nil
}

// Ranks returns the base asset's rank of each listed pair and contract.
func (c *Client) Ranks(ctx context.Context) (map[string]int32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	return c.ranks, nil
}

// SetPairStatus moves a pair for market-data-service and drops the cache,
// so the next listing shows the new status.
func (c *Client) SetPairStatus(ctx context.Context, symbol, to, reason string) (string, error) {
	resp, err := c.c.SetPairStatus(ctx, &instrumentv1.SetPairStatusRequest{Symbol: symbol, ToStatus: to, Reason: reason, Actor: Actor})
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
	if err != nil {
		return "", err
	}
	return resp.GetFromStatus(), nil
}

// SetContractStatus moves a contract for market-data-service and drops
// the cache, like SetPairStatus.
func (c *Client) SetContractStatus(ctx context.Context, symbol, to, reason string) (string, error) {
	resp, err := c.c.SetContractStatus(ctx, &instrumentv1.SetContractStatusRequest{Symbol: symbol, ToStatus: to, Reason: reason, Actor: Actor})
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
	if err != nil {
		return "", err
	}
	return resp.GetFromStatus(), nil
}
