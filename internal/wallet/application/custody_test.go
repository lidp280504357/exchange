package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

func (m *memStore) Callbacks() ports.CallbackRepo { return memCallbacks{m} }

func (a memAddresses) Lock(context.Context, string, string) error { return nil }

func (a memAddresses) Owner(_ context.Context, network, address string) (string, error) {
	for _, x := range a.m.addresses {
		if x.Network == network && strings.EqualFold(x.Address, address) {
			return x.UserID, nil
		}
	}
	return "", nil
}

func (a memAddresses) Count(_ context.Context, networks []string) (int, error) {
	n := 0
	for _, x := range a.m.addresses {
		if slices.Contains(networks, x.Network) {
			n++
		}
	}
	return n, nil
}

func (d memDeposits) ByProviderTx(_ context.Context, id string) (*domain.Deposit, error) {
	for _, x := range d.m.deposits {
		if x.ProviderTxID == id {
			return &x, nil
		}
	}
	return nil, nil
}

func (w memWithdrawals) Submitted(_ context.Context, provider string) ([]domain.Withdrawal, error) {
	return w.all(func(x domain.Withdrawal) bool {
		return x.Status == domain.WithdrawalSubmitted && x.Provider == provider
	}), nil
}

func (w memWithdrawals) Outstanding(_ context.Context, provider string) ([]domain.Withdrawal, error) {
	return w.all(func(x domain.Withdrawal) bool {
		return x.Provider == provider && (x.Status == domain.WithdrawalSubmitted || (x.Status == domain.WithdrawalConfirmed && x.SettleJournal == ""))
	}), nil
}

type memCallbacks struct{ m *memStore }

func (c memCallbacks) Receive(_ context.Context, x domain.Callback) (domain.Callback, bool, error) {
	if x.SignatureOK {
		for i, y := range c.m.callbacks {
			if y.SignatureOK && y.Provider == x.Provider && y.TradeID == x.TradeID && y.Status == x.Status {
				c.m.callbacks[i].Attempts++
				for _, ip := range x.RemoteIPs {
					if ips := c.m.callbacks[i].RemoteIPs; !slices.Contains(ips, ip) {
						c.m.callbacks[i].RemoteIPs = append(ips, ip)[max(0, len(ips)+1-domain.MaxRemoteIPs):]
					}
				}
				return c.m.callbacks[i], false, nil
			}
		}
	}
	x.Attempts = 1
	c.m.callbacks = append(c.m.callbacks, x)
	return x, true, nil
}

func (c memCallbacks) Finish(_ context.Context, id, result, detail string, at time.Time) error {
	for i, y := range c.m.callbacks {
		if y.ID == id {
			c.m.callbacks[i].Result, c.m.callbacks[i].Detail, c.m.callbacks[i].ProcessedAt = result, detail, at
		}
	}
	return nil
}

func (c memCallbacks) Get(_ context.Context, id string) (*domain.Callback, error) {
	for _, y := range c.m.callbacks {
		if y.ID == id {
			return &y, nil
		}
	}
	return nil, nil
}

func (c memCallbacks) Page(context.Context, ports.CallbackFilter) ([]domain.Callback, error) {
	out := slices.Clone(c.m.callbacks)
	slices.Reverse(out)
	return out, nil
}

func (c memCallbacks) RejectedSince(_ context.Context, t time.Time) (int, error) {
	n := 0
	for _, y := range c.m.callbacks {
		if y.Result == domain.CallbackRejected && !y.ReceivedAt.Before(t) {
			n++
		}
	}
	return n, nil
}

func (c memCallbacks) Attention(context.Context) (int, time.Time, error) {
	n := 0
	for _, y := range c.m.callbacks {
		if y.SignatureOK && (y.Result == domain.CallbackFailed || y.Result == domain.CallbackUnmatched) {
			n++
		}
	}
	return n, time.Time{}, nil
}

// fakeCustody is a custodian whose callbacks are the JSON of a
// ports.CustodyTrade, "bad:" before it for a wrong signature and "stale:"
// for an old one.
type fakeCustody struct {
	// onCreate runs while an address is created (a concurrent request),
	// onSubmit while a withdrawal is handed over (a callback meanwhile).
	onCreate  func()
	onSubmit  func(id string)
	created   int
	submitted []string
	refuse    bool
	down      bool
	invalid   map[string]bool
	coins     []ports.CustodyCoin
}

func (f *fakeCustody) Provider() string { return domain.ProviderUdun }

func (f *fakeCustody) CreateAddress(context.Context, domain.Network, string) (string, error) {
	if f.down {
		return "", errors.New("gateway down")
	}
	f.created++
	n := f.created
	if f.onCreate != nil {
		f.onCreate()
	}
	return []string{"TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7", "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"}[n-1], nil
}

func (f *fakeCustody) Submit(_ context.Context, w domain.Withdrawal, _ domain.Network) error {
	if f.down {
		return errors.New("gateway down")
	}
	if f.onSubmit != nil {
		f.onSubmit(w.ID)
	}
	if f.refuse {
		return domain.ErrCustodyRefused.WithDetail("reason", "code 4001: balance too low")
	}
	f.submitted = append(f.submitted, w.ID)
	return nil
}

func (f *fakeCustody) CheckAddress(_ context.Context, _ domain.Network, address string) (bool, error) {
	return !f.invalid[address], nil
}

func (f *fakeCustody) Coins(context.Context) ([]ports.CustodyCoin, error) {
	if f.down {
		return nil, errors.New("gateway down")
	}
	return f.coins, nil
}

func (f *fakeCustody) Parse(_ string, raw []byte, _ time.Time, window time.Duration) (ports.CustodyTrade, error) {
	s := string(raw)
	if strings.HasPrefix(s, "bad:") {
		return ports.CustodyTrade{TradeID: "forged"}, domain.ErrCallbackSignature
	}
	if rest, stale := strings.CutPrefix(s, "stale:"); stale {
		if window > 0 {
			return ports.CustodyTrade{}, domain.ErrCallbackStale
		}
		s = rest
	}
	var t ports.CustodyTrade
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return t, domain.ErrCallbackMalformed
	}
	return t, nil
}

const (
	tron     = "TRON"
	usdtCoin = "195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	payeeTRX = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
)

type custodyHarness struct {
	*withdrawHarness
	custody *fakeCustody
	cproc   *CustodyProcessor
	reg     *prometheus.Registry
	// callbackFrom is the address the custodian's callbacks come from.
	callbackFrom string
}

func newCustodyHarness(t *testing.T) *custodyHarness {
	w := newWithdrawHarness(t)
	w.nets.nets = append(w.nets.nets, domain.Network{
		Asset: "USDT", Network: tron, Chain: "tron", Contract: payeeTRX, Decimals: 6, Confirmations: 20, MinDeposit: d("1"),
		Enabled: true, WithdrawEnabled: true, MinWithdraw: d("10"), WithdrawFee: d("1"), AddressFormat: domain.FormatTRON,
		Provider: domain.ProviderUdun, ProviderCoin: usdtCoin,
	})
	w.ledger.available["alice"] = d("1000")
	w.svc.W.Prices = fakePrices{usdt: d("1")}
	// The custodian reports its balance of every coin, as a real one does.
	none := decimal.Zero
	c := &fakeCustody{invalid: map[string]bool{}, coins: []ports.CustodyCoin{{Code: usdtCoin, Symbol: "USDT", Decimals: 6, Token: true, Balance: &none}}}
	w.svc.Custodians = map[string]ports.Custody{domain.ProviderUdun: c}
	h := &custodyHarness{withdrawHarness: w, custody: c, reg: prometheus.NewRegistry(), callbackFrom: "203.0.113.10"}
	h.cproc = NewCustodyProcessor(CustodyProcessor{
		Store: w.store, Ledger: w.ledger, Networks: w.nets, Eligibility: w.elig, Custody: c, Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return w.now },
	}, h.reg)
	return h
}

