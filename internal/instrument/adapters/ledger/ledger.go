// Package ledger reads the welcome credits ledger-service grants (design
// 2026-10-04 §4.2) over its internal REST address, for the platform
// profile to show them.
package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/instrument/domain"
)

// Client implements ports.Ledger.
type Client struct {
	// BaseURL is ledger-service's internal address (LEDGER_SERVICE_URL).
	BaseURL string
	HTTP    *http.Client
}

// WelcomeCredits returns what a new account gets: nothing while the flag
// ledger.welcome_credit is off for everyone, whatever the list says.
func (c *Client) WelcomeCredits(ctx context.Context) ([]domain.Credit, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.BaseURL, "/")+"/internal/ledger/settings/welcome-credits", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("welcome credits: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Credits []struct {
			Asset  string          `json:"asset"`
			Amount decimal.Decimal `json:"amount"`
		} `json:"credits"`
		FlagEnabled bool `json:"flag_enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("welcome credits: %w", err)
	}
	out := []domain.Credit{}
	if !body.FlagEnabled {
		return out, nil
	}
	for _, c := range body.Credits {
		out = append(out, domain.Credit{Asset: c.Asset, Amount: c.Amount})
	}
	return out, nil
}
