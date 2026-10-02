// Package api reaches the platform's services over their internal REST
// addresses for the simulated market's bots: each request as the bot's
// user (X-User-Id, as the gateway would pass it), through the same order
// and balance paths as anyone; pair rules from instrument-service and
// prices from market-data-service.
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

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
)

// Client implements ports.Trading and ports.Prices: the base URLs of
// spot-trading-service, ledger-service, market-data-service and
// instrument-service.
type Client struct {
	TradingURL    string
	LedgerURL     string
	MarketURL     string
	InstrumentURL string
	HTTP          *http.Client
}

// Error is a refusal from a service: its unified error code.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return fmt.Sprintf("HTTP %d %s", e.Status, e.Code) }

func (c *Client) do(ctx context.Context, method, rawURL, user string, body, out any) error {
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
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
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

func code(err error) string {
	if e := (*Error)(nil); errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Pair reads the pair's rules from instrument-service.
func (c *Client) Pair(ctx context.Context, symbol string) (domain.Pair, error) {
	var p struct {
		Symbol      string `json:"symbol"`
		TickSize    string `json:"tick_size"`
		LotSize     string `json:"lot_size"`
		MinQuantity string `json:"min_quantity"`
		MinNotional string `json:"min_notional"`
		Status      string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, c.InstrumentURL+"/v1/market/pairs/"+url.PathEscape(symbol), "", nil, &p); err != nil {
		return domain.Pair{}, fmt.Errorf("pair %s: %w", symbol, err)
	}
	var ds [4]decimal.Decimal
	for i, s := range []string{p.TickSize, p.LotSize, p.MinQuantity, p.MinNotional} {
		v, err := decimal.NewFromString(s)
		if err != nil {
			return domain.Pair{}, fmt.Errorf("pair %s: bad rule %q", symbol, s)
		}
		ds[i] = v
	}
	if !ds[0].IsPositive() || !ds[1].IsPositive() {
		return domain.Pair{}, fmt.Errorf("pair %s: no tick or lot size", symbol)
	}
	return domain.Pair{Symbol: p.Symbol, Tick: ds[0], Lot: ds[1], MinQty: ds[2], MinNotional: ds[3], Trading: p.Status == "TRADING"}, nil
}

// Open lists the bot's active orders on symbol.
func (c *Client) Open(ctx context.Context, user, symbol string) ([]domain.Order, error) {
	var page struct {
		Items []struct {
			OrderID         string `json:"order_id"`
			Side            string `json:"side"`
			Price           string `json:"price"`
			CancelRequested bool   `json:"cancel_requested"`
		} `json:"items"`
	}
	q := url.Values{"symbol": {symbol}, "status": {"ACTIVE"}, "limit": {"200"}}
	if err := c.do(ctx, http.MethodGet, c.TradingURL+"/v1/orders?"+q.Encode(), user, nil, &page); err != nil {
		return nil, fmt.Errorf("open orders: %w", err)
	}
	out := make([]domain.Order, 0, len(page.Items))
	for _, o := range page.Items {
		price, err := decimal.NewFromString(o.Price)
		if err != nil {
			continue // a market order: never resting
		}
		out = append(out, domain.Order{ID: o.OrderID, Side: domain.Side(o.Side), Price: price, Canceling: o.CancelRequested})
	}
	return out, nil
}

// clientID is a new client order ID (at most 36 characters).
func clientID() string { return "sim" + strings.ReplaceAll(uuid.NewString(), "-", "") }

// Limit places a GTC limit order.
func (c *Client) Limit(ctx context.Context, user, symbol string, side domain.Side, price, qty decimal.Decimal) (string, error) {
	body := map[string]string{
		"symbol": symbol, "side": string(side), "type": "LIMIT", "time_in_force": "GTC",
		"price": price.String(), "quantity": qty.String(), "client_order_id": clientID(),
	}
	var out struct {
		OrderID string `json:"order_id"`
	}
	err := c.do(ctx, http.MethodPost, c.TradingURL+"/v1/orders", user, body, &out)
	if code(err) == "LEDGER_INSUFFICIENT_BALANCE" {
		return "", ports.ErrFunds
	}
	if err != nil {
		return "", fmt.Errorf("limit %s %s@%s: %w", side, qty, price, err)
	}
	return out.OrderID, nil
}

// Market places a market order: a buy spends quote, a sell sells qty.
func (c *Client) Market(ctx context.Context, user, symbol string, side domain.Side, quote, qty decimal.Decimal) error {
	body := map[string]string{"symbol": symbol, "side": string(side), "type": "MARKET", "client_order_id": clientID()}
	if side == domain.Buy {
		body["quote_amount"] = quote.String()
	} else {
		body["quantity"] = qty.String()
	}
	err := c.do(ctx, http.MethodPost, c.TradingURL+"/v1/orders", user, body, nil)
	if code(err) == "LEDGER_INSUFFICIENT_BALANCE" {
		return ports.ErrFunds
	}
	if err != nil {
		return fmt.Errorf("market %s: %w", side, err)
	}
	return nil
}

// Cancel asks to cancel one order; one that has just finished is fine.
func (c *Client) Cancel(ctx context.Context, user, orderID string) error {
	err := c.do(ctx, http.MethodDelete, c.TradingURL+"/v1/orders/"+url.PathEscape(orderID), user, nil, nil)
	switch code(err) {
	case "ORDER_ALREADY_FILLED", "COMMON_CONFLICT", "ORDER_NOT_FOUND":
		return nil
	}
	if err != nil {
		return fmt.Errorf("cancel %s: %w", orderID, err)
	}
	return nil
}

// CancelAll asks to cancel every active order of the bot on symbol.
func (c *Client) CancelAll(ctx context.Context, user, symbol string) error {
	if err := c.do(ctx, http.MethodDelete, c.TradingURL+"/v1/orders?"+url.Values{"symbol": {symbol}}.Encode(), user, nil, nil); err != nil {
		return fmt.Errorf("cancel all %s: %w", symbol, err)
	}
	return nil
}

// Balances returns the bot's available SPOT balances by asset.
func (c *Client) Balances(ctx context.Context, user string) (map[string]decimal.Decimal, error) {
	var body struct {
		Balances []struct {
			AccountType string `json:"account_type"`
			Asset       string `json:"asset"`
			Available   string `json:"available"`
		} `json:"balances"`
	}
	if err := c.do(ctx, http.MethodGet, c.LedgerURL+"/v1/account/balances?account_type=SPOT", user, nil, &body); err != nil {
		return nil, fmt.Errorf("balances: %w", err)
	}
	out := map[string]decimal.Decimal{}
	for _, b := range body.Balances {
		if v, err := decimal.NewFromString(b.Available); err == nil && b.AccountType == "SPOT" {
			out[b.Asset] = v
		}
	}
	return out, nil
}

// Reference reads the internal reference price and whether it is fresh.
func (c *Client) Reference(ctx context.Context, symbol string) (decimal.Decimal, bool, error) {
	var body struct {
		Price *string `json:"price"`
		Fresh bool    `json:"fresh"`
	}
	if err := c.do(ctx, http.MethodGet, c.MarketURL+"/internal/market/"+url.PathEscape(symbol)+"/reference", "", nil, &body); err != nil {
		return decimal.Zero, false, fmt.Errorf("reference %s: %w", symbol, err)
	}
	if body.Price == nil {
		return decimal.Zero, false, nil
	}
	p, err := decimal.NewFromString(*body.Price)
	return p, err == nil && body.Fresh, err
}

// Last reads the pair's last traded price on the platform (0: none).
func (c *Client) Last(ctx context.Context, symbol string) (decimal.Decimal, error) {
	var body struct {
		Last *string `json:"last"`
	}
	if err := c.do(ctx, http.MethodGet, c.MarketURL+"/v1/market/"+url.PathEscape(symbol)+"/ticker", "", nil, &body); err != nil {
		return decimal.Zero, fmt.Errorf("ticker %s: %w", symbol, err)
	}
	if body.Last == nil || *body.Last == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(*body.Last)
}