func (h *custodyHarness) callback(t *testing.T, tr ports.CustodyTrade) domain.Callback {
	t.Helper()
	if tr.Decimals == 0 { // as the custodian counts the coin, unless a test says otherwise
		for _, c := range h.custody.coins {
			if c.Code == tr.Coin {
				tr.Decimals = c.Decimals
			}
		}
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := h.svc.HandleCallback(context.Background(), domain.ProviderUdun, "application/json", h.callbackFrom, raw)
	if err != nil {
		t.Fatalf("callback %s: %v", tr.TradeID, err)
	}
	return cb
}

func (h *custodyHarness) cround(t *testing.T) {
	t.Helper()
	if err := h.cproc.Round(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCustodyDepositAddress(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	a, _, err := h.svc.DepositAddress(ctx, "alice", "USDT", tron)
	if err != nil || a.Address != "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7" || a.Provider != domain.ProviderUdun {
		t.Fatalf("address %+v %v", a, err)
	}
	again, _, err := h.svc.DepositAddress(ctx, "alice", "USDT", tron)
	if err != nil || again.Address != a.Address || h.custody.created != 1 {
		t.Fatalf("asked again: %+v %v (created %d)", again, err, h.custody.created)
	}
	h.custody.down = true
	if _, _, err := h.svc.DepositAddress(ctx, "bob", "USDT", tron); !apperr.Is(err, "WALLET_UNAVAILABLE") {
		t.Fatalf("custodian down: %v", err)
	}
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositAddressAssigned"}) {
		t.Fatalf("events %v", got)
	}

	// Two first requests at once: the one that records second takes the
	// first's address; the custodian's other one is never shown.
	h.custody.down = false
	h.custody.onCreate = func() {
		h.custody.onCreate = nil
		if _, _, err := h.svc.DepositAddress(ctx, "carol", "USDT", tron); err != nil {
			t.Fatal(err)
		}
	}
	h.custody.created = 0
	got, _, err := h.svc.DepositAddress(ctx, "carol", "USDT", tron)
	if err != nil || got.Address != "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj" || h.custody.created != 2 {
		t.Fatalf("raced: %+v %v (created %d)", got, err, h.custody.created)
	}
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositAddressAssigned"}) {
		t.Fatalf("one address assigned: %v", got)
	}
}

func TestCustodyDepositCallbacks(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	addr, _, err := h.svc.DepositAddress(ctx, "alice", "USDT", tron)
	if err != nil {
		t.Fatal(err)
	}
	h.store.take()
	deposit := ports.CustodyTrade{
		TradeID: "t1", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("25.5"), RawAmount: d("25500000"), TxHash: "abc", Block: 99,
	}
	cb := h.callback(t, deposit)
	if cb.Result != domain.CallbackApplied {
		t.Fatalf("callback %+v", cb)
	}
	var got domain.Deposit
	for _, x := range h.store.deposits {
		got = x
	}
	if got.Status != domain.StatusConfirmed || got.ProviderTxID != "UDUN:t1" || !got.Amount.Equal(d("25.5")) || got.UserID != "alice" ||
		got.Confirmations != 20 || got.Asset != "USDT" {
		t.Fatalf("deposit %+v", got)
	}
	// The custodian's retry changes nothing.
	if again := h.callback(t, deposit); again.ID != cb.ID || again.Attempts != 2 || len(h.store.deposits) != 1 {
		t.Fatalf("retry %+v, %d deposits", again, len(h.store.deposits))
	}
	h.cround(t)
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositDetected", "DepositConfirmed"}) {
		t.Fatalf("events %v", got)
	}

	small := deposit
	small.TradeID, small.TxHash, small.Amount, small.RawAmount = "t2", "abc2", d("0.5"), d("500000")
	h.callback(t, small)
	// The same transfer under another trade is not booked twice.
	twice := deposit
	twice.TradeID = "t1-again"
	if cb := h.callback(t, twice); cb.Result != domain.CallbackUnmatched || len(h.store.deposits) != 2 {
		t.Fatalf("the same transfer again: %+v", cb)
	}
	review := deposit
	review.TradeID, review.Status, review.Word = "t3", 0, domain.CustodyReview
	if cb := h.callback(t, review); cb.Result != domain.CallbackIgnored {
		t.Fatalf("a deposit in review: %+v", cb)
	}
	stranger := deposit
	stranger.TradeID, stranger.Address = "t4", "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	if cb := h.callback(t, stranger); cb.Result != domain.CallbackUnmatched {
		t.Fatalf("a deposit to an address of nobody: %+v", cb)
	}
	otherCoin := deposit
	otherCoin.TradeID, otherCoin.Coin = "t5", "0:0"
	if cb := h.callback(t, otherCoin); cb.Result != domain.CallbackUnmatched {
		t.Fatalf("a coin no network uses: %+v", cb)
	}
	var unclaimed int
	for _, x := range h.store.deposits {
		if x.Unclaimed && x.Reason == domain.ReasonBelowMinimum {
			unclaimed++
		}
	}
	if unclaimed != 1 || len(h.store.deposits) != 2 {
		t.Fatalf("%d deposits, %d unclaimed", len(h.store.deposits), unclaimed)
	}

	for raw, code := range map[string]string{"bad:{}": "WALLET_CALLBACK_SIGNATURE", "stale:{}": "WALLET_CALLBACK_STALE", "[": "WALLET_CALLBACK_MALFORMED"} {
		if _, err := h.svc.HandleCallback(ctx, domain.ProviderUdun, "", "198.51.100.9", []byte(raw)); !apperr.Is(err, code) {
			t.Errorf("%s: %v, want %s", raw, err, code)
		}
	}
	rejected := 0
	for _, x := range h.store.callbacks {
		if x.Result == domain.CallbackRejected && !x.SignatureOK {
			rejected++
		}
	}
	if rejected != 3 {
		t.Fatalf("%d refused callbacks logged", rejected)
	}
	if _, err := h.svc.HandleCallback(ctx, "OTHER", "", "", []byte("{}")); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("an unknown custodian: %v", err)
	}
}

