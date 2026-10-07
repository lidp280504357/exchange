// Package prices values assets in USDT from market-data-service's
// tickers (GET /v1/market/tickers): the last price of each asset's USDT
// pair, the platform's or, for the pairs that follow it, the reference
// market's (ADR-0010) — the source of the contracts' index prices too.
// A price event on a followed pair (design 2026-10-07, general price
// control) moves its ticker; one that leaves the perpetuals and the
// leverage on the reference market's price (risk false, GET
// /internal/market/overlay) is valued at market-data's price for risk
// instead (GET /internal/market/{symbol}/reference?for=risk; review GD ①).
package prices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
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

// Poll reads the tickers once, and the price events without risk: their
// pairs are valued at market-data's price for risk, or keep the price
// last polled while it cannot be read (it goes stale in MaxTickerAge).
func (p *Poller) Poll(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var list []Ticker
	if err := p.get(ctx, "/v1/market/tickers", func(r io.Reader) (err error) {
		list, err = Parse(r)
		return err
	}); err != nil {
		return fmt.Errorf("tickers: %w", err)
	}
	var plain []string
	if err := p.get(ctx, "/internal/market/overlay", func(r io.Reader) (err error) {
		plain, err = ParseOverlays(r)
		return err
	}); err != nil {
		p.Set(list, p.Now())
		return fmt.Errorf("price events: %w", err)
	}
	var errs []error
	for _, symbol := range plain {
		i := slices.IndexFunc(list, func(t Ticker) bool { return t.Symbol == symbol })
		if i < 0 {
			continue
		}
		t, err := p.riskPrice(ctx, symbol)
		if err != nil {
			errs = append(errs, fmt.Errorf("the price for risk of %s: %w", symbol, err))
			var ok bool
			if t, ok = p.last(symbol); !ok {
				list = slices.Delete(list, i, i+1) // never priced: not the event's
				continue
			}
		}
		list[i] = t
	}
	p.Set(list, p.Now())
	return errors.Join(errs...)
}

// riskPrice reads a pair's price for risk from market-data.
func (p *Poller) riskPrice(ctx context.Context, symbol string) (Ticker, error) {
	var body struct {
		Price     *string   `json:"price"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := p.get(ctx, "/internal/market/"+url.PathEscape(symbol)+"/reference?for=risk", func(r io.Reader) error {
		return json.NewDecoder(r).Decode(&body)
	}); err != nil {
		return Ticker{}, err
	}
	if body.Price == nil {
		return Ticker{}, errors.New("no price")
	}
	last, err := decimal.NewFromString(*body.Price)
	if err != nil || !last.IsPositive() {
		return Ticker{}, fmt.Errorf("price %q", *body.Price)
	}
	return Ticker{Symbol: symbol, Last: last, UpdatedAt: body.UpdatedAt}, nil
}

// last is the ticker of symbol as last polled; false when there is none.
func (p *Poller) last(symbol string) (Ticker, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if base, ok := strings.CutSuffix(symbol, "-"+domain.ValueAsset); ok {
		if t, ok := p.tickers[base]; ok {
			return t, true
		}
	}
	return Ticker{}, false
}

func (p *Poller) get(ctx context.Context, path string, read func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.Base, "/")+path, nil)
	if err != nil {
		return err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return read(resp.Body)
}

// ParseOverlays reads market-data's price events and returns the pairs of
// those without risk.
func ParseOverlays(r io.Reader) ([]string, error) {
	var body struct {
		Items []struct {
			Symbol string `json:"symbol"`
			Risk   bool   `json:"risk"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	var out []string
	for _, o := range body.Items {
		if !o.Risk {
			out = append(out, o.Symbol)
		}
	}
	return out, nil
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
