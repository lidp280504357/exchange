// Package rates reads settled funding rates from market-data-service.
package rates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Client implements ports.FundingRates over market-data-service's REST
// API (GET /v1/market/{symbol}/funding-rates).
type Client struct {
	Base   string
	Client *http.Client
}

// Rate returns the rate and mark price of the contract's period that
// settled at at; found is false while it is not settled.
func (c Client) Rate(ctx context.Context, symbol string, at time.Time) (decimal.Decimal, decimal.Decimal, bool, error) {
	q := url.Values{
		"from": {at.UTC().Format(time.RFC3339)}, "to": {at.UTC().Add(time.Second).Format(time.RFC3339)}, "limit": {"1"},
	}
	u := strings.TrimRight(c.Base, "/") + "/v1/market/" + url.PathEscape(symbol) + "/funding-rates?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return decimal.Zero, decimal.Zero, false, err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return decimal.Zero, decimal.Zero, false, fmt.Errorf("funding rates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return decimal.Zero, decimal.Zero, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, decimal.Zero, false, fmt.Errorf("funding rates: HTTP %d", resp.StatusCode)
	}
	var body struct {
		FundingRates []struct {
			FundingTime time.Time       `json:"funding_time"`
			FundingRate decimal.Decimal `json:"funding_rate"`
			MarkPrice   decimal.Decimal `json:"mark_price"`
		} `json:"funding_rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return decimal.Zero, decimal.Zero, false, fmt.Errorf("funding rates: %w", err)
	}
	for _, r := range body.FundingRates {
		if r.FundingTime.Equal(at) && r.MarkPrice.IsPositive() {
			return r.FundingRate, r.MarkPrice, true, nil
		}
	}
	return decimal.Zero, decimal.Zero, false, nil
}