// requestCustody requests a USDT withdrawal on TRON for alice and has it
// approved.
func (h *custodyHarness) requestCustody(t *testing.T, amount string) domain.Withdrawal {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.W.StepUps.Consume(ctx, "alice", "none"); err == nil {
		t.Fatal("no step-up should pass")
	}
	if len(h.store.book) == 0 {
		if _, err := h.svc.AddAddress(ctx, "alice", AddressInput{Network: tron, Address: payeeTRX, StepUp: h.stepUp("add", 2, true)}); err != nil {
			t.Fatal(err)
		}
		h.now = h.now.Add(73 * time.Hour)
	}
	wd, err := h.svc.RequestWithdrawal(ctx, "alice", WithdrawalInput{
		Asset: "USDT", Network: tron, Address: payeeTRX, Amount: d(amount), StepUp: h.stepUp(fmt.Sprintf("w%d", len(h.store.wds)), 2, true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if wd.Status != domain.WithdrawalApproved || wd.Provider != domain.ProviderUdun {
		t.Fatalf("requested %+v", wd)
	}
	return wd
}

func TestCustodyWithdrawalSent(t *testing.T) {
	h := newCustodyHarness(t)
	h.custody.invalid["TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"] = true
	if _, err := h.svc.AddAddress(context.Background(), "alice", AddressInput{
		Network: tron, Address: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", StepUp: h.stepUp("x", 2, true),
	}); !apperr.Is(err, "WALLET_INVALID_ADDRESS") {
		t.Fatalf("an address the custodian refuses: %v", err)
	}
	wd := h.requestCustody(t, "100")
	h.store.wevents = nil
	h.cround(t)
	got := h.store.wds[wd.ID]
	if got.Status != domain.WithdrawalSubmitted || got.ProviderStatus != domain.CustodyAccepted || !slices.Equal(h.custody.submitted, []string{wd.ID}) {
		t.Fatalf("handed over: %+v (%v)", got, h.custody.submitted)
	}
	if _, err := h.svc.CancelWithdrawal(context.Background(), "alice", wd.ID); !apperr.Is(err, "WALLET_WITHDRAWAL_NOT_CANCELABLE") {
		t.Fatalf("cancel with the custodian: %v", err)
	}
	trade := ports.CustodyTrade{
		TradeID: "w-1", Kind: domain.CallbackWithdrawal, Status: 1, Word: domain.CustodyApproved, Coin: usdtCoin, Address: payeeTRX,
		Amount: d("100"), RawAmount: d("100000000"), BusinessID: wd.ID,
	}
	h.callback(t, trade)
	if got := h.store.wds[wd.ID]; got.ProviderStatus != domain.CustodyApproved || got.Status != domain.WithdrawalSubmitted {
		t.Fatalf("approved by the custodian: %+v", got)
	}
	// A person confirmed the custodian counts its fee on TRC20 in USDT, as
	// documented (review ④): booked as reported.
	if err := SetCustodyFeeUnit(context.Background(), h.store, domain.FeeUnit{
		Provider: domain.ProviderUdun, Asset: "USDT", Network: tron, Unit: "self", ConfirmedBy: "ops", Reason: "the first withdrawal on tronscan",
		ConfirmedAt: h.now,
	}); err != nil || h.audited("wallet.custody.fee_unit") != 1 {
		t.Fatalf("the fee unit: %v", err)
	}
	trade.Status, trade.Word, trade.TxHash, trade.Fee = 3, domain.CustodySuccess, "f00d", d("1.1")
	h.ledger.system[accountGasSupply] = d("5")
	h.callback(t, trade)
	got = h.store.wds[wd.ID]
	if got.Status != domain.WithdrawalConfirmed || got.TxHash != "f00d" || got.SettleJournal == "" {
		t.Fatalf("sent: %+v", got)
	}
	if !h.ledger.frozen["alice"].IsZero() || !h.ledger.system[accountWithdrawalPending].Equal(d("100")) {
		t.Fatalf("settled: frozen %s, pending %s", h.ledger.frozen["alice"], h.ledger.system[accountWithdrawalPending])
	}
	// A late review step and a repeat change nothing.
	trade.Status, trade.Word = 0, domain.CustodyReview
	if cb := h.callback(t, trade); cb.Result != domain.CallbackIgnored {
		t.Fatalf("a late review: %+v", cb)
	}
	h.cround(t)
	if fee, ok := h.store.fees["UDUN:w-1"]; !ok || !fee.Amount.Equal(d("1.1")) || fee.JournalID == "" {
		t.Fatalf("the custodian's fee: %+v", fee)
	}
	if got := eventNames(h.store.wevents); !slices.Equal(got, []string{"WithdrawalSubmitted", "WithdrawalConfirmed"}) {
		t.Fatalf("events %v", got)
	}
	// The custodian then says it failed: the confirmed withdrawal is left
	// as it is and a person checks (review ①, 2026-10-02).
	reg := prometheus.NewRegistry()
	h.svc.Contradictions = prometheus.NewCounter(prometheus.CounterOpts{Name: "contradictions"})
	reg.MustRegister(h.svc.Contradictions)
	trade.TradeID, trade.Status, trade.Word = "w-1b", 4, domain.CustodyFailed
	if cb := h.callback(t, trade); cb.Result != domain.CallbackDiscrepancy || !strings.Contains(cb.Detail, "nothing reversed") {
		t.Fatalf("failed after it was confirmed: %+v", cb)
	}
	mfs, err := reg.Gather()
	if err != nil || len(mfs) != 1 || mfs[0].GetMetric()[0].GetCounter().GetValue() != 1 {
		t.Fatalf("not counted: %v %v", mfs, err)
	}
	if got := h.store.wds[wd.ID]; got.Status != domain.WithdrawalConfirmed {
		t.Fatalf("the withdrawal %+v", got)
	}
}

// A callback that counts a coin in other decimals than the custodian
// lists for it is not booked: a person looks at it (review ②).
func TestCustodyCallbacksInOtherDecimalsAreNotBooked(t *testing.T) {
	h := newCustodyHarness(t)
	addr, _, err := h.svc.DepositAddress(context.Background(), "alice", "USDT", tron)
	if err != nil {
		t.Fatal(err)
	}
	cb := h.callback(t, ports.CustodyTrade{
		TradeID: "t-dec", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("0.255"), RawAmount: d("25500000"), Decimals: 8, TxHash: "dec",
	})
	if cb.Result != domain.CallbackUnmatched || !strings.Contains(cb.Detail, "in 8 decimals, the custodian lists 6") || len(h.store.deposits) != 0 {
		t.Fatalf("callback %+v, deposits %d", cb, len(h.store.deposits))
	}
}

func TestCustodyWithdrawalRefusedOrFailed(t *testing.T) {
	h := newCustodyHarness(t)
	refused := h.requestCustody(t, "50")
	h.custody.refuse = true
	h.cround(t)
	got := h.store.wds[refused.ID]
	if got.Status != domain.WithdrawalFailed || !strings.HasPrefix(got.RejectReason, "CUSTODY_REFUSED: code 4001") || got.UnfreezeJournal == "" {
		t.Fatalf("refused: %+v", got)
	}
	h.custody.refuse = false
	failed := h.requestCustody(t, "60")
	h.cround(t)
	h.callback(t, ports.CustodyTrade{
		TradeID: "w-2", Kind: domain.CallbackWithdrawal, Status: 4, Word: domain.CustodyFailed, Coin: usdtCoin, BusinessID: failed.ID,
		TxHash: "dead",
	})
	h.cround(t)
	got = h.store.wds[failed.ID]
	if got.Status != domain.WithdrawalFailed || got.RejectReason != "CUSTODY_FAILED: dead" || got.UnfreezeJournal == "" || got.SettleJournal != "" {
		t.Fatalf("failed on chain: %+v", got)
	}
	if !h.ledger.frozen["alice"].IsZero() || !h.ledger.available["alice"].Equal(d("1000")) {
		t.Fatalf("funds back: frozen %s, available %s", h.ledger.frozen["alice"], h.ledger.available["alice"])
	}
	if cb := h.callback(t, ports.CustodyTrade{
		TradeID: "w-3", Kind: domain.CallbackWithdrawal, Status: 3, Word: domain.CustodySuccess,
		BusinessID: "not-an-id",
	}); cb.Result != domain.CallbackUnmatched {
		t.Fatalf("an unknown business ID: %+v", cb)
	}
}

func TestCustodyResubmitsWhatWasNotAcknowledged(t *testing.T) {
	h := newCustodyHarness(t)
	wd := h.requestCustody(t, "20")
	h.custody.down = true
	if err := h.cproc.Round(context.Background()); err == nil {
		t.Fatal("a round with the custodian down reported nothing")
	}
	if got := h.store.wds[wd.ID]; got.Status != domain.WithdrawalSubmitted || got.ProviderStatus != domain.CustodySubmitted {
		t.Fatalf("unknown outcome: %+v", got)
	}
	h.custody.down = false
	h.cround(t)
	if len(h.custody.submitted) != 0 {
		t.Fatal("handed over again before ResubmitAfter")
	}
	h.now = h.now.Add(2 * time.Minute)
	h.cround(t)
	if got := h.store.wds[wd.ID]; got.ProviderStatus != domain.CustodyAccepted || len(h.custody.submitted) != 1 {
		t.Fatalf("handed over again: %+v", got)
	}
}

func TestCustodyCheck(t *testing.T) {
	h := newCustodyHarness(t)
	wd := h.requestCustody(t, "100")
	h.cround(t) // SUBMITTED: 100 in flight
	h.ledger.system[accountDepositPending] = d("-500")
	held := d("400") // the custodian sent the 100 already
	h.custody.coins = []ports.CustodyCoin{{Code: usdtCoin, Symbol: "USDT", Decimals: 6, Token: true, Balance: &held}}
	h.cproc.Elsewhere = func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }
	checks, err := h.cproc.Check(context.Background())
	if err != nil || len(checks) != 1 {
		t.Fatalf("checks %+v %v", checks, err)
	}
	c := checks[0]
	if c.Network != domain.ProviderUdun || !c.Ledger.Equal(d("500")) || !c.InFlight.Equal(d("100")) || !c.Shortfall.IsZero() || c.Addresses != 0 {
		t.Fatalf("check %+v", c)
	}
	missing := d("390")
	h.custody.coins[0].Balance = &missing
	h.cproc.Elsewhere = func(context.Context, string) (decimal.Decimal, error) { return d("5"), nil }
	checks, _ = h.cproc.Check(context.Background())
	if !checks[0].Shortfall.Equal(d("5")) || !checks[0].Elsewhere.Equal(d("5")) {
		t.Fatalf("10 missing, 5 held elsewhere: %+v", checks[0])
	}
	if held, err := h.cproc.Holdings(context.Background(), "USDT"); err != nil || !held.Equal(missing) {
		t.Fatalf("holdings %s %v", held, err)
	}

	// Handed over without an answer, the withdrawal is not in flight: the
	// custodian may not hold it, and counting it would hide a shortfall.
	cur := h.store.wds[wd.ID]
	cur.ProviderStatus = domain.CustodySubmitted
	h.store.wds[wd.ID] = cur
	checks, _ = h.cproc.Check(context.Background())
	if !checks[0].InFlight.IsZero() || !checks[0].Shortfall.Equal(d("105")) {
		t.Fatalf("unacknowledged: %+v", checks[0])
	}
	// Sent, and not booked by the ledger yet: in flight.
	cur.Status, cur.ProviderStatus = domain.WithdrawalConfirmed, domain.CustodySuccess
	h.store.wds[wd.ID] = cur
	checks, _ = h.cproc.Check(context.Background())
	if !checks[0].InFlight.Equal(d("100")) {
		t.Fatalf("sent, not booked: %+v", checks[0])
	}

	// A coin without a balance is not compared, rather than taken as zero.
	h.custody.coins[0].Balance = nil
	checks, err = h.cproc.Check(context.Background())
	if len(checks) != 0 || !errors.Is(err, errNotCompared) {
		t.Fatalf("no balance: %+v %v", checks, err)
	}
	// Nor is one at half of what the ledger expects in the coin's smallest
	// unit (6 decimals) or more, however short of it.
	raw := d("500000000")
	h.custody.coins[0].Balance = &raw
	checks, err = h.cproc.Check(context.Background())
	if len(checks) != 0 || !errors.Is(err, errNotCompared) || !strings.Contains(err.Error(), "smallest unit") {
		t.Fatalf("smallest unit: %+v %v", checks, err)
	}
	// Nor is it a holding for the platform's own wallets' check, where it
	// would hide their shortfall.
	if held, err := h.cproc.Holdings(context.Background(), "USDT"); err == nil {
		t.Fatalf("holdings in the smallest unit: %s", held)
	}
}

func TestReplayCallback(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	trade := ports.CustodyTrade{
		TradeID: "t9", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin,
		Address: "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7", Amount: d("30"), RawAmount: d("30000000"),
	}
	cb := h.callback(t, trade)
	if cb.Result != domain.CallbackUnmatched {
		t.Fatalf("before the address exists: %+v", cb)
	}
	if _, err := h.svc.ReplayCallback(ctx, cb.ID, "ops@example.com", "no"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a reason is required: %v", err)
	}
	if _, _, err := h.svc.DepositAddress(ctx, "alice", "USDT", tron); err != nil {
		t.Fatal(err)
	}
	out, err := h.svc.ReplayCallback(ctx, cb.ID, "ops@example.com", "the address was assigned late")
	if err != nil || out.Result != domain.CallbackApplied || len(h.store.deposits) != 1 || len(h.store.audits) != 1 {
		t.Fatalf("replayed %+v %v (%d deposits)", out, err, len(h.store.deposits))
	}
	if _, err := h.svc.ReplayCallback(ctx, cb.ID, "ops@example.com", "again please"); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("an applied callback replayed: %v", err)
	}
}

// A custodian that never answers the hand-overs (it times out) leaves the
// withdrawal UNCERTAIN after 30 minutes of them: one may have reached it,
// so nothing is released and the hand-overs stop; a person resolves it.
func TestAWithdrawalTheCustodianNeverAnswersBecomesUncertain(t *testing.T) {
	h := newCustodyHarness(t)
	wd := h.requestCustody(t, "20")
	h.custody.down = true
	for range 3 {
		_ = h.cproc.Round(context.Background())
		h.now = h.now.Add(10 * time.Minute)
	}
	if got := h.store.wds[wd.ID]; got.ProviderStatus != domain.CustodySubmitted {
		t.Fatalf("after 20 minutes: %+v", got)
	}
	h.now = h.now.Add(11 * time.Minute)
	_ = h.cproc.Round(context.Background())
	got := h.store.wds[wd.ID]
	if got.Status != domain.WithdrawalSubmitted || got.ProviderStatus != domain.CustodyUncertain || got.UnfreezeJournal != "" ||
		!strings.Contains(got.RejectReason, "no answer") {
		t.Fatalf("after 31 minutes: %+v", got)
	}
	audits := len(h.store.audits)
	if _, err := ResolveCustodyWithdrawal(context.Background(), h.store, wd.ID, true, "beef", "ops", "sent, says its console", h.now); err != nil {
		t.Fatal(err)
	}
	if len(h.store.audits) != audits+1 {
		t.Fatalf("the --sent resolution is not audited: %d audits", len(h.store.audits))
	}
}

// Review finding B1: the first hand-over's outcome is unknown, and the
// custodian refuses the retry ("balance too low": the first took it). It
// may still send the first, so nothing is released: the withdrawal waits,
// uncertain, for the custodian's callback or a person.
func TestCustodyRefusedRetriesWaitForTheCustodian(t *testing.T) {
	h := newCustodyHarness(t)
	sent := h.requestCustody(t, "20")
	unsent := h.requestCustody(t, "30")
	h.custody.down = true
	_ = h.cproc.Round(context.Background())
	h.custody.down, h.custody.refuse = false, true
	h.now = h.now.Add(2 * time.Minute)
	h.cround(t)
	for _, id := range []string{sent.ID, unsent.ID} {
		got := h.store.wds[id]
		if got.Status != domain.WithdrawalSubmitted || got.ProviderStatus != domain.CustodyUncertain || got.UnfreezeJournal != "" ||
			!strings.HasPrefix(got.RejectReason, "UNCERTAIN: code 4001") {
			t.Fatalf("a refused retry: %+v", got)
		}
	}
	if !h.ledger.frozen["alice"].Equal(d("52")) {
		t.Fatalf("frozen %s, want both withdrawals' 50 and their fees", h.ledger.frozen["alice"])
	}
	// No more hand-overs; the custodian's callback still applies.
	h.custody.refuse = false
	h.now = h.now.Add(2 * time.Minute)
	h.cround(t)
	if got := h.store.wds[sent.ID]; got.ProviderStatus != domain.CustodyUncertain {
		t.Fatalf("handed over again: %+v", got)
	}
	h.callback(t, ports.CustodyTrade{
		TradeID: "w-1", Kind: domain.CallbackWithdrawal, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, BusinessID: sent.ID,
		TxHash: "beef",
	})
	if got := h.store.wds[sent.ID]; got.Status != domain.WithdrawalConfirmed || got.RejectReason != "" {
		t.Fatalf("sent after all: %+v", got)
	}
	// A person finds the other was never sent: released within a round.
	if _, err := ResolveCustodyWithdrawal(context.Background(), h.store, unsent.ID, false, "", "ops", "", h.now); err == nil {
		t.Fatal("resolved without a reason")
	}
	// Not while it is being handed over: a later handover the custodian
	// accepts would send what was released.
	cur := h.store.wds[unsent.ID]
	cur.ProviderStatus = domain.CustodySubmitted
	h.store.wds[unsent.ID] = cur
	if _, err := ResolveCustodyWithdrawal(context.Background(), h.store, unsent.ID, false, "", "ops", "gone", h.now); !apperr.Is(err, "WALLET_CUSTODY_HANDOVER_PENDING") {
		t.Fatalf("failed while handed over: %v", err)
	}
	cur.ProviderStatus = domain.CustodyUncertain
	h.store.wds[unsent.ID] = cur
	audits := len(h.store.audits)
	got, err := ResolveCustodyWithdrawal(context.Background(), h.store, unsent.ID, false, "", "ops", "not in the custodian's records", h.now)
	if err != nil || got.Status != domain.WithdrawalFailed || got.RejectReason != "CUSTODY_FAILED: resolved by ops: not in the custodian's records" {
		t.Fatalf("resolved %+v %v", got, err)
	}
	if len(h.store.audits) != audits+1 {
		t.Fatalf("%d audits, want one more than %d", len(h.store.audits), audits)
	}
	h.cround(t)
	if got := h.store.wds[unsent.ID]; got.UnfreezeJournal == "" {
		t.Fatalf("not released: %+v", got)
	}
	if _, err := ResolveCustodyWithdrawal(context.Background(), h.store, unsent.ID, true, "beef", "ops", "again", h.now); err == nil {
		t.Fatal("resolved twice")
	}
}

// A retry refused after the custodian's callback said it holds the
// withdrawal (review, 2026-10-02 low): its word stands, not UNCERTAIN.
func TestARefusedRetryLeavesTheCustodiansWord(t *testing.T) {
	h := newCustodyHarness(t)
	wd := h.requestCustody(t, "20")
	h.custody.down = true
	_ = h.cproc.Round(context.Background())
	h.custody.down, h.custody.refuse = false, true
	h.custody.onSubmit = func(id string) {
		h.custody.onSubmit = nil
		h.callback(t, ports.CustodyTrade{
			TradeID: "w-1", Kind: domain.CallbackWithdrawal, Status: 0, Word: domain.CustodyReview, Coin: usdtCoin, BusinessID: id,
		})
	}
	h.now = h.now.Add(2 * time.Minute)
	h.cround(t)
	if got := h.store.wds[wd.ID]; got.Status != domain.WithdrawalSubmitted || got.ProviderStatus != domain.CustodyReview || got.RejectReason != "" {
		t.Fatalf("the custodian's review was overwritten: %+v", got)
	}
}

// The custodian accepting a handover of a withdrawal that failed and was
// released meanwhile (a callback while it was on its way) may still send
// it: counted with the contradictions and logged, nothing reversed.
func TestALateHandoverOfAFailedWithdrawalIsCounted(t *testing.T) {
	h := newCustodyHarness(t)
	contradictions := &countingCounter{}
	h.cproc.Contradictions = contradictions
	wd := h.requestCustody(t, "20")
	h.custody.onSubmit = func(id string) {
		cur := h.store.wds[id]
		cur.Status, cur.ProviderStatus, cur.RejectReason = domain.WithdrawalFailed, domain.CustodyFailed, "CUSTODY_FAILED: callback"
		h.store.wds[id] = cur
	}
	h.cround(t)
	if got := h.store.wds[wd.ID]; got.Status != domain.WithdrawalFailed || contradictions.n != 1 {
		t.Fatalf("%+v, %d contradictions", got, contradictions.n)
	}
}

// Review ④ (2026-10-03): the custodian's fee is booked only when its unit
// is known and it is within feeBound times the network's withdrawal fee
// and the amount sent; otherwise it is held for a person, who books it
// (as they found it charged) or writes it off.
func TestCustodyFeesHeldForAPerson(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	held := &countingCounter{}
	h.svc.FeesHeld = held
	h.ledger.system[accountGasSupply] = d("10")
	sent := func(id, trade, fee string) domain.Callback {
		t.Helper()
		return h.callback(t, ports.CustodyTrade{
			TradeID: trade, Kind: domain.CallbackWithdrawal, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, BusinessID: id,
			TxHash: "tx-" + trade, Fee: d(fee),
		})
	}
	custodied := func(_ context.Context, provider, asset, network string) (int32, bool, error) {
		return 6, provider == domain.ProviderUdun && asset == "USDT" && network == tron, nil
	}

	// A token whose fee unit nobody confirmed: held, whatever its size.
	first := h.requestCustody(t, "20")
	h.cround(t)
	cb := sent(first.ID, "w-1", "0.8")
	if got := h.store.wds[first.ID]; got.Status != domain.WithdrawalConfirmed || !strings.Contains(cb.Detail, "held for a person") {
		t.Fatalf("sent %+v, callback %+v", got, cb)
	}
	if f := h.store.fees["UDUN:w-1"]; f.Status != domain.FeeHeld || !strings.Contains(f.HoldReason, "nobody has confirmed") ||
		!f.Amount.Equal(d("0.8")) || held.n != 1 {
		t.Fatalf("held %+v (%d)", f, held.n)
	}

	// Confirmed in the coin itself: a fee within five times the network's
	// fee (1 USDT) is booked; one in another unit, 1500 "USDT" on 20 sent,
	// is held.
	if err := SetCustodyFeeUnit(ctx, h.store, domain.FeeUnit{
		Provider: domain.ProviderUdun, Asset: "USDT", Network: tron, Unit: domain.FeeUnitSelf, ConfirmedBy: "ops", Reason: "on tronscan",
		ConfirmedAt: h.now,
	}); err != nil {
		t.Fatal(err)
	}
	second, third := h.requestCustody(t, "20"), h.requestCustody(t, "20")
	h.cround(t)
	sent(second.ID, "w-2", "1.2")
	sent(third.ID, "w-3", "1500")
	if f := h.store.fees["UDUN:w-2"]; f.Status != domain.FeeBookable || !f.Amount.Equal(d("1.2")) {
		t.Fatalf("within the bound %+v", f)
	}
	if f := h.store.fees["UDUN:w-3"]; f.Status != domain.FeeHeld || !strings.Contains(f.HoldReason, "above 5 USDT") || held.n != 2 {
		t.Fatalf("beyond belief %+v (%d)", f, held.n)
	}
	h.cround(t)
	if h.store.fees["UDUN:w-2"].JournalID == "" || h.store.fees["UDUN:w-1"].JournalID != "" || h.store.fees["UDUN:w-3"].JournalID != "" {
		t.Fatalf("only the bookable fee is booked: %+v", h.store.fees)
	}
	if v := h.gauge(t, "wallet_custody_fees_held"); v != 2 {
		t.Fatalf("%v fees held", v)
	}

	// The person books the first as reported and the third as they found
	// it charged; booking one again finds nothing held.
	resolve := func(r FeeResolution) (domain.ChainFee, error) {
		r.Actor, r.Reason = "ops", "the custodian's statement"
		return ResolveCustodyFee(ctx, h.store, custodied, r, h.now)
	}
	if _, err := resolve(FeeResolution{WithdrawalID: third.ID, Book: true, Asset: "TRX", Amount: d("13.6")}); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("an asset not held with the custodian: %v", err)
	}
	if _, err := resolve(FeeResolution{WithdrawalID: third.ID, Book: true, Amount: d("1.5000001")}); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("beyond the asset's decimals: %v", err)
	}
	if _, err := resolve(FeeResolution{WithdrawalID: third.ID, Amount: d("1")}); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("an amount on a write-off: %v", err)
	}
	if f, err := resolve(FeeResolution{WithdrawalID: third.ID, Book: true, Amount: d("1.5")}); err != nil || f.Status != domain.FeeBookable ||
		!f.Amount.Equal(d("1.5")) || f.ResolvedBy != "ops" {
		t.Fatalf("booked as charged: %+v %v", f, err)
	}
	if _, err := resolve(FeeResolution{WithdrawalID: first.ID, Book: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(FeeResolution{WithdrawalID: first.ID, Book: true}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("nothing held any more: %v", err)
	}
	if h.audited("wallet.custody.fee.book") != 2 {
		t.Fatalf("%d bookings audited", h.audited("wallet.custody.fee.book"))
	}
	h.cround(t)
	if !h.store.fees["UDUN:w-1"].Amount.Equal(d("0.8")) || h.store.fees["UDUN:w-1"].JournalID == "" || h.store.fees["UDUN:w-3"].JournalID == "" {
		t.Fatalf("booked after the decisions: %+v", h.store.fees)
	}
	if v := h.gauge(t, "wallet_custody_fees_held"); v != 0 {
		t.Fatalf("%v fees held", v)
	}
	if _, err := resolve(FeeResolution{WithdrawalID: first.ID}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("a booked fee written off: %v", err)
	}

	// A fee written off is never booked.
	fourth := h.requestCustody(t, "20")
	h.cround(t)
	sent(fourth.ID, "w-4", "6")
	if f, err := resolve(FeeResolution{WithdrawalID: fourth.ID}); err != nil || f.Status != domain.FeeWrittenOff {
		t.Fatalf("written off %+v %v", f, err)
	}
	h.cround(t)
	if f := h.store.fees["UDUN:w-4"]; f.JournalID != "" || h.audited("wallet.custody.fee.write_off") != 1 {
		t.Fatalf("a written-off fee %+v", f)
	}

	// A fee within the bound that waits for GAS_SUPPLY may be written off
	// too; should the ledger book it meanwhile, the booking stands.
	h.ledger.system[accountGasSupply] = decimal.Zero
	fifth := h.requestCustody(t, "20")
	h.cround(t)
	sent(fifth.ID, "w-5", "0.7")
	if err := h.cproc.Round(ctx); err == nil || !strings.Contains(err.Error(), "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("GAS_SUPPLY short: %v", err)
	}
	if f := h.store.fees["UDUN:w-5"]; f.Status != domain.FeeBookable || f.JournalID != "" {
		t.Fatalf("waiting for GAS_SUPPLY %+v", f)
	}
	if _, err := resolve(FeeResolution{WithdrawalID: fifth.ID, Book: true}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("a bookable fee is booked as it comes, not by a person: %v", err)
	}
	f, err := resolve(FeeResolution{WithdrawalID: fifth.ID})
	if err != nil || f.Status != domain.FeeWrittenOff || f.HoldReason == "" {
		t.Fatalf("written off while waiting: %+v %v", f, err)
	}
	if was, err := h.store.ChainFees().MarkBooked(ctx, "UDUN:w-5", "j-late"); err != nil || was != domain.FeeWrittenOff ||
		h.store.fees["UDUN:w-5"].Status != domain.FeeBookable {
		t.Fatalf("the ledger booked it meanwhile: %s %v, %+v", was, err, h.store.fees["UDUN:w-5"])
	}
}

// The other units a person may confirm: fees charged outside the coin
// balances are not recorded; fees in the chain's coin are booked in the
// asset the platform holds of it with the custodian, at that coin's
// decimals, and held when it holds none.
func TestCustodyFeeUnits(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	h.ledger.system[accountGasSupply] = d("10")
	const ethCoin, erc20 = "60:60", "60:0xdAC17F958D2ee523a2206206994597C13D831ec7"
	h.nets.nets = append(h.nets.nets,
		domain.Network{
			Asset: "ETH", Network: "ETH", Chain: "1", Decimals: 18, Enabled: true, WithdrawEnabled: true, WithdrawFee: d("0.001"),
			Provider: domain.ProviderUdun, ProviderCoin: ethCoin,
		},
		domain.Network{
			Asset: "USDT", Network: "ETH", Chain: "1", Decimals: 6, Enabled: true, WithdrawEnabled: true, WithdrawFee: d("5"),
			Provider: domain.ProviderUdun, ProviderCoin: erc20,
		})
	none := decimal.Zero
	h.custody.coins = append(h.custody.coins, ports.CustodyCoin{Code: ethCoin, Decimals: 18, Balance: &none},
		ports.CustodyCoin{Code: erc20, Decimals: 6, Token: true, Balance: &none})
	unit := func(network, u string) {
		t.Helper()
		if err := SetCustodyFeeUnit(ctx, h.store, domain.FeeUnit{
			Provider: domain.ProviderUdun, Asset: "USDT", Network: network, Unit: u, ConfirmedBy: "ops", Reason: "the first withdrawal",
			ConfirmedAt: h.now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetCustodyFeeUnit(ctx, h.store, domain.FeeUnit{
		Provider: domain.ProviderUdun, Asset: "USDT", Network: tron, Unit: "GAS",
		ConfirmedBy: "ops", Reason: "?",
	}); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("an unknown unit: %v", err)
	}
	// sent hands a withdrawal over and reports it sent on network with a
	// fee of raw in the smallest unit of the callback's decimals.
	sent := func(trade, network, coin string, decimals int32, raw string) domain.Callback {
		t.Helper()
		wd := h.requestCustody(t, "20")
		h.cround(t)
		cur := h.store.wds[wd.ID]
		cur.Network = network
		h.store.wds[wd.ID] = cur
		return h.callback(t, ports.CustodyTrade{
			TradeID: trade, Kind: domain.CallbackWithdrawal, Status: 3, Word: domain.CustodySuccess, Coin: coin, BusinessID: wd.ID,
			TxHash: "tx-" + trade, Fee: d(raw).Shift(-decimals), Decimals: decimals,
		})
	}

	unit(tron, domain.FeeUnitOutside)
	if cb := sent("w-1", tron, usdtCoin, 6, "13600000"); !strings.Contains(cb.Detail, "charged outside") || len(h.store.fees) != 0 {
		t.Fatalf("outside: %+v, fees %v", cb, h.store.fees)
	}

	// The gas of an ERC20 transfer in wei, at the token's 6 decimals: read
	// as ETH at 18, 0.0021 ETH, booked in ETH.
	unit("ETH", domain.FeeUnitMain)
	sent("w-2", "ETH", erc20, 6, "2100000000000000")
	if f := h.store.fees["UDUN:w-2"]; f.Status != domain.FeeBookable || f.Asset != "ETH" || !f.Amount.Equal(d("0.0021")) || f.Network != "ETH" {
		t.Fatalf("in the chain's coin %+v", f)
	}
	// Beyond five times ETH's own withdrawal fee: held.
	sent("w-3", "ETH", erc20, 6, "9000000000000000")
	if f := h.store.fees["UDUN:w-3"]; f.Status != domain.FeeHeld || !strings.Contains(f.HoldReason, "above 0.005 ETH") {
		t.Fatalf("above the chain coin's bound %+v", f)
	}
	// Without a withdrawal fee on the chain coin's network nothing bounds
	// it: held.
	for i := range h.nets.nets {
		if h.nets.nets[i].ProviderCoin == ethCoin {
			h.nets.nets[i].WithdrawFee = decimal.Zero
		}
	}
	sent("w-5", "ETH", erc20, 6, "2100000000000000")
	if f := h.store.fees["UDUN:w-5"]; f.Status != domain.FeeHeld || !strings.Contains(f.HoldReason, "no bound") {
		t.Fatalf("no bound to compare with %+v", f)
	}
	// TRX is no coin the platform holds with the custodian: held.
	unit(tron, domain.FeeUnitMain)
	sent("w-4", tron, usdtCoin, 6, "13600000")
	if f := h.store.fees["UDUN:w-4"]; f.Status != domain.FeeHeld || !strings.Contains(f.HoldReason, "on no network") {
		t.Fatalf("a chain coin the platform does not hold %+v", f)
	}
}

func (h *custodyHarness) gauge(t *testing.T, name string) float64 {
	t.Helper()
	mfs, err := h.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("no metric %s", name)
	return 0
}

// countingCounter counts Inc calls (the rest of prometheus.Counter is not used).
type countingCounter struct {
	prometheus.Counter
	n int
}

func (c *countingCounter) Inc() { c.n++ }

// Refused callbacks are counted, and kept for a look only within limits:
// a flood of forged ones fills neither the table nor its rows.
func TestRefusedCallbacksAreCountedAndKeptWithinLimits(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	refused := &countingCounter{}
	h.svc.CallbacksRejected = refused
	long := "bad:" + strings.Repeat("é", 3000) // 6,000 bytes
	for range rejectedPerHour + 20 {
		if _, err := h.svc.HandleCallback(ctx, domain.ProviderUdun, "", "", []byte(long)); !apperr.Is(err, "WALLET_CALLBACK_SIGNATURE") {
			t.Fatal(err)
		}
	}
	if refused.n != rejectedPerHour+20 || len(h.store.callbacks) != rejectedPerHour {
		t.Fatalf("counted %d, kept %d", refused.n, len(h.store.callbacks))
	}
	if raw := h.store.callbacks[0].Raw; len(raw) > rejectedRaw || !utf8.ValidString(raw) || !strings.HasPrefix(raw, "bad:é") {
		t.Fatalf("kept %d bytes, valid %v", len(raw), utf8.ValidString(raw))
	}
	// An hour later there is room again.
	h.now = h.now.Add(time.Hour + time.Second)
	if _, err := h.svc.HandleCallback(ctx, domain.ProviderUdun, "", "", []byte("bad:{}")); err == nil || len(h.store.callbacks) != rejectedPerHour+1 {
		t.Fatalf("an hour later: %v, kept %d", err, len(h.store.callbacks))
	}
}

// Review B4 (design §9): funds missing that no withdrawal with an unknown
// outcome explains, on two checks five minutes apart, suspend the asset's
// withdrawals: new requests are refused and approved ones wait, until a
// person lifts it.
func TestAShortfallOnTwoChecksSuspendsTheAssetsWithdrawals(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	h.cproc.Elsewhere = func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }
	h.ledger.system[accountDepositPending] = d("-500")
	held := d("490") // 10 missing
	h.custody.coins = []ports.CustodyCoin{{Code: usdtCoin, Symbol: "USDT", Decimals: 6, Token: true, Balance: &held}}
	if _, err := h.cproc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.store.suspended) != 0 || h.cproc.recheckAt.IsZero() {
		t.Fatalf("a first sighting suspends nothing, it checks again: %v, recheck at %v", h.store.suspended, h.cproc.recheckAt)
	}
	// An operator's check within the five minutes decides nothing.
	h.now = h.now.Add(time.Minute)
	if _, err := h.cproc.Check(ctx); err != nil || len(h.store.suspended) != 0 {
		t.Fatalf("too soon: %v %v", h.store.suspended, err)
	}
	h.now = h.now.Add(4 * time.Minute)
	h.cround(t) // the check comes again now, not in an hour
	x, ok := h.store.suspended["USDT"]
	if !ok || x.SuspendedBy != domain.SuspendedBySystem || !x.Shortfall.Equal(d("10")) || h.audited("wallet.withdrawals.suspend") != 1 {
		t.Fatalf("suspended %+v (%v)", x, ok)
	}
	h.cround(t)
	if v := h.gaugeOf(t, "wallet_withdrawals_suspended", "USDT"); v != 1 {
		t.Fatalf("gauge %v", v)
	}

	// New requests are refused; one approved before waits.
	h.custody.coins[0].Balance = &held
	if _, err := h.svc.RequestWithdrawal(ctx, "alice", WithdrawalInput{
		Asset: "USDT", Network: tron, Address: payeeTRX, Amount: d("20"), StepUp: h.stepUp("w-sus", 2, true),
	}); !apperr.Is(err, "WALLET_WITHDRAW_SUSPENDED") {
		t.Fatalf("a request while suspended: %v", err)
	}
	delete(h.store.suspended, "USDT")
	wd := h.requestCustody(t, "20")
	h.store.suspended["USDT"] = x
	h.cround(t)
	if got := h.store.wds[wd.ID]; got.Status != domain.WithdrawalApproved || len(h.custody.submitted) != 0 {
		t.Fatalf("handed over while suspended: %+v", got)
	}
	if _, err := ResumeWithdrawals(ctx, h.store, Resume{Asset: "usdt", Actor: "ops"}, h.now); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no reason: %v", err)
	}
	lifted, err := ResumeWithdrawals(ctx, h.store, Resume{Asset: "usdt", Actor: "ops", Reason: "the custodian's statement explains it"}, h.now)
	if err != nil || lifted.Asset != "USDT" {
		t.Fatalf("resumed %+v %v", lifted, err)
	}
	if _, err := ResumeWithdrawals(ctx, h.store, Resume{Asset: "USDT", Actor: "ops", Reason: "again"}, h.now); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("resumed twice: %v", err)
	}
	h.cround(t)
	if got := h.store.wds[wd.ID]; got.Status != domain.WithdrawalSubmitted || h.audited("wallet.withdrawals.resume") != 1 {
		t.Fatalf("handed over once resumed: %+v", got)
	}
}

