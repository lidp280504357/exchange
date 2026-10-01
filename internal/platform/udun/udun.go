// Package udun speaks the gateway protocol of the Udun (优盾) custody
// wallet (ADR-0011, design 2026-09-30 §9.3). Every request is a POST of a
// JSON envelope {timestamp, nonce, sign, body}: body is the request as a
// JSON string, timestamp the Unix time in seconds and nonce a random
// number, both JSON numbers, and sign = md5(body + key + nonce +
// timestamp) in lower-case hex with the merchant's key. The gateway
// answers {code, message, data}, code 200 for success. Callbacks come
// back signed the same way, as a form or as JSON. wallet-service's custody
// adapter and the udun-mock test gateway share this package; the paths
// and body shapes follow Udun's SDK (github.com/0xcregis/udun-sdk-go).
package udun

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // the custodian's signature scheme, not ours to choose
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Paths of the gateway.
const (
	PathCreateAddress = "/mch/address/create"
	PathWithdraw      = "/mch/withdraw"
	PathCheckAddress  = "/mch/check/address"
	PathSupportCoins  = "/mch/support-coins"
)

// Codes of the gateway's answers.
const (
	CodeOK                = 200
	CodeInvalidAddress    = 4165
	CodeDuplicateBusiness = 4288
)

// Trade types and statuses of callbacks. A withdrawal gets at most two
// callbacks: the custodian's review (0 or 1, or 2 when it refuses) and the
// outcome (3 or 4).
const (
	TradeDeposit    = 1
	TradeWithdrawal = 2

	StatusReview   = 0
	StatusApproved = 1
	StatusRefused  = 2
	StatusSuccess  = 3
	StatusFailed   = 4
)

// CallbackReply is what a callback's receiver answers once it has
// recorded and applied it; anything else makes the custodian retry.
const CallbackReply = "success"

// Errors of envelopes.
var (
	ErrSignature = errors.New("udun: the signature does not match")
	ErrStale     = errors.New("udun: the timestamp is outside the accepted window")
	ErrMalformed = errors.New("udun: not a signed envelope")
)

// Sign returns md5(body + key + nonce + timestamp) in lower-case hex.
func Sign(body, key, nonce, timestamp string) string {
	sum := md5.Sum([]byte(body + key + nonce + timestamp)) //nolint:gosec // see the import
	return hex.EncodeToString(sum[:])
}

// Envelope is a signed request or callback; Timestamp and Nonce are kept
// as the decimal text they were signed with.
type Envelope struct {
	Timestamp string
	Nonce     string
	Sign      string
	Body      string
}

// Seal signs body with key at now under a random nonce.
func Seal(key string, body []byte, now time.Time) Envelope {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	e := Envelope{Timestamp: strconv.FormatInt(now.Unix(), 10), Nonce: strconv.FormatInt(n.Int64()+100000, 10), Body: string(body)}
	e.Sign = Sign(e.Body, key, e.Nonce, e.Timestamp)
	return e
}

// MarshalJSON writes the envelope with the timestamp and nonce as JSON
// numbers.
func (e Envelope) MarshalJSON() ([]byte, error) {
	if !isInteger(e.Timestamp) || !isInteger(e.Nonce) {
		return nil, ErrMalformed
	}
	return json.Marshal(struct {
		Timestamp json.Number `json:"timestamp"`
		Nonce     json.Number `json:"nonce"`
		Sign      string      `json:"sign"`
		Body      string      `json:"body"`
	}{json.Number(e.Timestamp), json.Number(e.Nonce), e.Sign, e.Body})
}

