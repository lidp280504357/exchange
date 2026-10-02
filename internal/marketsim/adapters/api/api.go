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
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
)

// Client implements ports.Trading, ports.Derivatives and ports.Prices:
// the base URLs of spot-trading-service, ledger-service,
// market-data-service, instrument-service and derivatives-service.
type Client struct {
	TradingURL     string
	LedgerURL      string
	MarketURL      string
	InstrumentURL  string
	DerivativesURL string
	HTTP           *http.Client
}

// Error is a refusal from a service: its unified error code.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return fmt.Sprintf("HTTP %d %s", e.Status, e.Code) }

// Refused reports an answer of 4xx but a timeout or a rate limit: the
// platform refused the request and did nothing (ports.Refused).
func (e *Error) Refused() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusRequestTimeout && e.Status != http.StatusTooManyRequests
}

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

// outOfBand is both trading services' refusal of a limit price too far
// from the band's anchor.
const outOfBand = "ORDER_PRICE_OUT_OF_BAND"

// band is a price band's share, 0 when there is none.
func band(s string) float64 {
	b, err := decimal.NewFromString(s)
	if err != nil || !b.IsPositive() {
		return 0
	}
	return b.InexactFloat64()
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
		PriceBand   string `json:"price_band"`
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
	return domain.Pair{
		Symbol: p.Symbol, Tick: ds[0], Lot: ds[1], MinQty: ds[2], MinNotional: ds[3], Band: band(p.PriceBand), Status: p.Status,
		Trading: p.Status == "TRADING",
	}, nil
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
	switch code(err) {
	case "LEDGER_INSUFFICIENT_BALANCE":
		return "", ports.ErrFunds
	case outOfBand:
		return "", ports.ErrOutOfBand
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

// LastTrade reads the pair's last trade on the platform (0: none).
func (c *Client) LastTrade(ctx context.Context, symbol string) (decimal.Decimal, time.Time, error) {
	var body struct {
		Trades []struct {
			Price      string    `json:"price"`
			ExecutedAt time.Time `json:"executed_at"`
		} `json:"trades"`
	}
	if err := c.do(ctx, http.MethodGet, c.MarketURL+"/v1/market/"+url.PathEscape(symbol)+"/trades?limit=1", "", nil, &body); err != nil {
		return decimal.Zero, time.Time{}, fmt.Errorf("trades %s: %w", symbol, err)
	}
	if len(body.Trades) == 0 {
		return decimal.Zero, time.Time{}, nil
	}
	p, err := decimal.NewFromString(body.Trades[0].Price)
	if err != nil {
		return decimal.Zero, time.Time{}, fmt.Errorf("trades %s: %w", symbol, err)
	}
	return p, body.Trades[0].ExecutedAt, nil
}

// Report gives market-data-service the simulated market's target of
// symbol.
func (c *Client) Report(ctx context.Context, symbol string, price decimal.Decimal) error {
	body := map[string]string{"price": price.String()}
	if err := c.do(ctx, http.MethodPut, c.MarketURL+"/internal/market/"+url.PathEscape(symbol)+"/simulated-price", "", body, nil); err != nil {
		return fmt.Errorf("simulated price %s: %w", symbol, err)
	}
	return nil
}

// Mark reads a contract's mark and index prices (0: none yet).
func (c *Client) Mark(ctx context.Context, symbol string) (mark, index decimal.Decimal, err error) {
	var body struct {
		MarkPrice  *string `json:"mark_price"`
		IndexPrice *string `json:"index_price"`
	}
	if err := c.do(ctx, http.MethodGet, c.MarketURL+"/v1/market/"+url.PathEscape(symbol)+"/mark-price", "", nil, &body); err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("mark price %s: %w", symbol, err)
	}
	parse := func(s *string) decimal.Decimal {
		if s == nil {
			return decimal.Zero
		}
		v, err := decimal.NewFromString(*s)
		if err != nil {
			return decimal.Zero
		}
		return v
	}
	return parse(body.MarkPrice), parse(body.IndexPrice), nil
}

// Contract reads the contract's rules from instrument-service.
func (c *Client) Contract(ctx context.Context, symbol string) (domain.Pair, error) {
	var k struct {
		Symbol      string `json:"symbol"`
		TickSize    string `json:"tick_size"`
		LotSize     string `json:"lot_size"`
		MinQuantity string `json:"min_quantity"`
		MinNotional string `json:"min_notional"`
		PriceBand   string `json:"price_band"`
		Status      string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, c.InstrumentURL+"/v1/market/contracts/"+url.PathEscape(symbol), "", nil, &k); err != nil {
		return domain.Pair{}, fmt.Errorf("contract %s: %w", symbol, err)
	}
	var ds [4]decimal.Decimal
	for i, s := range []string{k.TickSize, k.LotSize, k.MinQuantity, k.MinNotional} {
		v, err := decimal.NewFromString(s)
		if err != nil {
			return domain.Pair{}, fmt.Errorf("contract %s: bad rule %q", symbol, s)
		}
		ds[i] = v
	}
	if !ds[0].IsPositive() || !ds[1].IsPositive() {
		return domain.Pair{}, fmt.Errorf("contract %s: no tick or lot size", symbol)
	}
	return domain.Pair{
		Symbol: k.Symbol, Tick: ds[0], Lot: ds[1], MinQty: ds[2], MinNotional: ds[3], Band: band(k.PriceBand), Status: k.Status,
		Trading: k.Status == "TRADING",
	}, nil
}

// OpenContract lists the bot's active orders on the contract.
func (c *Client) OpenContract(ctx context.Context, user, symbol string) ([]domain.Order, error) {
	var page struct {
		Items []struct {
			OrderID         string `json:"order_id"`
			Side            string `json:"side"`
			Type            string `json:"type"`
			Price           string `json:"price"`
			CancelRequested bool   `json:"cancel_requested"`
		} `json:"items"`
	}
	q := url.Values{"symbol": {symbol}, "status": {"ACTIVE"}, "limit": {"200"}}
	if err := c.do(ctx, http.MethodGet, c.DerivativesURL+"/v1/derivatives/orders?"+q.Encode(), user, nil, &page); err != nil {
		return nil, fmt.Errorf("open contract orders: %w", err)
	}
	out := make([]domain.Order, 0, len(page.Items))
	for _, o := range page.Items {
		price, err := decimal.NewFromString(o.Price)
		if err != nil || o.Type != "LIMIT" {
			continue // a market order (its protection price): never resting
		}
		out = append(out, domain.Order{ID: o.OrderID, Side: domain.Side(o.Side), Price: price, Canceling: o.CancelRequested})
	}
	return out, nil
}

// LimitContract places a GTC limit order on the contract.
func (c *Client) LimitContract(ctx context.Context, user, symbol string, side domain.Side, price, qty decimal.Decimal) (string, error) {
	body := map[string]any{
		"symbol": symbol, "side": string(side), "type": "LIMIT", "time_in_force": "GTC",
		"price": price.String(), "quantity": qty.String(), "client_order_id": clientID(),
	}
	var out struct {
		OrderID string `json:"order_id"`
	}
	err := c.do(ctx, http.MethodPost, c.DerivativesURL+"/v1/derivatives/orders", user, body, &out)
	switch code(err) {
	case "DERIV_INSUFFICIENT_MARGIN", "LEDGER_INSUFFICIENT_BALANCE", "DERIV_RISK_LIMIT_EXCEEDED":
		return "", ports.ErrFunds
	case outOfBand:
		return "", ports.ErrOutOfBand
	}
	if err != nil {
		return "", fmt.Errorf("contract limit %s %s@%s: %w", side, qty, price, err)
	}
	return out.OrderID, nil
}

// MarketContract places a market order on the contract.
func (c *Client) MarketContract(ctx context.Context, user, symbol string, side domain.Side, qty decimal.Decimal, reduceOnly bool) error {
	body := map[string]any{
		"symbol": symbol, "side": string(side), "type": "MARKET", "quantity": qty.String(), "client_order_id": clientID(),
	}
	if reduceOnly {
		body["reduce_only"] = true
	}
	err := c.do(ctx, http.MethodPost, c.DerivativesURL+"/v1/derivatives/orders", user, body, nil)
	switch code(err) {
	case "DERIV_INSUFFICIENT_MARGIN", "LEDGER_INSUFFICIENT_BALANCE", "DERIV_RISK_LIMIT_EXCEEDED", "DERIV_REDUCE_ONLY_REJECTED":
		return ports.ErrFunds
	}
	if err != nil {
		return fmt.Errorf("contract market %s: %w", side, err)
	}
	return nil
}

// CancelContract asks to cancel one contract order.
func (c *Client) CancelContract(ctx context.Context, user, orderID string) error {
	err := c.do(ctx, http.MethodDelete, c.DerivativesURL+"/v1/derivatives/orders/"+url.PathEscape(orderID), user, nil, nil)
	switch code(err) {
	case "ORDER_ALREADY_FILLED", "COMMON_CONFLICT", "ORDER_NOT_FOUND":
		return nil
	}
	if err != nil {
		return fmt.Errorf("cancel contract order %s: %w", orderID, err)
	}
	return nil
}

// CancelAllContract asks to cancel every active contract order of the bot.
func (c *Client) CancelAllContract(ctx context.Context, user, symbol string) error {
	if err := c.do(ctx, http.MethodDelete, c.DerivativesURL+"/v1/derivatives/orders?"+url.Values{"symbol": {symbol}}.Encode(), user, nil, nil); err != nil {
		return fmt.Errorf("cancel all contract orders %s: %w", symbol, err)
	}
	return nil
}

// Position returns the bot's signed position on the contract.
func (c *Client) Position(ctx context.Context, user, symbol string) (decimal.Decimal, error) {
	var body struct {
		Positions []struct {
			Symbol   string `json:"symbol"`
			Quantity string `json:"quantity"`
		} `json:"positions"`
	}
	if err := c.do(ctx, http.MethodGet, c.DerivativesURL+"/v1/derivatives/positions", user, nil, &body); err != nil {
		return decimal.Zero, fmt.Errorf("positions: %w", err)
	}
	total := decimal.Zero
	for _, p := range body.Positions {
		if p.Symbol != symbol {
			continue
		}
		q, err := decimal.NewFromString(p.Quantity)
		if err != nil {
			return decimal.Zero, fmt.Errorf("position of %s: bad quantity %q", symbol, p.Quantity)
		}
		total = total.Add(q)
	}
	return total, nil
}

// Futures returns the bot's available FUTURES balance.
func (c *Client) Futures(ctx context.Context, user string) (decimal.Decimal, error) {
	var body struct {
		Available string `json:"available"`
	}
	if err := c.do(ctx, http.MethodGet, c.DerivativesURL+"/v1/derivatives/account", user, nil, &body); err != nil {
		return decimal.Zero, fmt.Errorf("futures account: %w", err)
	}
	return decimal.NewFromString(body.Available)
}

// ToFutures moves USDT from the bot's SPOT account to its FUTURES account.
func (c *Client) ToFutures(ctx context.Context, user string, amount decimal.Decimal, key string) error {
	body := map[string]string{"asset": "USDT", "amount": amount.String(), "from_account_type": "SPOT", "to_account_type": "FUTURES"}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.LedgerURL+"/v1/account/transfers", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("X-User-Id", user)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("transfer to futures: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Code == "LEDGER_INSUFFICIENT_BALANCE" {
			return ports.ErrFunds
		}
		return fmt.Errorf("transfer to futures: HTTP %d %s", resp.StatusCode, e.Code)
	}
	return nil
}
