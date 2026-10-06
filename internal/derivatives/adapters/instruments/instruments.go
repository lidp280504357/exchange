// Package instruments reads perpetual contracts and their assets from
// instrument-service.
package instruments

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/skill/exchange/internal/derivatives/domain"
)

// Client implements ports.Instruments. Contracts are cached for TTL: every
// order needs one, and a status change (a halt) still applies within
// seconds.
type Client struct {
	c   instrumentv1.InstrumentServiceClient
	ttl time.Duration

	mu       sync.Mutex
	cache    map[string]cached
	decimals map[string]int32
}

// followed reports whether the index pair follows a reference market.
func (c *Client) followed(ctx context.Context, indexSymbol string) (bool, error) {
	if indexSymbol == "" {
		return false, nil
	}
	resp, err := c.c.GetTradingPair(ctx, &instrumentv1.GetTradingPairRequest{Symbol: indexSymbol})
	if err != nil {
		return false, fmt.Errorf("index pair %s: %w", indexSymbol, err)
	}
	return resp.GetPair().GetReferenceSymbol() != "", nil
}

// marginCoin is a coin-margined contract's margin type.
const marginCoin = "COIN"

// sizeOf is a contract's face value, "0" for a linear one.
func sizeOf(k *instrumentv1.Contract) string {
	if s := k.GetContractSize(); s != "" {
		return s
	}
	return "0"
}

type cached struct {
	contract domain.Contract
	at       time.Time
}

// New wraps an InstrumentService client.
func New(c instrumentv1.InstrumentServiceClient, ttl time.Duration) *Client {
	return &Client{c: c, ttl: ttl, cache: map[string]cached{}, decimals: map[string]int32{}}
}

// Contract returns the contract with its fee rates and asset decimals.
func (c *Client) Contract(ctx context.Context, symbol string) (domain.Contract, error) {
	c.mu.Lock()
	hit, ok := c.cache[symbol]
	c.mu.Unlock()
	if ok && time.Since(hit.at) < c.ttl {
		return hit.contract, nil
	}
	resp, err := c.c.GetContract(ctx, &instrumentv1.GetContractRequest{Symbol: symbol})
	if err != nil {
		return domain.Contract{}, err
	}
	ct, err := c.convert(ctx, resp.GetContract())
	if err != nil {
		return domain.Contract{}, err
	}
	c.mu.Lock()
	c.cache[symbol] = cached{contract: ct, at: time.Now()}
	c.mu.Unlock()
	return ct, nil
}

// Contracts lists every contract, the coin-margined ones too (the list
// leaves them out unless asked, design 2026-10-06 §2.5).
func (c *Client) Contracts(ctx context.Context) ([]domain.Contract, error) {
	resp, err := c.c.ListContracts(ctx, &instrumentv1.ListContractsRequest{MarginType: "ALL"})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Contract, 0, len(resp.GetContracts()))
	for _, k := range resp.GetContracts() {
		ct, err := c.convert(ctx, k)
		if err != nil {
			return nil, err
		}
		out = append(out, ct)
	}
	return out, nil
}

// assetDecimals caches asset precision, which does not change.
func (c *Client) assetDecimals(ctx context.Context, asset string) (int32, error) {
	c.mu.Lock()
	n, ok := c.decimals[asset]
	c.mu.Unlock()
	if ok {
		return n, nil
	}
	resp, err := c.c.GetAsset(ctx, &instrumentv1.GetAssetRequest{AssetCode: asset})
	if err != nil {
		return 0, err
	}
	n = resp.GetAsset().GetDecimals()
	c.mu.Lock()
	c.decimals[asset] = n
	c.mu.Unlock()
	return n, nil
}

// convert reads a contract. Amounts are in its settlement asset, whose
// precision QuoteDecimals is: the quote's (USDT) of a linear contract, the
// base's of a coin-margined one, quoted in USD, which is no asset.
func (c *Client) convert(ctx context.Context, k *instrumentv1.Contract) (domain.Contract, error) {
	base, err := c.assetDecimals(ctx, k.GetBaseAsset())
	if err != nil {
		return domain.Contract{}, err
	}
	settle := k.GetSettleAsset()
	if settle == "" {
		settle = k.GetQuoteAsset() // a contract from before the coin-margined ones
	}
	quote, err := c.assetDecimals(ctx, settle)
	if err != nil {
		return domain.Contract{}, err
	}
	followed, err := c.followed(ctx, k.GetIndexSymbol())
	if err != nil {
		return domain.Contract{}, err
	}
	ct := domain.Contract{
		Symbol: k.GetSymbol(), Base: k.GetBaseAsset(), Quote: k.GetQuoteAsset(), Status: k.GetStatus(),
		FundingIntervalHours: k.GetFundingIntervalHours(), BaseDecimals: base, QuoteDecimals: quote, Followed: followed,
	}
	for _, f := range []struct {
		dst *decimal.Decimal
		src string
	}{
		{&ct.TickSize, k.GetTickSize()},
		{&ct.LotSize, k.GetLotSize()},
		{&ct.MinQuantity, k.GetMinQuantity()},
		{&ct.MaxQuantity, k.GetMaxQuantity()},
		{&ct.MinNotional, k.GetMinNotional()},
		{&ct.PriceBand, k.GetPriceBand()},
		{&ct.MakerFeeRate, k.GetMakerFeeRate()},
		{&ct.TakerFeeRate, k.GetTakerFeeRate()},
		{&ct.ContractSize, sizeOf(k)},
	} {
		v, err := decimal.NewFromString(f.src)
		if err != nil {
			return domain.Contract{}, fmt.Errorf("contract %s: bad decimal %q: %w", k.GetSymbol(), f.src, err)
		}
		*f.dst = v
	}
	if (k.GetMarginType() == marginCoin) != ct.Inverse() || ct.Settle() != settle {
		return domain.Contract{}, fmt.Errorf("contract %s: margin type %q, contract size %s and settlement asset %s do not agree",
			k.GetSymbol(), k.GetMarginType(), ct.ContractSize, settle)
	}
	for _, t := range k.GetRiskTiers() {
		n, err1 := decimal.NewFromString(t.GetMaxNotional())
		m, err2 := decimal.NewFromString(t.GetMmr())
		if err1 != nil || err2 != nil {
			return domain.Contract{}, fmt.Errorf("contract %s: bad risk tier", k.GetSymbol())
		}
		ct.Tiers = append(ct.Tiers, domain.RiskTier{MaxNotional: n, MaxLeverage: t.GetMaxLeverage(), MMR: m})
	}
	return ct, nil
}