// UnmarshalJSON reads an envelope whose timestamp and nonce are numbers
// or strings.
func (e *Envelope) UnmarshalJSON(b []byte) error {
	var w struct {
		Timestamp Text   `json:"timestamp"`
		Nonce     Text   `json:"nonce"`
		Sign      string `json:"sign"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	*e = Envelope{Timestamp: string(w.Timestamp), Nonce: string(w.Nonce), Sign: w.Sign, Body: w.Body}
	return nil
}

// Form encodes the envelope as a form, the way the custodian posts
// callbacks.
func (e Envelope) Form() url.Values {
	return url.Values{"timestamp": {e.Timestamp}, "nonce": {e.Nonce}, "sign": {e.Sign}, "body": {e.Body}}
}

// Verify checks the signature with key and, unless window is 0, that the
// timestamp (seconds, or milliseconds for a 13-digit one) lies within
// window of now.
func (e Envelope) Verify(key string, now time.Time, window time.Duration) error {
	if e.Body == "" || !isInteger(e.Timestamp) || !isInteger(e.Nonce) || e.Sign == "" {
		return ErrMalformed
	}
	want := Sign(e.Body, key, e.Nonce, e.Timestamp)
	if subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(e.Sign))) != 1 {
		return ErrSignature
	}
	if window <= 0 {
		return nil
	}
	at, err := e.Time()
	if err != nil {
		return err
	}
	if d := now.Sub(at); d > window || d < -window {
		return ErrStale
	}
	return nil
}

// Time is when the envelope was signed.
func (e Envelope) Time() (time.Time, error) {
	ts, err := strconv.ParseInt(e.Timestamp, 10, 64)
	if err != nil {
		return time.Time{}, ErrMalformed
	}
	if ts > 1e12 {
		return time.UnixMilli(ts), nil
	}
	return time.Unix(ts, 0), nil
}

func isInteger(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

// ParseEnvelope reads a callback's envelope from its body: a form, or JSON
// when the content type says so (or the body looks like it).
func ParseEnvelope(contentType string, raw []byte) (Envelope, error) {
	trimmed := bytes.TrimSpace(raw)
	if strings.Contains(contentType, "json") || bytes.HasPrefix(trimmed, []byte("{")) {
		var e Envelope
		if err := json.Unmarshal(trimmed, &e); err != nil {
			return Envelope{}, err
		}
		return e, nil
	}
	form, err := url.ParseQuery(string(trimmed))
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return Envelope{Timestamp: form.Get("timestamp"), Nonce: form.Get("nonce"), Sign: form.Get("sign"), Body: form.Get("body")}, nil
}

// Text is a JSON string or number kept as its text: the gateway writes
// codes, amounts and statuses either way.
type Text string

// UnmarshalJSON accepts a string, a number or null.
func (t *Text) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case bytes.Equal(b, []byte("null")):
		*t = ""
	case len(b) > 0 && b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*t = Text(s)
	default:
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return fmt.Errorf("udun: %s is neither a string nor a number", b)
		}
		*t = Text(n)
	}
	return nil
}

// Error is the gateway's refusal: a code other than 200.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("udun: code %d: %s", e.Code, e.Message) }

// IsCode reports whether err is the gateway's refusal with code.
func IsCode(err error, code int) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Client calls the gateway for one merchant.
type Client struct {
	BaseURL    string
	MerchantID string
	Key        string
	HTTP       *http.Client
	Now        func() time.Time
}

// call posts body to path and decodes the answer's data into out (when
// not nil). A transport failure or a non-200 HTTP status is a plain
// error; a refusal is an *Error.
func (c *Client) call(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	payload, err := json.Marshal(Seal(c.Key, raw, now()))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("udun %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("udun %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("udun %s: HTTP %d", path, resp.StatusCode)
	}
	var r struct {
		Code    Text            `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(answer, &r); err != nil {
		return fmt.Errorf("udun %s: unreadable answer: %w", path, err)
	}
	code, err := strconv.Atoi(string(r.Code))
	if err != nil {
		return fmt.Errorf("udun %s: answer without a code", path)
	}
	if code != CodeOK {
		return &Error{Code: code, Message: r.Message}
	}
	if out == nil || len(r.Data) == 0 || string(r.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(r.Data, out); err != nil {
		return fmt.Errorf("udun %s: unreadable data: %w", path, err)
	}
	return nil
}

// Address is a deposit address the gateway created.
type Address struct {
	Address  string `json:"address"`
	CoinType Text   `json:"coinType"`
}

// CreateAddress asks for a new deposit address of the chain mainCoinType;
// the gateway posts its deposits to callURL. alias names it in the
// custodian's console.
func (c *Client) CreateAddress(ctx context.Context, mainCoinType int, callURL, walletID, alias string) (Address, error) {
	body := []map[string]any{{
		"merchantId": c.MerchantID, "mainCoinType": mainCoinType, "callUrl": callURL, "walletId": walletID, "alias": alias,
	}}
	var data json.RawMessage
	if err := c.call(ctx, PathCreateAddress, body, &data); err != nil {
		return Address{}, err
	}
	// The data is the address, or a list holding it.
	var a Address
	if err := json.Unmarshal(data, &a); err != nil {
		var list []Address
		if json.Unmarshal(data, &list) != nil || len(list) == 0 {
			return Address{}, fmt.Errorf("udun %s: no address in %s", PathCreateAddress, data)
		}
		a = list[0]
	}
	if a.Address == "" {
		return Address{}, fmt.Errorf("udun %s: no address in %s", PathCreateAddress, data)
	}
	return a, nil
}

// Withdrawal is a transfer for the gateway to send. BusinessID is unique:
// the gateway refuses a repeat with CodeDuplicateBusiness.
type Withdrawal struct {
	Address      string `json:"address"`
	Amount       string `json:"amount"`
	MerchantID   string `json:"merchantId"`
	MainCoinType string `json:"mainCoinType"`
	CoinType     string `json:"coinType"`
	CallURL      string `json:"callUrl"`
	BusinessID   string `json:"businessId"`
	WalletID     string `json:"walletId"`
	Memo         string `json:"memo"`
}

// Withdraw hands a withdrawal over.
func (c *Client) Withdraw(ctx context.Context, w Withdrawal) error {
	w.MerchantID = c.MerchantID
	return c.call(ctx, PathWithdraw, []Withdrawal{w}, nil)
}

// CheckAddress asks whether address is valid on the chain mainCoinType.
func (c *Client) CheckAddress(ctx context.Context, mainCoinType, address string) (bool, error) {
	body := []map[string]string{{"merchantId": c.MerchantID, "mainCoinType": mainCoinType, "address": address}}
	err := c.call(ctx, PathCheckAddress, body, nil)
	if IsCode(err, CodeInvalidAddress) {
		return false, nil
	}
	return err == nil, err
}

// Coin is a coin the merchant can use, with its balance when asked for.
type Coin struct {
	Name         string `json:"name"`
	Symbol       string `json:"symbol"`
	MainCoinType Text   `json:"mainCoinType"`
	CoinType     Text   `json:"coinType"`
	Decimals     Text   `json:"decimals"`
	// TokenStatus is 0 for a chain's coin, 1 for a token.
	TokenStatus Text   `json:"tokenStatus"`
	MainSymbol  string `json:"mainSymbol"`
	Balance     Text   `json:"balance"`
}

// Code is the coin's "mainCoinType:coinType", how networks name it.
func (c Coin) Code() string { return string(c.MainCoinType) + ":" + string(c.CoinType) }

// SupportCoins lists the merchant's coins, with balances when asked.
func (c *Client) SupportCoins(ctx context.Context, showBalance bool) ([]Coin, error) {
	var out []Coin
	err := c.call(ctx, PathSupportCoins, map[string]any{"merchantId": c.MerchantID, "showBalance": showBalance}, &out)
	return out, err
}

// SplitCoin splits a network's provider coin "mainCoinType:coinType".
func SplitCoin(code string) (main, coin string, err error) {
	main, coin, ok := strings.Cut(code, ":")
	if !ok || !isInteger(main) || coin == "" {
		return "", "", fmt.Errorf("udun: %q is not mainCoinType:coinType", code)
	}
	return main, coin, nil
}