// What a withdrawal with an unknown outcome may have taken explains a
// shortfall that large; one that is gone by the next check is forgotten.
func TestAShortfallExplainedOrGoneSuspendsNothing(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	h.cproc.Elsewhere = func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }
	wd := h.requestCustody(t, "30")
	h.custody.down = true
	_ = h.cproc.Round(ctx) // handed over without an answer: SUBMITTED
	h.custody.down = false
	if got := h.store.wds[wd.ID]; got.ProviderStatus != domain.CustodySubmitted {
		t.Fatalf("not unanswered: %+v", got)
	}
	h.ledger.system[accountDepositPending] = d("-500")
	held := d("470") // 30 missing: the unanswered withdrawal's
	h.custody.coins = []ports.CustodyCoin{{Code: usdtCoin, Symbol: "USDT", Decimals: 6, Token: true, Balance: &held}}
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 0 {
		t.Fatalf("explained: %v %v", h.store.watches, err)
	}
	less := d("460") // 10 more: suspect, then back by the next check
	h.custody.coins[0].Balance = &less
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 1 {
		t.Fatalf("suspect: %v %v", h.store.watches, err)
	}
	h.custody.coins[0].Balance = &held
	h.now = h.now.Add(5 * time.Minute)
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 0 || len(h.store.suspended) != 0 {
		t.Fatalf("gone: %v %v %v", h.store.watches, h.store.suspended, err)
	}
}

