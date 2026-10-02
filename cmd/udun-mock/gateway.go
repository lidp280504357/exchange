package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/udun"
)

// coin is a coin the merchant can use, with what the gateway holds of it.
type coin struct {
	Symbol       string          `json:"symbol"`
	MainCoinType string          `json:"main_coin_type"`
	CoinType     string          `json:"coin_type"`
	Decimals     int32           `json:"decimals"`
	Balance      decimal.Decimal `json:"balance"`
}

func (c coin) code() string { return c.MainCoinType + ":" + c.CoinType }
func (c coin) token() bool  { return c.MainCoinType != c.CoinType }
func (c coin) mainSymbol() string {
	switch c.MainCoinType {
	case chainBTC:
		return "BTC"
	case chainTRON:
		return "TRX"
	case "9006":
		return "BNB"
	}
	return "ETH"
}

type address struct {
	MainCoinType string    `json:"main_coin_type"`
	CallURL      string    `json:"call_url"`
	Alias        string    `json:"alias"`
	CreatedAt    time.Time `json:"created_at"`
}

type withdrawal struct {
	BusinessID string          `json:"business_id"`
	Address    string          `json:"address"`
	Amount     decimal.Decimal `json:"amount"`
	Coin       string          `json:"coin"`
	CallURL    string          `json:"call_url"`
	TradeID    string          `json:"trade_id"`
	Status     int             `json:"status"` // -1 until reviewed
	TxID       string          `json:"tx_id"`
	CreatedAt  time.Time       `json:"created_at"`
	// Held: its answer was lost, and its callbacks wait for the
	// hand-over to come again (quirk LoseAnswer).
	Held bool `json:"held,omitempty"`
}

// quirk is how the gateway misbehaves for withdrawals to an address, to
// take the platform down the paths a gateway that behaves never does
// (review B7): a fee on the callbacks in the coin's smallest unit, a
// review (status 0) before the approval, the first hand-over taken but its
// answer lost (HTTP 502; the callbacks wait until it is handed over
// again), and a repeated business ID refused with RepeatCode rather than
// 4288 (as a gateway that checks the balance first, which the first
// hand-over took, would say "insufficient balance").
type quirk struct {
	Fee        string `json:"fee,omitempty"`
	Review     bool   `json:"review,omitempty"`
	LoseAnswer bool   `json:"lose_answer,omitempty"`
	RepeatCode int    `json:"repeat_code,omitempty"`
}

// callback is a callback to deliver; one that is not answered "success"
// is tried again later, the way the custodian does.
type callback struct {
	URL      string     `json:"url"`
	Trade    udun.Trade `json:"trade"`
	Due      time.Time  `json:"due"`
	Attempts int        `json:"attempts"`
}

// state is everything the gateway keeps, saved to a file after each
// change.
type state struct {
	Coins       []coin                 `json:"coins"`
	Addresses   map[string]address     `json:"addresses"`
	Withdrawals map[string]*withdrawal `json:"withdrawals"`
	// Outcomes say how withdrawals to an address end: 2 refused, 3 sent
	// (the default), 4 failed on chain; Quirks how the gateway misbehaves
	// on the way.
	Outcomes map[string]int   `json:"outcomes"`
	Quirks   map[string]quirk `json:"quirks,omitempty"`
	// Pending callbacks, and the last ones delivered for replays.
	Pending   []*callback `json:"pending"`
	Delivered []*callback `json:"delivered"`
	// Delay holds callbacks back (a fault drill).
	Delay time.Duration `json:"delay"`
	Seq   int64         `json:"seq"`
	Block int64         `json:"block"`
}

type gateway struct {
	merchant string
	key      string
	path     string
	step     time.Duration
	client   *http.Client
	log      *slog.Logger
	now      func() time.Time

	mu sync.Mutex
	st state
}

