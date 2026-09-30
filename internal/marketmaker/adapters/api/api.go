// Package api reads the platform's services over their internal REST
// addresses for HOUSE's liquidity publisher: the pairs and contracts from
// instrument-service, HOUSE's contract positions from derivatives-service
// (as HOUSE's account, the user identity in X-User-Id as the gateway
// would pass it).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
)

// Client implements ports.Specs and the positions half of ports.House.
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
	Symbol          string `json:"symbol"`
	BaseAsset       string `json:"base_asset"`
	QuoteAsset      string `json:"quote_asset"`
	IndexSymbol     string `json:"index_symbol"`
	TickSize        string `json:"tick_size"`
	LotSize         string `json:"lot_size"`
	Status          string `json:"status"`
	ReferenceSymbol string `json:"reference_symbol"`
}

func (r instrumentRow) spec(contract bool) (domain.Spec, bool) {
	tick, err1 := decimal.NewFromString(r.TickSize)
	lot, err2 := decimal.NewFromString(r.LotSize)
	if err1 != nil || err2 != nil || !tick.IsPositive() || !lot.IsPositive() {
		return domain.Spec{}, false
	}
	return domain.Spec{Symbol: r.Symbol, Base: r.BaseAsset, Quote: r.QuoteAsset, TickSize: tick, LotSize: lot, Contract: contract}, true
}

// Specs lists the TRADING pairs that follow a reference market, and the
// TRADING contracts whose index pair does (they follow the reference
// market's perpetual of the same symbol).
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
	if err := c.get(ctx, c.Instrument+"/v1/market/contracts", &contracts); err != nil {
		return nil, fmt.Errorf("contracts: %w", err)
	}
	followed := map[string]bool{}
	var out []domain.Spec
	for _, p := range pairs.Pairs {
		if p.ReferenceSymbol == "" {
			continue
		}
		followed[p.Symbol] = true
		if s, ok := p.spec(false); ok && p.Status == "TRADING" {
			out = append(out, s)
		}
	}
	for _, k := range contracts.Contracts {
		if s, ok := k.spec(true); ok && k.Status == "TRADING" && followed[k.IndexSymbol] {
			out = append(out, s)
		}
	}
	return out, nil
}

// Positions returns HOUSE's net position on each contract, long positive.
func (c *Client) Positions(ctx context.Context) (map[string]decimal.Decimal, error) {
	var body struct {
		Positions []struct {
			Symbol   string `json:"symbol"`
			Quantity string `json:"quantity"`
		} `json:"positions"`
	}
	if err := c.get(ctx, c.Derivatives+"/v1/derivatives/positions", &body); err != nil {
		return nil, fmt.Errorf("positions: %w", err)
	}
	out := map[string]decimal.Decimal{}
	for _, p := range body.Positions {
		q, err := decimal.NewFromString(p.Quantity)
		if err != nil {
			return nil, fmt.Errorf("position of %s: bad quantity %q", p.Symbol, p.Quantity)
		}
		out[p.Symbol] = out[p.Symbol].Add(q)
	}
	return out, nil
}