// An operator may suspend an asset's withdrawals by hand, once.
func TestAnOperatorSuspendsWithdrawals(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	known := func(_ context.Context, asset string) (bool, error) { return asset == "USDT", nil }
	if _, err := SuspendWithdrawals(ctx, h.store, known, "usdt", "ops", "the custodian reported an incident", h.now); err != nil {
		t.Fatal(err)
	}
	if _, err := SuspendWithdrawals(ctx, h.store, known, "USDT", "ops", "again", h.now); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("twice: %v", err)
	}
	if _, err := SuspendWithdrawals(ctx, h.store, known, "USTD", "ops", "a typo", h.now); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("an asset no network withdraws: %v", err)
	}
	if x := h.store.suspended["USDT"]; x.SuspendedBy != "ops" || h.audited("wallet.withdrawals.suspend") != 1 {
		t.Fatalf("suspended %+v", x)
	}
}

func (h *custodyHarness) gaugeOf(t *testing.T, name, asset string) float64 {
	t.Helper()
	mfs, err := h.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "asset" && l.GetValue() == asset {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	return 0
}

// suspects counts the assets the custody checks suspect of missing funds.
func (h *custodyHarness) suspects() int {
	n := 0
	for _, w := range h.store.watches {
		if !w.SuspectSince.IsZero() {
			n++
		}
	}
	return n
}

// missing sets what the custodian holds of USDT against 500 expected.
func (h *custodyHarness) missing(amount string) {
	h.cproc.Elsewhere = func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }
	h.ledger.system[accountDepositPending] = d("-500")
	held := d("500").Sub(d(amount))
	h.custody.coins = []ports.CustodyCoin{{Code: usdtCoin, Symbol: "USDT", Decimals: 6, Token: true, Balance: &held}}
}

