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
	// onCreate runs while an address is created (a concurrent request).
	onCreate  func()
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
	switch {
	case f.down:
		return errors.New("gateway down")
	case f.refuse:
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
	h := &custodyHarness{withdrawHarness: w, custody: c}
	h.cproc = NewCustodyProcessor(CustodyProcessor{
		Store: w.store, Ledger: w.ledger, Networks: w.nets, Eligibility: w.elig, Custody: c, Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return w.now },
	}, prometheus.NewRegistry())
	return h
}

func (h *custodyHarness) callback(t *testing.T, tr ports.CustodyTrade) domain.Callback {
	t.Helper()
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := h.svc.HandleCallback(context.Background(), domain.ProviderUdun, "application/json", raw)
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
	small.TradeID, small.Amount, small.RawAmount = "t2", d("0.5"), d("500000")
	h.callback(t, small)
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
		if _, err := h.svc.HandleCallback(ctx, domain.ProviderUdun, "", []byte(raw)); !apperr.Is(err, code) {
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
	if _, err := h.svc.HandleCallback(ctx, "OTHER", "", []byte("{}")); !apperr.Is(err, apperr.CodeNotFound) {
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
	// Nor is one a thousand times what the ledger expects (in its smallest unit).
	raw := d("500000000")
	h.custody.coins[0].Balance = &raw
	checks, err = h.cproc.Check(context.Background())
	if len(checks) != 0 || !errors.Is(err, errNotCompared) || !strings.Contains(err.Error(), "smallest unit") {
		t.Fatalf("smallest unit: %+v %v", checks, err)
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
	got, err := ResolveCustodyWithdrawal(context.Background(), h.store, unsent.ID, false, "", "ops", "not in the custodian's records", h.now)
	if err != nil || got.Status != domain.WithdrawalFailed || got.RejectReason != "CUSTODY_FAILED: resolved by ops: not in the custodian's records" {
		t.Fatalf("resolved %+v %v", got, err)
	}
	h.cround(t)
	if got := h.store.wds[unsent.ID]; got.UnfreezeJournal == "" {
		t.Fatalf("not released: %+v", got)
	}
	if _, err := ResolveCustodyWithdrawal(context.Background(), h.store, unsent.ID, true, "beef", "ops", "again", h.now); err == nil {
		t.Fatal("resolved twice")
	}
}

// Review finding B2: a fee above what was sent is in another unit or
// wrong; it is not booked (GAS_SUPPLY would never cover it) but counted.
func TestCustodyFeesAboveTheAmountAreNotBooked(t *testing.T) {
	h := newCustodyHarness(t)
	refused := &countingCounter{}
	h.svc.FeesRefused = refused
	wd := h.requestCustody(t, "20")
	h.cround(t)
	cb := h.callback(t, ports.CustodyTrade{
		TradeID: "w-9", Kind: domain.CallbackWithdrawal, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, BusinessID: wd.ID,
		TxHash: "f00d", Fee: d("1500000000"),
	})
	if got := h.store.wds[wd.ID]; got.Status != domain.WithdrawalConfirmed || !strings.Contains(cb.Detail, "not booked") {
		t.Fatalf("sent %+v, callback %+v", got, cb)
	}
	if len(h.store.fees) != 0 || refused.n != 1 {
		t.Fatalf("fees %v, refused %d", h.store.fees, refused.n)
	}
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
		if _, err := h.svc.HandleCallback(ctx, domain.ProviderUdun, "", []byte(long)); !apperr.Is(err, "WALLET_CALLBACK_SIGNATURE") {
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
	if _, err := h.svc.HandleCallback(ctx, domain.ProviderUdun, "", []byte("bad:{}")); err == nil || len(h.store.callbacks) != rejectedPerHour+1 {
		t.Fatalf("an hour later: %v, kept %d", err, len(h.store.callbacks))
	}
}
