// Package trading calls spot-trading-service's internal API for
// liquidations (only on the compose network): canceling a margin
// account's orders and placing a liquidation's market orders against
// HOUSE.
package trading

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Client implements ports.Trading.
type Client struct {
	// BaseURL is spot-trading-service's internal address
	// (SPOT_TRADING_SERVICE_URL).
	BaseURL string
	HTTP    *http.Client
}

// post sends body and decodes a 2xx answer into out; an error answer
// comes back as the apperr it carries, of the kind of its status.
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.BaseURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return apperr.Unavailable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return apperr.Unavailable(err)
	}
	if resp.StatusCode/100 == 2 {
		if out == nil {
			return nil
		}
		return json.Unmarshal(data, out)
	}
	var e httpx.ErrorBody
	_ = json.Unmarshal(data, &e)
	kind := apperr.KindUnavailable
	switch resp.StatusCode {
	case http.StatusBadRequest:
		kind = apperr.KindInvalid
	case http.StatusNotFound:
		kind = apperr.KindNotFound
	case http.StatusConflict:
		kind = apperr.KindConflict
	case http.StatusUnprocessableEntity:
		kind = apperr.KindUnprocessable
	case http.StatusForbidden:
		kind = apperr.KindForbidden
	}
	if e.Code == "" {
		e.Code, e.Message = apperr.CodeUnavailable, fmt.Sprintf("%s: HTTP %d", path, resp.StatusCode)
	}
	out2 := apperr.New(kind, e.Code, e.Message)
	for k, v := range e.Details {
		out2 = out2.WithDetail(k, v)
	}
	return out2
}

func symbolOf(a domain.Account) string {
	if a.IsCross() {
		return ""
	}
	return a.Symbol
}

// CancelAccount asks for every open order on the margin account to be
// canceled.
func (c *Client) CancelAccount(ctx context.Context, userID string, a domain.Account) error {
	return c.post(ctx, "/internal/orders/cancel", map[string]string{
		"user_id": userID, "account": string(a.Type), "symbol": symbolOf(a),
	}, nil)
}

// PlaceLiquidation places a liquidation's market order (a sell by
// quantity, a buy by quote amount; its attempt) and returns where it
// stands; the same order again answers the order as it is now (a
// rejected one its rejection).
func (c *Client) PlaceLiquidation(ctx context.Context, userID string, a domain.Account, o ports.LiquidationOrder) (ports.OrderState, error) {
	body := map[string]any{
		"liquidation_id": o.LiquidationID, "user_id": userID, "account": string(a.Type), "symbol": o.Symbol, "side": o.Side,
		"side_effect": string(domain.SideEffectNone), "attempt": max(o.Attempt, 1),
	}
	if o.Side == "SELL" {
		body["quantity"] = o.Quantity.String()
	} else {
		body["quote_amount"] = o.QuoteAmount.String()
	}
	var out struct {
		OrderID        string `json:"order_id"`
		Status         string `json:"status"`
		FilledQuantity string `json:"filled_quantity"`
		FilledQuote    string `json:"filled_quote"`
	}
	if err := c.post(ctx, "/internal/orders/liquidations", body, &out); err != nil {
		return ports.OrderState{}, err
	}
	st := ports.OrderState{OrderID: out.OrderID, Status: out.Status}
	var err error
	if st.FilledQuantity, err = decimal.NewFromString(out.FilledQuantity); err == nil {
		st.FilledQuote, err = decimal.NewFromString(out.FilledQuote)
	}
	if out.OrderID == "" || out.Status == "" || err != nil {
		return ports.OrderState{}, apperr.Unavailable(fmt.Errorf("liquidation order of %s: an answer without its order, status or fills",
			o.LiquidationID))
	}
	return st, nil
}
