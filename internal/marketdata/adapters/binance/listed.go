package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// invalidSymbol is Binance's error code for a symbol it does not list.
const invalidSymbol = -1121

// Listed reports whether Binance lists symbol (BTCUSDT) on its spot
// market, or on USDⓈ-M futures. The admin console checks a reference
// symbol with it before a pair uses one: a symbol Binance does not know
// fails the batched ticker reads of every pair.
func (s *Source) Listed(ctx context.Context, symbol string, futures bool) (bool, error) {
	rest, _, prefix, err := s.urls(futures)
	if err != nil {
		return false, err
	}
	if err := s.wait(ctx); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rest+prefix+"/ticker/price?"+url.Values{"symbol": {symbol}}.Encode(), nil)
	if err != nil {
		return false, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("binance listed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusBadRequest:
		var body struct {
			Code int `json:"code"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Code == invalidSymbol {
			return false, nil
		}
		return false, fmt.Errorf("binance listed: HTTP 400 code %d", body.Code)
	case http.StatusTooManyRequests, http.StatusTeapot:
		s.backOff(resp)
		return false, fmt.Errorf("binance listed: HTTP %d, backing off", resp.StatusCode)
	default:
		return false, fmt.Errorf("binance listed: HTTP %d", resp.StatusCode)
	}
}
