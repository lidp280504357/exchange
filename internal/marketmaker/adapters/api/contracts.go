package api

import (
	"context"
	"errors"
	"fmt"
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

// MarkStale is how old a mark price may be before the quotes on it stop
// (§11.10's 5 seconds for reference prices).
const MarkStale = 5 * time.Second

// Contracts implements ports.Orders, References, Pairs and Positions for
// perpetual contracts: derivatives-service's order and position API as
// the market maker's account, the mark price from market-data-service
// and the contract from instrument-service.
type Contracts struct {
	// Client carries the market maker's account, the HTTP client and the
	// market-data and instrument addresses.
	Client *Client
	// Derivatives is derivatives-service's REST address.
	Derivatives string
	// Now is the clock of mark freshness; nil means time.Now.
	Now func() time.Time

	mu    sync.Mutex
	specs map[string]cachedPair
}

// Refusals of a contract order: the ones the next round retries (funds,
// limits, a price the mark moved away from) and the ones that mean the
// contract takes no opening orders for now.
var (
	retryLater = map[string]bool{
		"DERIV_INSUFFICIENT_MARGIN": true, "DERIV_RISK_LIMIT_EXCEEDED": true, "ORDER_TOO_MANY_OPEN": true,
		"ORDER_PRICE_OUT_OF_BAND": true,
	}
	paused = map[string]bool{
		"DERIV_REDUCE_ONLY_MODE": true, "DERIV_MARK_PRICE_UNAVAILABLE": true, "DERIV_DISABLED": true,
		"DERIV_POSITION_LIQUIDATING": true, "INSTRUMENT_NOT_TRADING": true,
	}
)

// Open lists the account's active orders of a contract.
func (c *Contracts) Open(ctx context.Context, symbol string) ([]ports.Order, error) {
	var page struct {
		Items []struct {
			OrderID         string `json:"order_id"`
			Side            string `json:"side"`
			Type            string `json:"type"`
			Price           string `json:"price"`
			CancelRequested bool   `json:"cancel_requested"`
		} `json:"items"`
	}
	q := url.Values{"symbol": {symbol}, "status": {"ACTIVE"}, "limit": {"100"}}
	if err := c.Client.do(ctx, http.MethodGet, c.Derivatives+"/v1/derivatives/orders?"+q.Encode(), nil, &page); err != nil {
		return nil, fmt.Errorf("open contract orders: %w", err)
	}
	out := make([]ports.Order, 0, len(page.Items))
	for _, o := range page.Items {
		price, err := decimal.NewFromString(o.Price)
		if err != nil || o.Type != "LIMIT" {
			continue // never the market maker's
		}
		out = append(out, ports.Order{ID: o.OrderID, Side: o.Side, Price: price, CancelRequested: o.CancelRequested})
	}
	return out, nil
}

// Place sends a GTC limit order. Refusals the next round retries are not
// errors; a contract that takes no opening orders answers
// ports.ErrPaused.
func (c *Contracts) Place(ctx context.Context, symbol string, q domain.Quote) error {
	body := map[string]string{
		"symbol": symbol, "side": q.Side, "type": "LIMIT", "time_in_force": "GTC",
		"price": q.Price.String(), "quantity": q.Quantity.String(), "client_order_id": "mm" + strings.ReplaceAll(uuid.NewString(), "-", ""),
	}
	err := c.Client.do(ctx, http.MethodPost, c.Derivatives+"/v1/derivatives/orders", body, nil)
	var e *Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &e) && retryLater[e.Code]:
		return nil
	case errors.As(err, &e) && paused[e.Code]:
		return fmt.Errorf("%w: %s", ports.ErrPaused, e.Code)
	}
	return fmt.Errorf("place %s %s@%s: %w", symbol, q.Side, q.Price, err)
}

// Cancel asks to cancel one order; an order that has just finished is fine.
func (c *Contracts) Cancel(ctx context.Context, orderID string) error {
	err := c.Client.do(ctx, http.MethodDelete, c.Derivatives+"/v1/derivatives/orders/"+url.PathEscape(orderID), nil, nil)
	if e := (*Error)(nil); errors.As(err, &e) && (e.Code == "ORDER_ALREADY_FILLED" || e.Code == "COMMON_CONFLICT") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cancel %s: %w", orderID, err)
	}
	return nil
}

// CancelAll asks to cancel every active order of a contract.
func (c *Contracts) CancelAll(ctx context.Context, symbol string) error {
	err := c.Client.do(ctx, http.MethodDelete, c.Derivatives+"/v1/derivatives/orders?"+url.Values{"symbol": {symbol}}.Encode(), nil, nil)
	if err != nil {
		return fmt.Errorf("cancel all %s: %w", symbol, err)
	}
	return nil
}

// Net returns the account's net position on a contract.
func (c *Contracts) Net(ctx context.Context, symbol string) (decimal.Decimal, error) {
	var body struct {
		Positions []struct {
			Quantity string `json:"quantity"`
		} `json:"positions"`
	}
	if err := c.Client.do(ctx, http.MethodGet, c.Derivatives+"/v1/derivatives/positions?"+url.Values{"symbol": {symbol}}.Encode(), nil,
		&body); err != nil {
		return decimal.Zero, fmt.Errorf("positions: %w", err)
	}
	net := decimal.Zero
	for _, p := range body.Positions {
		q, err := decimal.NewFromString(p.Quantity)
		if err != nil {
			return decimal.Zero, fmt.Errorf("position of %s: bad quantity %q", symbol, p.Quantity)
		}
		net = net.Add(q)
	}
	return net, nil
}

// Price reads the contract's mark price; it is fresh when it is not
// degraded and at most MarkStale old.
func (c *Contracts) Price(ctx context.Context, symbol string) (decimal.Decimal, bool, error) {
	var body struct {
		MarkPrice *string    `json:"mark_price"`
		Degraded  bool       `json:"degraded"`
		UpdatedAt *time.Time `json:"updated_at"`
	}
	if err := c.Client.do(ctx, http.MethodGet, c.Client.Market+"/v1/market/"+url.PathEscape(symbol)+"/mark-price", nil, &body); err != nil {
		return decimal.Zero, false, fmt.Errorf("mark price: %w", err)
	}
	if body.MarkPrice == nil || body.UpdatedAt == nil {
		return decimal.Zero, false, nil
	}
	p, err := decimal.NewFromString(*body.MarkPrice)
	if err != nil {
		return decimal.Zero, false, err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	return p, !body.Degraded && now().Sub(*body.UpdatedAt) <= MarkStale, nil
}

// Pair reads a contract, cached for ten seconds.
func (c *Contracts) Pair(ctx context.Context, symbol string) (ports.PairInfo, error) {
	c.mu.Lock()
	hit, ok := c.specs[symbol]
	c.mu.Unlock()
	if ok && time.Since(hit.at) < 10*time.Second {
		return hit.pair, nil
	}
	info, err := c.Client.instrument(ctx, "/v1/market/contracts/"+url.PathEscape(symbol))
	if err != nil {
		return ports.PairInfo{}, fmt.Errorf("contract %s: %w", symbol, err)
	}
	c.mu.Lock()
	if c.specs == nil {
		c.specs = map[string]cachedPair{}
	}
	c.specs[symbol] = cachedPair{pair: info, at: time.Now()}
	c.mu.Unlock()
	return info, nil
}
