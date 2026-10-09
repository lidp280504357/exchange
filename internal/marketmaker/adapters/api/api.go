// Package api reads the platform's services over their internal REST
// addresses for HOUSE's liquidity publisher: the pairs and contracts from
// instrument-service, HOUSE's contract positions and account from
// derivatives-service (as HOUSE's account, the user identity in X-User-Id
// as the gateway would pass it).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketmaker/domain"
)

// Client implements ports.Specs and the contracts half of ports.House.
type Client struct {
	Instrument  string
	Derivatives string
	// HouseUser is HOUSE's account.
	HouseUser string
	HTTP      *http.Client
}

func (c *Client) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if c.HouseUser != "" {
		req.Header.Set("X-User-Id", c.HouseUser)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, e.Code)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type instrumentRow struct {
	Symbol          string  `json:"symbol"`
	BaseAsset       string  `json:"base_asset"`
	QuoteAsset      string  `json:"quote_asset"`
	IndexSymbol     string  `json:"index_symbol"`
	TickSize        string  `json:"tick_size"`
	LotSize         string  `json:"lot_size"`
	Status          string  `json:"status"`
	ReferenceSymbol *string `json:"reference_symbol"`
	// A contract's settlement asset and, coin-margined, its face value;
	// its highest leverage.
	SettleAsset  string `json:"settle_asset"`
	ContractSize string `json:"contract_size"`
	MaxLeverage  int32  `json:"max_leverage"`
}

// reference is the row's reference symbol, "" when it has none.
func (r instrumentRow) reference() string {
	if r.ReferenceSymbol == nil {
		return ""
	}
	return *r.ReferenceSymbol
}

func (r instrumentRow) spec(contract bool) (domain.Spec, bool) {
	tick, err1 := decimal.NewFromString(r.TickSize)
	lot, err2 := decimal.NewFromString(r.LotSize)
	if err1 != nil || err2 != nil || !tick.IsPositive() || !lot.IsPositive() {
		return domain.Spec{}, false
	}
	s := domain.Spec{Symbol: r.Symbol, Base: r.BaseAsset, Quote: r.QuoteAsset, TickSize: tick, LotSize: lot, Contract: contract}
	if contract {
		s.Settle, s.MaxLeverage = r.SettleAsset, r.MaxLeverage
		if s.Settle == "" {
			s.Settle = r.QuoteAsset // a contract from before the coin-margined ones
		}
		if r.ContractSize != "" {
			size, err := decimal.NewFromString(r.ContractSize)
			if err != nil || size.IsNegative() {
				return domain.Spec{}, false
			}
			s.ContractSize = size
		}
	}
	return s, true
}

// Specs lists the pairs that follow a reference market, those not
// TRADING marked Halted, and the TRADING contracts, of both margin types,
// that follow one (their reference_symbol: Binance's perpetual).
func (c *Client) Specs(ctx context.Context) ([]domain.Spec, error) {
	var pairs struct {
		Pairs []instrumentRow `json:"pairs"`
	}
	if err := c.get(ctx, c.Instrument+"/v1/market/pairs", &pairs); err != nil {
		return nil, fmt.Errorf("pairs: %w", err)
	}
	var contracts struct {
		Contracts []instrumentRow `json:"contracts"`
	}
	if err := c.get(ctx, c.Instrument+"/v1/market/contracts?margin_type=ALL", &contracts); err != nil {
		return nil, fmt.Errorf("contracts: %w", err)
	}
	var out []domain.Spec
	for _, p := range pairs.Pairs {
		if p.reference() == "" {
			continue
		}
		if s, ok := p.spec(false); ok {
			s.Halted = p.Status != "TRADING"
			out = append(out, s)
		}
	}
	for _, k := range contracts.Contracts {
		if s, ok := k.spec(true); ok && k.Status == "TRADING" && k.reference() != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// Backed lists the assets with a network to deposit or withdraw them on,
// enabled or not.
func (c *Client) Backed(ctx context.Context) ([]string, error) {
	var body struct {
		Assets []struct {
			Code     string            `json:"asset_code"`
			Networks []json.RawMessage `json:"networks"`
		} `json:"assets"`
	}
	if err := c.get(ctx, c.Instrument+"/v1/market/assets", &body); err != nil {
		return nil, fmt.Errorf("assets: %w", err)
	}
	var out []string
	for _, a := range body.Assets {
		if len(a.Networks) > 0 {
			out = append(out, a.Code)
		}
	}
	return out, nil
}

// Contracts returns HOUSE's FUTURES accounts in assets: its net position
// on each contract (long positive; contracts of a coin-margined one); by
// settlement asset what its positions are worth together in USDT, each at
// its mark price (value_usd, the contracts' face value of a coin-margined
// one; the entry price before a linear contract's first mark price); and
// each account's equity, its margin balance in the asset.
func (c *Client) Contracts(ctx context.Context, assets []string) (domain.ContractAccount, error) {
	var body struct {
		Positions []struct {
			Symbol      string  `json:"symbol"`
			Quantity    string  `json:"quantity"`
			EntryPrice  string  `json:"entry_price"`
			Notional    *string `json:"notional"`
			SettleAsset string  `json:"settle_asset"`
			ValueUSD    *string `json:"value_usd"`
		} `json:"positions"`
	}
	if err := c.get(ctx, c.Derivatives+"/v1/derivatives/positions", &body); err != nil {
		return domain.ContractAccount{}, fmt.Errorf("positions: %w", err)
	}
	a := domain.ContractAccount{
		Positions: map[string]decimal.Decimal{}, Exposure: map[string]decimal.Decimal{}, Equity: map[string]decimal.Decimal{},
	}
	for _, p := range body.Positions {
		q, err := decimal.NewFromString(p.Quantity)
		if err != nil {
			return domain.ContractAccount{}, fmt.Errorf("position of %s: bad quantity %q", p.Symbol, p.Quantity)
		}
		a.Positions[p.Symbol] = a.Positions[p.Symbol].Add(q)
		settle := p.SettleAsset
		if settle == "" {
			settle = domain.Valuation
		}
		var worth decimal.Decimal
		switch {
		case p.ValueUSD != nil:
			worth, err = decimal.NewFromString(*p.ValueUSD)
		case p.Notional != nil:
			worth, err = decimal.NewFromString(*p.Notional)
		default:
			worth, err = decimal.NewFromString(p.EntryPrice)
			worth = worth.Mul(q)
		}
		if err != nil {
			return domain.ContractAccount{}, fmt.Errorf("position of %s: bad value, notional or entry price", p.Symbol)
		}
		a.Exposure[settle] = a.Exposure[settle].Add(worth.Abs())
	}
	for _, asset := range assets {
		var acct struct {
			MarginBalance string `json:"margin_balance"`
		}
		if err := c.get(ctx, c.Derivatives+"/v1/derivatives/account?"+url.Values{"asset": {asset}}.Encode(), &acct); err != nil {
			return domain.ContractAccount{}, fmt.Errorf("%s account: %w", asset, err)
		}
		equity, err := decimal.NewFromString(acct.MarginBalance)
		if err != nil {
			return domain.ContractAccount{}, fmt.Errorf("%s account: bad margin balance %q", asset, acct.MarginBalance)
		}
		a.Equity[asset] = equity
	}
	return a, nil
}