// defaultCoins mirror the custody networks of deploy/instruments/test.json.
func defaultCoins() []coin {
	return []coin{
		{Symbol: "USDT", MainCoinType: chainTRON, CoinType: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Decimals: 6},
		{Symbol: "USDT", MainCoinType: "9006", CoinType: "0x55d398326f99059fF775485246999027B3197955", Decimals: 18},
		{Symbol: "USDT", MainCoinType: "60", CoinType: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6},
		{Symbol: "BTC", MainCoinType: chainBTC, CoinType: chainBTC, Decimals: 8},
		{Symbol: "ETH", MainCoinType: "60", CoinType: "60", Decimals: 18},
	}
}

// parseCoins reads UDUN_MOCK_COINS: SYMBOL:MAIN:COIN:DECIMALS,...
func parseCoins(s string) ([]coin, error) {
	if strings.TrimSpace(s) == "" {
		return defaultCoins(), nil
	}
	var out []coin
	for _, f := range strings.Split(s, ",") {
		p := strings.Split(strings.TrimSpace(f), ":")
		if len(p) != 4 {
			return nil, fmt.Errorf("UDUN_MOCK_COINS: %q is not SYMBOL:MAIN:COIN:DECIMALS", f)
		}
		d, err := strconv.ParseInt(p[3], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("UDUN_MOCK_COINS: %q: %w", f, err)
		}
		out = append(out, coin{Symbol: p[0], MainCoinType: p[1], CoinType: p[2], Decimals: int32(d)})
	}
	return out, nil
}

func newGateway(merchant, key, path string, coins []coin, step time.Duration, log *slog.Logger) (*gateway, error) {
	g := &gateway{
		merchant: merchant, key: key, path: path, step: step, client: &http.Client{Timeout: 10 * time.Second}, log: log, now: time.Now,
		st: state{Addresses: map[string]address{}, Withdrawals: map[string]*withdrawal{}, Outcomes: map[string]int{}, Block: 1_000_000},
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(raw, &g.st); err != nil {
				return nil, fmt.Errorf("state %s: %w", path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}
	// Configured coins are added; balances of known ones are kept.
	for _, c := range coins {
		if !slices.ContainsFunc(g.st.Coins, func(x coin) bool { return x.code() == c.code() }) {
			g.st.Coins = append(g.st.Coins, c)
		}
	}
	return g, g.saveLocked()
}

// saveLocked writes the state; the caller holds mu.
func (g *gateway) saveLocked() error {
	if g.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(g.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(g.path), ".state.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, g.path)
}

func (g *gateway) coinLocked(code string) *coin {
	for i := range g.st.Coins {
		if g.st.Coins[i].code() == code {
			return &g.st.Coins[i]
		}
	}
	return nil
}

func (g *gateway) nextTradeLocked() string {
	g.st.Seq++
	return strconv.FormatInt(g.now().UnixMilli()*1000+g.st.Seq%1000, 10)
}

// routes mounts the gateway's API and the mock's own controls.
func (g *gateway) routes(r chi.Router) {
	r.Post(udun.PathCreateAddress, g.signed(g.createAddress))
	r.Post(udun.PathWithdraw, g.signed(g.withdraw))
	r.Post(udun.PathCheckAddress, g.signed(g.checkAddress))
	r.Post(udun.PathSupportCoins, g.signed(g.supportCoins))
	r.Post("/mock/deposit", g.mockDeposit)
	r.Post("/mock/outcome", g.mockOutcome)
	r.Post("/mock/delay", g.mockDelay)
	r.Post("/mock/replay", g.mockReplay)
	r.Get("/mock/state", g.mockState)
}

type answer struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
	// lost answers 502 instead, as if the answer never arrived.
	lost bool
}

func reply(w http.ResponseWriter, a answer) {
	if a.lost {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a)
}

// signed checks a request's envelope (the merchant's key, five minutes)
// and hands its body to fn.
func (g *gateway) signed(fn func(body []byte) answer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			reply(w, answer{Code: 400, Message: "unreadable request"})
			return
		}
		var e udun.Envelope
		if err := json.Unmarshal(raw, &e); err != nil {
			reply(w, answer{Code: 400, Message: "not an envelope"})
			return
		}
		if err := e.Verify(g.key, g.now(), 5*time.Minute); err != nil {
			reply(w, answer{Code: 401, Message: err.Error()})
			return
		}
		reply(w, fn([]byte(e.Body)))
	}
}

