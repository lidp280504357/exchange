// Package platform reads the exchange's name from instrument-service's
// platform profile (design 2026-10-04 §4.5) over its internal REST
// address.
package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Client implements ports.PlatformName.
type Client struct {
	// BaseURL is instrument-service's internal address
	// (INSTRUMENT_SERVICE_URL).
	BaseURL string
	HTTP    *http.Client
}

// PlatformName returns the profile's name.
func (c *Client) PlatformName(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.BaseURL, "/")+"/internal/platform/profile", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("platform profile: HTTP %d", resp.StatusCode)
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return "", fmt.Errorf("platform profile: %w", err)
	}
	return p.Name, nil
}
