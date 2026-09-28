// Package prices quotes assets in USDT for the withdrawal limits: the
// reference price market-data-service keeps for ASSET-USDT (§11.6: the
// platform index when there is one, else the reference), else a
// configured fallback.
package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"
)

// Client implements ports.Prices.
type Client struct {
	// Base is market-data-service's internal REST address.
	Base   string
	Client *http.Client
	// Fallback prices, by asset, when no fresh reference exists.
	Fallback map[string]decimal.Decimal
}

// ParseFallback reads "ETH:2500,BTC:60000".
func ParseFallback(s string) (map[string]decimal.Decimal, error) {
	out := map[string]decimal.Decimal{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		asset, price, ok := strings.Cut(part, ":")
		p, err := decimal.NewFromString(price)
		if !ok || err != nil || !p.IsPositive() {
			return nil, fmt.Errorf("bad fallback price %q (want ASSET:PRICE)", part)
		}
		out[strings.ToUpper(asset)] = p
	}
	return out, nil
}

// USDT returns asset's price and where it came from.
func (c *Client) USDT(ctx context.Context, asset string) (decimal.Decimal, string, error) {
	if c.Base != "" {
		if p, source, err := c.reference(ctx, asset); err == nil {
			return p, source, nil
		}
	}
	if p, ok := c.Fallback[asset]; ok {
		return p, "fallback", nil
	}
	return decimal.Zero, "", fmt.Errorf("no USDT price for %s", asset)
}

func (c *Client) reference(ctx context.Context, asset string) (decimal.Decimal, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/internal/market/"+asset+"-USDT/reference", nil)
	if err != nil {
		return decimal.Zero, "", err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return decimal.Zero, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Source *string `json:"source"`
		Price  *string `json:"price"`
		Fresh  bool    `json:"fresh"`
	}
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, "", fmt.Errorf("reference price: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return decimal.Zero, "", err
	}
	if !body.Fresh || body.Price == nil || body.Source == nil {
		return decimal.Zero, "", fmt.Errorf("no fresh reference price for %s", asset)
	}
	p, err := decimal.NewFromString(*body.Price)
	if err != nil || !p.IsPositive() {
		return decimal.Zero, "", fmt.Errorf("bad reference price %q", *body.Price)
	}
	return p, "reference:" + *body.Source, nil
}
