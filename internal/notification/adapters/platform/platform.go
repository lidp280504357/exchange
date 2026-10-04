// Package platform reads the exchange's name and mode from
// instrument-service's platform profile (design 2026-10-04 §4.4, §4.5) over
// its internal REST address.
package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Client implements ports.PlatformName and ports.PlatformMode.
type Client struct {
	// BaseURL is instrument-service's internal address
	// (INSTRUMENT_SERVICE_URL).
	BaseURL string
	HTTP    *http.Client
}

// profile is what the service reads of the profile.
type profile struct {
	Name     string `json:"name"`
	TestMode struct {
		Enabled bool `json:"enabled"`
	} `json:"test_mode"`
}

func (c *Client) profile(ctx context.Context) (profile, error) {
	var p profile
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.BaseURL, "/")+"/internal/platform/profile", nil)
	if err != nil {
		return p, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return p, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return p, fmt.Errorf("platform profile: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return p, fmt.Errorf("platform profile: %w", err)
	}
	return p, nil
}

// PlatformName returns the profile's name.
func (c *Client) PlatformName(ctx context.Context) (string, error) {
	p, err := c.profile(ctx)
	return p.Name, err
}

// TestMode reports whether the profile has the exchange in test mode.
func (c *Client) TestMode(ctx context.Context) (bool, error) {
	p, err := c.profile(ctx)
	return p.TestMode.Enabled, err
}