// Review of ebb8aaa, H1: an asset's threshold is WALLET_SHORTFALL_STOP's,
// else the smallest withdrawal fee of its custody networks (1 USDT here):
// a custodian's rounding is not a loss.
func TestAShortfallWithinTheStopSuspendsNothing(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	h.missing("0.000001")
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 0 {
		t.Fatalf("a rounding: %v %v", h.store.watches, err)
	}
	h.missing("1")
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 0 {
		t.Fatalf("one fee's worth: %v %v", h.store.watches, err)
	}
	h.missing("1.5")
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 1 {
		t.Fatalf("beyond the fee: %v %v", h.store.watches, err)
	}
	h.cproc.ShortfallStop = map[string]decimal.Decimal{"USDT": d("2")}
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 0 {
		t.Fatalf("within the configured stop: %v %v", h.store.watches, err)
	}
}

// Fees held for a person are each alerted on their own: they explain what
// they took and suspend nothing.
func TestHeldFeesExplainAShortfall(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	h.store.fees["UDUN:fee-1"] = domain.ChainFee{
		TxHash: "UDUN:fee-1", Network: tron, Asset: "USDT", Amount: d("3"), Purpose: domain.FeeWithdrawal, Status: domain.FeeHeld,
	}
	h.missing("3.5")
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 0 {
		t.Fatalf("held fee: %v %v", h.store.watches, err)
	}
	h.missing("4.5") // 1.5 beyond the held fee
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 1 {
		t.Fatalf("beyond the held fee: %v %v", h.store.watches, err)
	}
	// One held as beyond belief explains at most five times the network's
	// fee, not a loss as large as itself (review AB): 3 + 5 of 50.
	h.store.fees["UDUN:fee-2"] = domain.ChainFee{
		TxHash: "UDUN:fee-2", Network: tron, Asset: "USDT", Amount: d("100"), Purpose: domain.FeeWithdrawal, Status: domain.FeeHeld,
	}
	h.missing("50")
	h.now = h.now.Add(5 * time.Minute)
	if _, err := h.cproc.Check(ctx); err != nil || len(h.store.suspended) != 1 || !h.store.suspended["USDT"].Shortfall.Equal(d("42")) {
		t.Fatalf("a held fee hid a loss: %v %v", h.store.suspended, err)
	}
}