func (g *gateway) createAddress(body []byte) answer {
	var reqs []struct {
		MerchantID   string    `json:"merchantId"`
		MainCoinType udun.Text `json:"mainCoinType"`
		CallURL      string    `json:"callUrl"`
		Alias        string    `json:"alias"`
	}
	if err := json.Unmarshal(body, &reqs); err != nil || len(reqs) != 1 {
		return answer{Code: 400, Message: "one address request expected"}
	}
	req := reqs[0]
	if req.MerchantID != g.merchant {
		return answer{Code: 403, Message: "unknown merchant"}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !slices.ContainsFunc(g.st.Coins, func(c coin) bool { return c.MainCoinType == string(req.MainCoinType) }) {
		return answer{Code: 4005, Message: "unsupported main coin"}
	}
	a := newAddress(string(req.MainCoinType))
	g.st.Addresses[a] = address{MainCoinType: string(req.MainCoinType), CallURL: req.CallURL, Alias: req.Alias, CreatedAt: g.now()}
	if err := g.saveLocked(); err != nil {
		return answer{Code: 500, Message: err.Error()}
	}
	g.log.Info("address created", "address", a, "main_coin_type", req.MainCoinType, "alias", req.Alias)
	return answer{Code: udun.CodeOK, Message: "SUCCESS", Data: map[string]any{"address": a, "coinType": req.MainCoinType}}
}

func (g *gateway) checkAddress(body []byte) answer {
	var reqs []struct {
		MainCoinType udun.Text `json:"mainCoinType"`
		Address      string    `json:"address"`
	}
	if err := json.Unmarshal(body, &reqs); err != nil || len(reqs) != 1 {
		return answer{Code: 400, Message: "one address expected"}
	}
	if !validAddress(string(reqs[0].MainCoinType), reqs[0].Address) {
		return answer{Code: udun.CodeInvalidAddress, Message: "invalid address"}
	}
	return answer{Code: udun.CodeOK, Message: "SUCCESS"}
}

func (g *gateway) supportCoins(body []byte) answer {
	var req struct {
		MerchantID  string `json:"merchantId"`
		ShowBalance bool   `json:"showBalance"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.MerchantID != g.merchant {
		return answer{Code: 403, Message: "unknown merchant"}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]map[string]any, 0, len(g.st.Coins))
	for _, c := range g.st.Coins {
		token := 0
		if c.token() {
			token = 1
		}
		m := map[string]any{
			"name": c.Symbol, "symbol": c.Symbol, "mainCoinType": c.MainCoinType, "coinType": c.CoinType, "decimals": strconv.Itoa(int(c.Decimals)),
			"tokenStatus": token, "mainSymbol": c.mainSymbol(),
		}
		if req.ShowBalance {
			m["balance"] = c.Balance.String()
		}
		out = append(out, m)
	}
	return answer{Code: udun.CodeOK, Message: "SUCCESS", Data: out}
}

// errInsufficient is the mock's refusal of a withdrawal it cannot cover.
const errInsufficient = 4001

func (g *gateway) withdraw(body []byte) answer {
	var reqs []udun.Withdrawal
	if err := json.Unmarshal(body, &reqs); err != nil || len(reqs) != 1 {
		return answer{Code: 400, Message: "one withdrawal expected"}
	}
	req := reqs[0]
	amount, err := decimal.NewFromString(req.Amount)
	switch {
	case req.MerchantID != g.merchant:
		return answer{Code: 403, Message: "unknown merchant"}
	case err != nil || !amount.IsPositive():
		return answer{Code: 4002, Message: "bad amount"}
	case req.BusinessID == "":
		return answer{Code: 4003, Message: "businessId is required"}
	case !validAddress(req.MainCoinType, req.Address):
		return answer{Code: udun.CodeInvalidAddress, Message: "invalid address"}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if wd, dup := g.st.Withdrawals[req.BusinessID]; dup {
		q := g.st.Quirks[wd.Address]
		if wd.Held {
			// Handed over again: the first one goes ahead now.
			wd.Held = false
			if c := g.coinLocked(wd.Coin); c != nil {
				g.queueWithdrawalLocked(*c, wd)
			}
			_ = g.saveLocked()
		}
		if q.RepeatCode != 0 {
			return answer{Code: q.RepeatCode, Message: "insufficient balance"}
		}
		return answer{Code: udun.CodeDuplicateBusiness, Message: "duplicate businessId"}
	}
	c := g.coinLocked(req.MainCoinType + ":" + req.CoinType)
	switch {
	case c == nil:
		return answer{Code: 4005, Message: "unsupported coin"}
	case c.Balance.LessThan(amount):
		return answer{Code: errInsufficient, Message: "insufficient balance"}
	}
	c.Balance = c.Balance.Sub(amount)
	wd := &withdrawal{
		BusinessID: req.BusinessID, Address: req.Address, Amount: amount, Coin: c.code(), CallURL: req.CallURL, TradeID: g.nextTradeLocked(),
		Status: -1, CreatedAt: g.now(),
	}
	g.st.Withdrawals[wd.BusinessID] = wd
	lost := g.st.Quirks[req.Address].LoseAnswer
	if lost {
		wd.Held = true
	} else {
		g.queueWithdrawalLocked(*c, wd)
	}
	if err := g.saveLocked(); err != nil {
		return answer{Code: 500, Message: err.Error()}
	}
	g.log.Info("withdrawal taken", "business_id", wd.BusinessID, "amount", amount.String(), "coin", wd.Coin, "answer_lost", lost)
	return answer{Code: udun.CodeOK, Message: "SUCCESS", lost: lost}
}

// queueWithdrawalLocked queues a withdrawal's callbacks: its review first
// (a review in progress before it when the address's quirk says so); a
// refusal ends it there, else the transfer's outcome follows.
func (g *gateway) queueWithdrawalLocked(c coin, wd *withdrawal) {
	outcome, ok := g.st.Outcomes[wd.Address]
	if !ok {
		outcome = udun.StatusSuccess
	}
	at := g.step
	if g.st.Quirks[wd.Address].Review {
		g.queueLocked(wd.CallURL, g.withdrawalTrade(c, wd, udun.StatusReview), at)
		at += g.step
	}
	first := udun.StatusApproved
	if outcome == udun.StatusRefused {
		first = udun.StatusRefused
	}
	g.queueLocked(wd.CallURL, g.withdrawalTrade(c, wd, first), at)
	if first != udun.StatusRefused {
		g.queueLocked(wd.CallURL, g.withdrawalTrade(c, wd, outcome), at+g.step)
	}
}

func (g *gateway) withdrawalTrade(c coin, wd *withdrawal, status int) udun.Trade {
	fee := g.st.Quirks[wd.Address].Fee
	if fee == "" {
		fee = "0"
	}
	t := udun.Trade{
		Address: udun.Text(wd.Address), Amount: udun.Text(wd.Amount.Shift(c.Decimals).String()), Fee: udun.Text(fee),
		Decimals: udun.Text(strconv.Itoa(int(c.Decimals))), CoinType: udun.Text(c.CoinType), MainCoinType: udun.Text(c.MainCoinType),
		BusinessID: udun.Text(wd.BusinessID), Status: udun.Text(strconv.Itoa(status)), TradeID: udun.Text(wd.TradeID),
		TradeType: udun.Text(strconv.Itoa(udun.TradeWithdrawal)),
	}
	if status == udun.StatusSuccess || status == udun.StatusFailed {
		if wd.TxID == "" {
			wd.TxID = newTxID(c.MainCoinType)
		}
		t.TxID = udun.Text(wd.TxID)
		g.st.Block++
		t.BlockHigh = udun.Text(strconv.FormatInt(g.st.Block, 10))
	}
	return t
}

func (g *gateway) queueLocked(url string, t udun.Trade, after time.Duration) {
	g.st.Pending = append(g.st.Pending, &callback{URL: url, Trade: t, Due: g.now().Add(after + g.st.Delay)})
}

// deliver sends the callbacks that are due, every second, and settles
// the withdrawals they report: a refused or failed one gives its amount
// back to the balance.
func (g *gateway) deliver(ctx context.Context) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		g.flush(ctx)
	}
}

// flush sends the callbacks that are due now.
func (g *gateway) flush(ctx context.Context) {
	g.mu.Lock()
	var due []*callback
	now := g.now()
	for _, c := range g.st.Pending {
		if !c.Due.After(now) {
			due = append(due, c)
		}
	}
	g.mu.Unlock()
	for _, c := range due {
		g.send(ctx, c)
	}
}

// send delivers one callback; an unanswered one is tried again after
// 5 s, 10 s, 20 s ... up to ten minutes apart.
func (g *gateway) send(ctx context.Context, c *callback) {
	body, err := json.Marshal(c.Trade)
	if err != nil {
		return
	}
	env := udun.Seal(g.key, body, g.now())
	ok, err := udun.Post(ctx, g.client, c.URL, env)
	g.mu.Lock()
	defer g.mu.Unlock()
	c.Attempts++
	if !ok {
		backoff := min(5*time.Second<<min(c.Attempts-1, 7), 10*time.Minute)
		c.Due = g.now().Add(backoff)
		g.log.Warn("callback not accepted, retrying", "trade_id", c.Trade.TradeID, "status", c.Trade.Status, "attempts", c.Attempts,
			"retry_in", backoff.String(), "error", err)
		_ = g.saveLocked()
		return
	}
	g.st.Pending = slices.DeleteFunc(g.st.Pending, func(x *callback) bool { return x == c })
	g.st.Delivered = append(g.st.Delivered, c)
	if len(g.st.Delivered) > 50 {
		g.st.Delivered = g.st.Delivered[len(g.st.Delivered)-50:]
	}
	if wd := g.st.Withdrawals[string(c.Trade.BusinessID)]; wd != nil && c.Trade.TradeType == udun.Text(strconv.Itoa(udun.TradeWithdrawal)) {
		status, _ := strconv.Atoi(string(c.Trade.Status))
		wd.Status = status
		if status == udun.StatusRefused || status == udun.StatusFailed {
			if coin := g.coinLocked(wd.Coin); coin != nil {
				coin.Balance = coin.Balance.Add(wd.Amount)
			}
		}
	}
	g.log.Info("callback delivered", "trade_id", c.Trade.TradeID, "trade_type", c.Trade.TradeType, "status", c.Trade.Status,
		"attempts", c.Attempts)
	_ = g.saveLocked()
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// mockDeposit reports a confirmed deposit to one of the gateway's
// addresses: {"address", "coin" (MAIN:COIN, default the address's chain
// coin), "amount"}.
func (g *gateway) mockDeposit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
		Coin    string `json:"coin"`
		Amount  string `json:"amount"`
	}
	if !decode(w, r, &req) {
		return
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || !amount.IsPositive() {
		http.Error(w, "amount must be a positive decimal", http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.st.Addresses[req.Address]
	if !ok {
		http.Error(w, "not an address of this gateway", http.StatusNotFound)
		return
	}
	code := req.Coin
	if code == "" {
		code = a.MainCoinType + ":" + a.MainCoinType
	}
	c := g.coinLocked(code)
	if c == nil || c.MainCoinType != a.MainCoinType {
		http.Error(w, "the address's chain has no coin "+code, http.StatusBadRequest)
		return
	}
	if !amount.Equal(amount.Truncate(c.Decimals)) {
		http.Error(w, "more decimals than the coin has", http.StatusBadRequest)
		return
	}
	c.Balance = c.Balance.Add(amount)
	g.st.Block++
	t := udun.Trade{
		Address: udun.Text(req.Address), Amount: udun.Text(amount.Shift(c.Decimals).String()), Fee: "0",
		Decimals: udun.Text(strconv.Itoa(int(c.Decimals))), CoinType: udun.Text(c.CoinType), MainCoinType: udun.Text(c.MainCoinType),
		BlockHigh: udun.Text(strconv.FormatInt(g.st.Block, 10)), Status: udun.Text(strconv.Itoa(udun.StatusSuccess)),
		TradeID: udun.Text(g.nextTradeLocked()), TradeType: udun.Text(strconv.Itoa(udun.TradeDeposit)),
		TxID: udun.Text(newTxID(c.MainCoinType)),
	}
	g.queueLocked(a.CallURL, t, 0)
	if err := g.saveLocked(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"trade_id": string(t.TradeID), "tx_id": string(t.TxID)})
}

// mockOutcome sets how withdrawals to an address end: {"address",
// "status": 2 refused, 3 sent, 4 failed}, and how the gateway misbehaves
// on the way (the quirk's fields beside them; none clears it).
func (g *gateway) mockOutcome(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
		Status  int    `json:"status"`
		quirk
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Status < udun.StatusRefused || req.Status > udun.StatusFailed {
		http.Error(w, "status is 2, 3 or 4", http.StatusBadRequest)
		return
	}
	if req.Fee != "" {
		if f, err := decimal.NewFromString(req.Fee); err != nil || f.IsNegative() || !f.Equal(f.Truncate(0)) {
			http.Error(w, "fee is a whole number of the coin's smallest unit", http.StatusBadRequest)
			return
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st.Outcomes[req.Address] = req.Status
	if g.st.Quirks == nil {
		g.st.Quirks = map[string]quirk{}
	}
	if req.quirk == (quirk{}) {
		delete(g.st.Quirks, req.Address)
	} else {
		g.st.Quirks[req.Address] = req.quirk
	}
	_ = g.saveLocked()
	writeJSON(w, map[string]any{"address": req.Address, "status": req.Status, "quirk": req.quirk})
}

// mockDelay holds callbacks back: {"seconds"}; 0 restores.
func (g *gateway) mockDelay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds int `json:"seconds"`
	}
	if !decode(w, r, &req) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st.Delay = time.Duration(max(req.Seconds, 0)) * time.Second
	_ = g.saveLocked()
	writeJSON(w, map[string]any{"delay_seconds": int(g.st.Delay.Seconds())})
}

// mockReplay sends a delivered callback again: {"trade_id" (default the
// last one), "age_seconds" to sign it that long ago, "forge" to sign it
// with another key}. It answers what the receiver said.
func (g *gateway) mockReplay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TradeID    string `json:"trade_id"`
		AgeSeconds int    `json:"age_seconds"`
		Forge      bool   `json:"forge"`
	}
	if !decode(w, r, &req) {
		return
	}
	g.mu.Lock()
	var c *callback
	for i := len(g.st.Delivered) - 1; i >= 0; i-- {
		if req.TradeID == "" || string(g.st.Delivered[i].Trade.TradeID) == req.TradeID {
			c = g.st.Delivered[i]
			break
		}
	}
	g.mu.Unlock()
	if c == nil {
		http.Error(w, "no such delivered callback", http.StatusNotFound)
		return
	}
	body, err := json.Marshal(c.Trade)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key := g.key
	if req.Forge {
		key = "not-" + g.key
	}
	ok, err := udun.Post(r.Context(), g.client, c.URL, udun.Seal(key, body, g.now().Add(-time.Duration(req.AgeSeconds)*time.Second)))
	out := map[string]any{"trade_id": c.Trade.TradeID, "status": c.Trade.Status, "accepted": ok}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, out)
}

func (g *gateway) mockState(w http.ResponseWriter, _ *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	writeJSON(w, map[string]any{
		"coins": g.st.Coins, "addresses": len(g.st.Addresses), "withdrawals": len(g.st.Withdrawals), "pending": g.st.Pending,
		"delay_seconds": int(g.st.Delay.Seconds()), "outcomes": g.st.Outcomes,
	})
}
