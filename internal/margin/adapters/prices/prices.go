// Package prices values assets in USDT from market-data-service's
// tickers (GET /v1/market/tickers): the last price of each asset's USDT
// pair, the platform's or, for the pairs that follow it, the reference
// market's (ADR-0010) — the source of the contracts' index prices too.
package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
)

// Freshness limits: a price counts as fresh while the last poll that
// brought it is at most MaxPollAge old and its ticker was computed at
// most MaxTickerAge ago (a reference ticker stops when its feed does; the
// platform's are computed when asked).
const (
	MaxPollAge   = 10 * time.Second
	MaxTickerAge = time.Minute
)

// Ticker is what the poller reads of a ticker.
type Ticker struct {
	Symbol    string
	Last      decimal.Decimal
	UpdatedAt time.Time
}

// Poller implements ports.Prices; it is safe for concurrent use.
type Poller struct {
	Base   string
	Client *http.Client
	// Every is the poll interval (one second when zero).
	Every time.Duration
	Log   *slog.Logger
	Now   func() time.Time

	mu      sync.RWMutex
	tickers map[string]Ticker // by asset
	polled  time.Time
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) error {
	every := p.Every
	if every <= 0 {
		every = time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	failures := 0
	for {
		if err := p.Poll(ctx); err != nil && ctx.Err() == nil {
			if failures++; failures == 1 || failures%60 == 0 {
				p.Log.WarnContext(ctx, "ticker poll failed", "error", err, "failures", failures)
			}
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Poll reads the tickers once.
func (p *Poller) Poll(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.Base, "/")+"/v1/market/tickers", nil)
	if err != nil {
		return err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return fmt.Errorf("tickers: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tickers: HTTP %d", resp.StatusCode)
	}
	list, err := Parse(resp.Body)
	if err != nil {
		return err
	}
	p.Set(list, p.Now())
	return nil
}

// Parse reads a tickers response.
func Parse(r io.Reader) ([]Ticker, error) {
	var body struct {
		Tickers []struct {
			Symbol    string    `json:"symbol"`
			Last      *string   `json:"last"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"tickers"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, fmt.Errorf("tickers: %w", err)
	}
	out := make([]Ticker, 0, len(body.Tickers))
	for _, t := range body.Tickers {
		if t.Last == nil {
			continue
		}
		last, err := decimal.NewFromString(*t.Last)
		if err != nil || !last.IsPositive() {
			continue
		}
		out = append(out, Ticker{Symbol: t.Symbol, Last: last, UpdatedAt: t.UpdatedAt})
	}
	return out, nil
}

// Set replaces the prices with those of a poll made at at: each asset's
// is the last price of its USDT pair.
func (p *Poller) Set(list []Ticker, at time.Time) {
	byAsset := make(map[string]Ticker, len(list))
	for _, t := range list {
		if base, ok := strings.CutSuffix(t.Symbol, "-"+domain.ValueAsset); ok && base != "" {
			byAsset[base] = t
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tickers, p.polled = byAsset, at
}

// Prices returns the latest prices and whether each is fresh.
func (p *Poller) Prices() domain.Prices {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := p.Now()
	pollFresh := now.Sub(p.polled) <= MaxPollAge
	out := make(domain.Prices, len(p.tickers))
	for asset, t := range p.tickers {
		out[asset] = domain.Price{Value: t.Last, Fresh: pollFresh && now.Sub(t.UpdatedAt) <= MaxTickerAge}
	}
	return out
}