// A network charging no withdrawal fee gives its smallest minimum
// withdrawal as the threshold, not 0 (review AB).
func TestAFeelessNetworksThresholdIsItsMinimum(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	h.nets.nets[len(h.nets.nets)-1].WithdrawFee = decimal.Zero
	h.missing("9")
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 0 {
		t.Fatalf("within the minimum withdrawal: %v %v", h.store.watches, err)
	}
	h.missing("11")
	if _, err := h.cproc.Check(ctx); err != nil || h.suspects() != 1 {
		t.Fatalf("beyond it: %v %v", h.store.watches, err)
	}
}

// Review of ebb8aaa, H2: lifting a suspension starts the checks over, so a
// standing difference suspends the asset again only on two checks; one a
// person accepted for a while does not, unless more goes missing, and the
// acceptance lapses.
func TestAResumeStartsTheChecksOver(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	h.missing("10")
	suspendNow := func() {
		t.Helper()
		if _, err := h.cproc.Check(ctx); err != nil {
			t.Fatal(err)
		}
		h.now = h.now.Add(5 * time.Minute)
		h.cround(t)
		if _, ok := h.store.suspended["USDT"]; !ok {
			t.Fatalf("not suspended: %v", h.store.watches)
		}
	}
	suspendNow()
	if h.suspects() != 0 {
		t.Fatalf("the suspension spends the suspicion: %v", h.store.watches)
	}
	if _, err := ResumeWithdrawals(ctx, h.store, Resume{Asset: "USDT", Actor: "ops", Reason: "looked at it"}, h.now); err != nil {
		t.Fatal(err)
	}
	// Still missing an hour later: suspected anew, not suspended at once.
	h.now = h.now.Add(time.Hour)
	h.cround(t)
	if _, ok := h.store.suspended["USDT"]; ok || h.suspects() != 1 {
		t.Fatalf("suspended at once after a resume: %v %v", h.store.suspended, h.store.watches)
	}
	h.now = h.now.Add(5 * time.Minute)
	h.cround(t)
	if _, ok := h.store.suspended["USDT"]; !ok {
		t.Fatal("still missing on two checks: suspended again")
	}

	for _, bad := range []Resume{
		{Asset: "USDT", Actor: "ops", Reason: "accepted", Accept: d("-1"), AcceptFor: time.Hour},
		{Asset: "USDT", Actor: "ops", Reason: "accepted", Accept: d("10")},
		{Asset: "USDT", Actor: "ops", Reason: "accepted", Accept: d("10"), AcceptFor: domain.MaxAcceptFor + time.Hour},
	} {
		if _, err := ResumeWithdrawals(ctx, h.store, bad, h.now); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	tooMuch := Resume{Asset: "USDT", Actor: "ops", Reason: "more than was missing", Accept: d("11"), AcceptFor: time.Hour}
	if _, err := ResumeWithdrawals(ctx, h.store, tooMuch, h.now); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("accepted more than the check found missing: %v", err)
	}
	if _, ok := h.store.suspended["USDT"]; !ok {
		t.Fatal("a refused resume lifted the suspension")
	}
	accepted := Resume{Asset: "USDT", Actor: "ops", Reason: "the custodian's rounding, a ledger correction follows", Accept: d("10"), AcceptFor: 24 * time.Hour}
	if _, err := ResumeWithdrawals(ctx, h.store, accepted, h.now); err != nil {
		t.Fatal(err)
	}
	if a, _ := h.store.audits[len(h.store.audits)-1].(*auditv1.AdminActionPerformed); !strings.Contains(a.GetDetails(), `"accepted":"10"`) {
		t.Fatalf("the acceptance is audited: %v", a)
	}
	for range 3 { // the accepted difference stays, hour after hour
		h.now = h.now.Add(time.Hour)
		h.cround(t)
		h.now = h.now.Add(5 * time.Minute)
		h.cround(t)
	}
	if _, ok := h.store.suspended["USDT"]; ok || h.suspects() != 0 {
		t.Fatalf("an accepted difference suspended: %v %v", h.store.suspended, h.store.watches)
	}
	// More goes missing: what is beyond the accepted difference counts.
	h.missing("15")
	h.now = h.now.Add(time.Hour)
	suspendNow()
	if x := h.store.suspended["USDT"]; !x.Shortfall.Equal(d("15")) || !strings.Contains(x.Reason, "10 of it accepted by ops") {
		t.Fatalf("suspended %+v", x)
	}
	// Lifted again, the acceptance lapses after its day.
	if _, err := ResumeWithdrawals(ctx, h.store, Resume{Asset: "USDT", Actor: "ops", Reason: "found the other 5"}, h.now); err != nil {
		t.Fatal(err)
	}
	h.missing("10")
	h.now = h.now.Add(24 * time.Hour)
	suspendNow()
	if w := h.store.watches["USDT"]; !w.Accepted.IsZero() || !w.AcceptedUntil.IsZero() {
		t.Fatalf("a lapsed acceptance is forgotten: %+v", w)
	}
}

