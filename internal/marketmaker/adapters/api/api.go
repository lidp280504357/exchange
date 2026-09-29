// Package api reaches the platform's services over their internal REST
// addresses, as the market maker's own account: the same order and ledger
// paths as any user (§11.10), the user identity in X-User-Id as the
// gateway would pass it. Client quotes spot pairs, Contracts perpetual
// contracts.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/marketmaker/ports"
)

// Client implements ports.Orders, Balances, References and Pairs.
type Client struct {
	Trading    string
	Ledger     string
	Market     string
	Instrument string
	UserID     string
	HTTP       *http.Client

	mu    sync.Mutex
	pairs map[string]cachedPair
}

type cachedPair struct {
	pair ports.PairInfo
	at   time.Time
}

// Error is a refusal from a service: its unified error code.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return fmt.Sprintf("HTTP %d %s", e.Status, e.Code) }

func (c *Client) do(ctx context.Context, method, rawURL string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-User-Id", c.UserID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return &Error{Status: resp.StatusCode, Code: e.Code}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Open lists the account's active orders of symbol.
func (c *Client) Open(ctx context.Context, symbol string) ([]ports.Order, error) {
	var page struct {
		Items []struct {
			OrderID         string `json:"order_id"`
			Side            string `json:"side"`
			Price           string `json:"price"`
			CancelRequested bool   `json:"cancel_requested"`
		} `json:"items"`
	}
	q := url.Values{"symbol": {symbol}, "status": {"ACTIVE"}, "limit": {"200"}}
	if err := c.do(ctx, http.MethodGet, c.Trading+"/v1/orders?"+q.Encode(), nil, &page); err != nil {
		return nil, fmt.Errorf("open orders: %w", err)
	}
	out := make([]ports.Order, 0, len(page.Items))
	for _, o := range page.Items {
		price, err := decimal.NewFromString(o.Price)
		if err != nil {
			continue // a market order: never the market maker's
		}
		out = append(out, ports.Order{ID: o.OrderID, Side: o.Side, Price: price, CancelRequested: o.CancelRequested})
	}
	return out, nil
}

// Place sends a GTC limit order; a refusal for funds is left for the next
// round, when released funds are back.
func (c *Client) Place(ctx context.Context, symbol string, q domain.Quote) error {
	body := map[string]string{
		"symbol": symbol, "side": q.Side, "type": "LIMIT", "time_in_force": "GTC",
		// client_order_id takes at most 36 characters.
		"price": q.Price.String(), "quantity": q.Quantity.String(), "client_order_id": "mm" + strings.ReplaceAll(uuid.NewString(), "-", ""),
	}
	err := c.do(ctx, http.MethodPost, c.Trading+"/v1/orders", body, nil)
	if e := (*Error)(nil); errors.As(err, &e) && e.Code == "LEDGER_INSUFFICIENT_BALANCE" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("place %s %s@%s: %w", symbol, q.Side, q.Price, err)
	}
	return nil
}

// Cancel asks to cancel one order; an order that has just finished is fine.
func (c *Client) Cancel(ctx context.Context, orderID string) error {
	err := c.do(ctx, http.MethodDelete, c.Trading+"/v1/orders/"+url.PathEscape(orderID), nil, nil)
	if e := (*Error)(nil); errors.As(err, &e) && (e.Code == "ORDER_ALREADY_FILLED" || e.Code == "COMMON_CONFLICT") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cancel %s: %w", orderID, err)
	}
	return nil
}

// CancelAll asks to cancel every active order of symbol.
func (c *Client) CancelAll(ctx context.Context, symbol string) error {
	if err := c.do(ctx, http.MethodDelete, c.Trading+"/v1/orders?"+url.Values{"symbol": {symbol}}.Encode(), nil, nil); err != nil {
		return fmt.Errorf("cancel all %s: %w", symbol, err)
	}
	return nil
}

// Spot returns the account's spot balances by asset.
func (c *Client) Spot(ctx context.Context) (map[string]ports.Balance, error) {
	var body struct {
		Balances []struct {
			AccountType string `json:"account_type"`
			Asset       string `json:"asset"`
			Available   string `json:"available"`
			Frozen      string `json:"frozen"`
		} `json:"balances"`
	}
	if err := c.do(ctx, http.MethodGet, c.Ledger+"/v1/account/balances?account_type=SPOT", nil, &body); err != nil {
		return nil, fmt.Errorf("balances: %w", err)
	}
	out := map[string]ports.Balance{}
	for _, b := range body.Balances {
		avail, err1 := decimal.NewFromString(b.Available)
		frozen, err2 := decimal.NewFromString(b.Frozen)
		if b.AccountType == "SPOT" && err1 == nil && err2 == nil {
			out[b.Asset] = ports.Balance{Available: avail, Frozen: frozen}
		}
	}
	return out, nil
}

// Price reads the internal reference price.
func (c *Client) Price(ctx context.Context, symbol string) (decimal.Decimal, bool, error) {
	var body struct {
		Price *string `json:"price"`
		Fresh bool    `json:"fresh"`
	}
	if err := c.do(ctx, http.MethodGet, c.Market+"/internal/market/"+url.PathEscape(symbol)+"/reference", nil, &body); err != nil {
		return decimal.Zero, false, fmt.Errorf("reference: %w", err)
	}
	if body.Price == nil {
		return decimal.Zero, false, nil
	}
	p, err := decimal.NewFromString(*body.Price)
	return p, err == nil && body.Fresh, err
}

// Pair reads a trading pair, cached for ten seconds.
func (c *Client) Pair(ctx context.Context, symbol string) (ports.PairInfo, error) {
	c.mu.Lock()
	hit, ok := c.pairs[symbol]
	c.mu.Unlock()
	if ok && time.Since(hit.at) < 10*time.Second {
		return hit.pair, nil
	}
	info, err := c.instrument(ctx, "/v1/market/pairs/"+url.PathEscape(symbol))
	if err != nil {
		return ports.PairInfo{}, fmt.Errorf("pair %s: %w", symbol, err)
	}
	c.mu.Lock()
	if c.pairs == nil {
		c.pairs = map[string]cachedPair{}
	}
	c.pairs[symbol] = cachedPair{pair: info, at: time.Now()}
	c.mu.Unlock()
	return info, nil
}

// instrument reads a trading pair or a contract (the same fields) from
// instrument-service's public API.
func (c *Client) instrument(ctx context.Context, path string) (ports.PairInfo, error) {
	var p struct {
		Status     string `json:"status"`
		BaseAsset  string `json:"base_asset"`
		QuoteAsset string `json:"quote_asset"`
		TickSize   string `json:"tick_size"`
		LotSize    string `json:"lot_size"`
	}
	if err := c.do(ctx, http.MethodGet, c.Instrument+path, nil, &p); err != nil {
		return ports.PairInfo{}, err
	}
	tick, err1 := decimal.NewFromString(p.TickSize)
	lot, err2 := decimal.NewFromString(p.LotSize)
	if err1 != nil || err2 != nil || !tick.IsPositive() || !lot.IsPositive() {
		return ports.PairInfo{}, errors.New("bad tick or lot size")
	}
	return ports.PairInfo{Status: p.Status, Base: p.BaseAsset, Quote: p.QuoteAsset, TickSize: tick, LotSize: lot}, nil
}
