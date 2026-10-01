package udun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Trade is the body of a callback: a deposit (TradeDeposit) to one of the
// merchant's addresses or the progress of a withdrawal (TradeWithdrawal,
// BusinessID being the merchant's ID of it). Amount and Fee are integers
// in units of 10^-Decimals.
type Trade struct {
	Address      Text `json:"address"`
	Amount       Text `json:"amount"`
	Fee          Text `json:"fee"`
	Decimals     Text `json:"decimals"`
	CoinType     Text `json:"coinType"`
	MainCoinType Text `json:"mainCoinType"`
	BusinessID   Text `json:"businessId"`
	BlockHigh    Text `json:"blockHigh"`
	Status       Text `json:"status"`
	TradeID      Text `json:"tradeId"`
	TradeType    Text `json:"tradeType"`
	TxID         Text `json:"txId"`
	Memo         Text `json:"memo"`
}

// MarshalJSON writes the numeric fields as numbers, as the gateway does.
func (t Trade) MarshalJSON() ([]byte, error) {
	num := func(s Text) json.Number {
		if s == "" {
			return "0"
		}
		return json.Number(s)
	}
	return json.Marshal(struct {
		Address      string      `json:"address"`
		Amount       string      `json:"amount"`
		Fee          string      `json:"fee"`
		Decimals     json.Number `json:"decimals"`
		CoinType     string      `json:"coinType"`
		MainCoinType string      `json:"mainCoinType"`
		BusinessID   string      `json:"businessId"`
		BlockHigh    json.Number `json:"blockHigh"`
		Status       json.Number `json:"status"`
		TradeID      string      `json:"tradeId"`
		TradeType    json.Number `json:"tradeType"`
		TxID         string      `json:"txId"`
		Memo         string      `json:"memo"`
	}{
		string(t.Address), string(t.Amount), string(t.Fee), num(t.Decimals), string(t.CoinType), string(t.MainCoinType),
		string(t.BusinessID), num(t.BlockHigh), num(t.Status), string(t.TradeID), num(t.TradeType), string(t.TxID), string(t.Memo),
	})
}

// Coin is the trade's "mainCoinType:coinType".
func (t Trade) Coin() string { return string(t.MainCoinType) + ":" + string(t.CoinType) }

// Kind returns the trade type and status.
func (t Trade) Kind() (tradeType, status int, err error) {
	if tradeType, err = strconv.Atoi(string(t.TradeType)); err != nil {
		return 0, 0, fmt.Errorf("udun: bad tradeType %q", t.TradeType)
	}
	if status, err = strconv.Atoi(string(t.Status)); err != nil {
		return 0, 0, fmt.Errorf("udun: bad status %q", t.Status)
	}
	return tradeType, status, nil
}

// Value returns the amount and fee in the coin's unit.
func (t Trade) Value() (amount, fee decimal.Decimal, err error) {
	decimals := int32(0)
	if t.Decimals != "" {
		d, err := strconv.ParseInt(string(t.Decimals), 10, 32)
		if err != nil || d < 0 || d > 36 {
			return decimal.Zero, decimal.Zero, fmt.Errorf("udun: bad decimals %q", t.Decimals)
		}
		decimals = int32(d)
	}
	scaled := func(s Text, name string) (decimal.Decimal, error) {
		if s == "" {
			return decimal.Zero, nil
		}
		v, err := decimal.NewFromString(string(s))
		if err != nil || v.IsNegative() {
			return decimal.Zero, fmt.Errorf("udun: bad %s %q", name, s)
		}
		return v.Shift(-decimals), nil
	}
	if amount, err = scaled(t.Amount, "amount"); err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	if fee, err = scaled(t.Fee, "fee"); err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	return amount, fee, nil
}

// ParseTrade decodes a verified envelope's trade.
func ParseTrade(e Envelope) (Trade, error) {
	var t Trade
	if err := json.Unmarshal([]byte(e.Body), &t); err != nil {
		return Trade{}, fmt.Errorf("%w: the body is not a trade: %w", ErrMalformed, err)
	}
	if t.TradeID == "" {
		return Trade{}, fmt.Errorf("%w: the trade has no tradeId", ErrMalformed)
	}
	return t, nil
}

// Notify posts a trade to url signed with key, as a form the way the
// gateway does, and reports whether the receiver answered CallbackReply.
// The test gateway uses it.
func Notify(ctx context.Context, client *http.Client, url, key string, t Trade, now time.Time) (bool, error) {
	body, err := json.Marshal(t)
	if err != nil {
		return false, err
	}
	return Post(ctx, client, url, Seal(key, body, now))
}

// Post posts a signed envelope as a form and reports whether the receiver
// answered CallbackReply.
func Post(ctx context.Context, client *http.Client, url string, e Envelope) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(e.Form().Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK && strings.TrimSpace(string(answer)) == CallbackReply, nil
}