// Review of ebb8aaa: a recheck the custodian did not answer comes again
// RecheckAfter later, not with the hourly check.
func TestAFailedRecheckComesAgainSoon(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	h.missing("10")
	h.cround(t) // the hourly check: a first sighting
	if h.suspects() != 1 || h.cproc.recheckAt.IsZero() {
		t.Fatalf("suspected: %v, recheck at %v", h.store.watches, h.cproc.recheckAt)
	}
	h.now = h.now.Add(5 * time.Minute)
	h.custody.down = true
	if err := h.cproc.Round(ctx); err == nil {
		t.Fatal("the custodian is down")
	}
	if want := h.now.Add(5 * time.Minute); !h.cproc.recheckAt.Equal(want) {
		t.Fatalf("recheck at %v, want %v", h.cproc.recheckAt, want)
	}
	h.custody.down = false
	h.now = h.now.Add(5 * time.Minute)
	h.cround(t)
	if _, ok := h.store.suspended["USDT"]; !ok {
		t.Fatal("decided ten minutes after the first sighting, not an hour")
	}
}

// A restart keeps the suspicion (the store has it): the new processor's
// first check decides.
func TestARestartKeepsTheSuspicion(t *testing.T) {
	h := newCustodyHarness(t)
	h.missing("10")
	h.cround(t)
	again := NewCustodyProcessor(CustodyProcessor{
		Store: h.store, Ledger: h.ledger, Networks: h.nets, Eligibility: h.elig, Custody: h.custody, Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return h.now }, Elsewhere: h.cproc.Elsewhere,
	}, prometheus.NewRegistry())
	h.now = h.now.Add(5 * time.Minute)
	if err := again.Round(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.store.suspended["USDT"]; !ok {
		t.Fatalf("the restarted processor forgot the first sighting: %v", h.store.watches)
	}
}

// A suspension holds the asset's approved withdrawals on every network,
// counted by asset; other work goes on.
func TestASuspensionHoldsTheAssetOnEveryNetwork(t *testing.T) {
	h := newCustodyHarness(t)
	bsc := domain.Network{
		Asset: "USDT", Network: "BSC", Chain: "bsc", Contract: "0x55d398326f99059ff775485246999027b3197955", Decimals: 18, Confirmations: 15,
		MinDeposit: d("1"), Enabled: true, WithdrawEnabled: true, MinWithdraw: d("10"), WithdrawFee: d("0.5"), AddressFormat: domain.FormatEVM,
		Provider: domain.ProviderUdun, ProviderCoin: "9006:0x55d398326f99059fF775485246999027B3197955",
	}
	h.nets.nets = append(h.nets.nets, bsc)
	none := decimal.Zero
	h.custody.coins = append(h.custody.coins, ports.CustodyCoin{Code: bsc.ProviderCoin, Symbol: "USDT", Decimals: 18, Token: true, Balance: &none})
	onTron := h.requestCustody(t, "20")
	onBSC := onTron
	onBSC.ID, onBSC.Network, onBSC.Address = "wd-bsc", "BSC", "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
	h.store.wds[onBSC.ID] = onBSC
	for _, asset := range []string{"USDT", "BTC"} {
		h.store.suspended[asset] = domain.Suspension{Asset: asset, Reason: "a test", SuspendedBy: "ops", SuspendedAt: h.now, Shortfall: decimal.Zero}
	}
	h.cround(t)
	if h.gaugeOf(t, "wallet_withdrawals_suspended", "BTC") != 1 || h.gaugeOf(t, "wallet_withdrawals_suspended_waiting", "BTC") != 0 {
		t.Fatal("BTC is suspended with nothing waiting")
	}
	for _, id := range []string{onTron.ID, onBSC.ID} {
		if got := h.store.wds[id]; got.Status != domain.WithdrawalApproved {
			t.Fatalf("%s handed over while suspended: %+v", got.Network, got)
		}
	}
	if len(h.custody.submitted) != 0 || h.gaugeOf(t, "wallet_withdrawals_suspended_waiting", "USDT") != 2 {
		t.Fatalf("submitted %v, waiting %v", h.custody.submitted, h.gaugeOf(t, "wallet_withdrawals_suspended_waiting", "USDT"))
	}
	delete(h.store.suspended, "USDT")
	h.cround(t)
	if len(h.custody.submitted) != 2 || h.gaugeOf(t, "wallet_withdrawals_suspended_waiting", "USDT") != 0 {
		t.Fatalf("both go once lifted: %v", h.custody.submitted)
	}
}

// The addresses a callback's deliveries come from are kept with it (the
// allow list is drawn from them, 2026-10-03): a retry from another adds
// it, at most MaxRemoteIPs; a refused callback keeps its own.
func TestACallbackKeepsTheAddressesItCameFrom(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	a, _, err := h.svc.DepositAddress(ctx, "alice", "USDT", tron)
	if err != nil {
		t.Fatal(err)
	}
	tr := ports.CustodyTrade{
		TradeID: "dep-ip", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: a.Address,
		Amount: d("5"), RawAmount: d("5000000"), TxHash: "0xip", Block: 99,
	}
	h.callback(t, tr)
	h.callbackFrom = "203.0.113.11"
	h.callback(t, tr)
	h.callback(t, tr) // the same address again: kept once
	cb := h.store.callbacks[len(h.store.callbacks)-1]
	if cb.Attempts != 3 || !slices.Equal(cb.RemoteIPs, []string{"203.0.113.10", "203.0.113.11"}) {
		t.Fatalf("kept %v after %d attempts", cb.RemoteIPs, cb.Attempts)
	}
	for i := range domain.MaxRemoteIPs { // the newest are kept
		h.callbackFrom = fmt.Sprintf("198.51.100.%d", i+1)
		h.callback(t, tr)
	}
	cb = h.store.callbacks[len(h.store.callbacks)-1]
	if len(cb.RemoteIPs) != domain.MaxRemoteIPs || cb.RemoteIPs[0] != "198.51.100.1" || cb.RemoteIPs[domain.MaxRemoteIPs-1] != "198.51.100.8" {
		t.Fatalf("the newest %d: %v", domain.MaxRemoteIPs, cb.RemoteIPs)
	}
	if _, err := h.svc.HandleCallback(ctx, domain.ProviderUdun, "", "198.51.100.66", []byte("bad:{}")); err == nil {
		t.Fatal("a forged callback was taken")
	}
	if got := h.store.callbacks[len(h.store.callbacks)-1]; got.SignatureOK || !slices.Equal(got.RemoteIPs, []string{"198.51.100.66"}) {
		t.Fatalf("the refused one: %+v", got)
	}
}
