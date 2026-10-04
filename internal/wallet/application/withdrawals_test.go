package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

func (m *memStore) WithdrawAddresses() ports.WithdrawAddressRepo { return memBook{m} }
func (m *memStore) Withdrawals() ports.WithdrawalRepo            { return memWithdrawals{m} }
func (m *memStore) Attempts() ports.AttemptRepo                  { return memAttempts{m} }
func (m *memStore) Nonces() ports.NonceRepo                      { return memNonces{m} }
func (m *memStore) Prices() ports.PriceRepo                      { return memPrices{m} }

func (m *memStore) EmitWithdrawal(_ context.Context, msg proto.Message, userID string) error {
	if userID == domain.NoOwner {
		return errors.New("an event of the deposit of no user (NoOwner)")
	}
	m.wevents = append(m.wevents, msg)
	return nil
}

func (m *memStore) Audit(_ context.Context, msg proto.Message, _ string) error {
	m.audits = append(m.audits, msg)
	return nil
}

type memBook struct{ m *memStore }

func (b memBook) Insert(_ context.Context, a domain.WithdrawAddress) error {
	b.m.book[a.ID] = a
	return nil
}

func (b memBook) List(_ context.Context, user string) ([]domain.WithdrawAddress, error) {
	var out []domain.WithdrawAddress
	for _, a := range b.m.book {
		if a.UserID == user {
			out = append(out, a)
		}
	}
	return out, nil
}

func (b memBook) Find(_ context.Context, user, network, address string) (*domain.WithdrawAddress, error) {
	for _, a := range b.m.book {
		if a.UserID == user && a.Network == network && strings.EqualFold(a.Address, address) {
			return &a, nil
		}
	}
	return nil, nil
}

func (b memBook) Delete(_ context.Context, user, id string, _ time.Time) (bool, error) {
	a, ok := b.m.book[id]
	if !ok || a.UserID != user {
		return false, nil
	}
	delete(b.m.book, id)
	return true, nil
}

type memWithdrawals struct{ m *memStore }

func (w memWithdrawals) Insert(_ context.Context, x domain.Withdrawal) error {
	w.m.wds[x.ID] = x
	return nil
}

func (w memWithdrawals) Update(_ context.Context, x domain.Withdrawal) error {
	w.m.wds[x.ID] = x
	return nil
}

func (w memWithdrawals) Get(_ context.Context, id string) (*domain.Withdrawal, error) {
	if x, ok := w.m.wds[id]; ok {
		return &x, nil
	}
	return nil, nil
}

func (w memWithdrawals) GetForUpdate(ctx context.Context, id string) (*domain.Withdrawal, error) {
	return w.Get(ctx, id)
}

func (w memWithdrawals) all(keep func(domain.Withdrawal) bool) []domain.Withdrawal {
	var out []domain.Withdrawal
	for _, x := range w.m.wds {
		if keep(x) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b domain.Withdrawal) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (w memWithdrawals) ByUser(_ context.Context, user, _ string, _ int) ([]domain.Withdrawal, error) {
	return w.all(func(x domain.Withdrawal) bool { return x.UserID == user }), nil
}

func (w memWithdrawals) ByStatus(_ context.Context, network string, statuses ...string) ([]domain.Withdrawal, error) {
	return w.all(func(x domain.Withdrawal) bool { return x.Network == network && slices.Contains(statuses, x.Status) }), nil
}

func (w memWithdrawals) Page(_ context.Context, _ string, f ports.WithdrawalFilter) ([]domain.Withdrawal, error) {
	out := w.all(func(x domain.Withdrawal) bool {
		return (f.Status == "" || x.Status == f.Status) && (f.UserID == "" || x.UserID == f.UserID) &&
			(f.After == "" || (f.Oldest && x.ID > f.After) || (!f.Oldest && x.ID < f.After))
	})
	if !f.Oldest {
		slices.Reverse(out)
	}
	return out[:min(f.Limit, len(out))], nil
}

func (w memWithdrawals) Unreleased(_ context.Context, network string) ([]domain.Withdrawal, error) {
	return w.all(func(x domain.Withdrawal) bool { return x.Network == network && x.NeedsRelease() }), nil
}

func (w memWithdrawals) Unsettled(_ context.Context, network string) ([]domain.Withdrawal, error) {
	return w.all(func(x domain.Withdrawal) bool {
		return x.Network == network && x.InternalUserID == "" && x.SettleJournal == "" &&
			slices.Contains([]string{domain.WithdrawalBroadcast, domain.WithdrawalConfirming, domain.WithdrawalConfirmed}, x.Status)
	}), nil
}

func (w memWithdrawals) ValueSince(_ context.Context, user string, t time.Time) (decimal.Decimal, error) {
	sum := decimal.Zero
	for _, x := range w.all(func(x domain.Withdrawal) bool {
		return x.UserID == user && !x.CreatedAt.Before(t) && x.Status != domain.WithdrawalRejected && x.Status != domain.WithdrawalCanceled
	}) {
		sum = sum.Add(x.ValueUSDT)
	}
	return sum, nil
}

type memAttempts struct{ m *memStore }

func (a memAttempts) Insert(_ context.Context, x domain.Attempt) error {
	a.m.attempts = append(a.m.attempts, x)
	return nil
}

func (a memAttempts) Of(_ context.Context, id string) ([]domain.Attempt, error) {
	var out []domain.Attempt
	for i := len(a.m.attempts) - 1; i >= 0; i-- {
		if a.m.attempts[i].WithdrawalID == id {
			out = append(out, a.m.attempts[i])
		}
	}
	return out, nil
}

type memNonces struct{ m *memStore }

func (n memNonces) Peek(_ context.Context, address string) (uint64, error) {
	return n.m.nonce[strings.ToLower(address)], nil
}

func (n memNonces) Advance(_ context.Context, address string, next uint64) error {
	k := strings.ToLower(address)
	n.m.nonce[k] = max(n.m.nonce[k], next)
	return nil
}

type memPrices struct{ m *memStore }

func (p memPrices) Get(_ context.Context, day time.Time, asset string) (*decimal.Decimal, error) {
	if v, ok := p.m.prices[day.Format(time.DateOnly)+asset]; ok {
		return &v, nil
	}
	return nil, nil
}

func (p memPrices) Put(_ context.Context, day time.Time, asset string, price decimal.Decimal, _ string) (decimal.Decimal, error) {
	k := day.Format(time.DateOnly) + asset
	if v, ok := p.m.prices[k]; ok {
		return v, nil
	}
	p.m.prices[k] = price
	return price, nil
}

type fakeStepUps struct {
	tokens map[string]ports.StepUp // token -> context
}

func (f *fakeStepUps) Consume(_ context.Context, _, token string) (ports.StepUp, error) {
	su, ok := f.tokens[token]
	if !ok {
		return ports.StepUp{}, apperr.New(apperr.KindForbidden, "AUTH_STEP_UP_REQUIRED", "step up first")
	}
	delete(f.tokens, token)
	return su, nil
}

type fakeProfiles map[string]time.Time

func (f fakeProfiles) Created(_ context.Context, user string) (time.Time, error) { return f[user], nil }

type fakePrices struct{ usdt decimal.Decimal }

func (f fakePrices) USDT(context.Context, string) (decimal.Decimal, string, error) {
	return f.usdt, "test", nil
}

type withdrawHarness struct {
	*harness
	node    *fakeNode
	signer  *fakeSigner
	ledger  *fakeLedger
	stepUps *fakeStepUps
	proc    *Processor
	now     time.Time
}

func newWithdrawHarness(t *testing.T) *withdrawHarness {
	h := newHarness(t)
	h.nets.nets[0].WithdrawEnabled, h.nets.nets[0].MinWithdraw, h.nets.nets[0].WithdrawFee = true, d("0.001"), d("0.0002")
	w := &withdrawHarness{
		harness: h, now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
		node: &fakeNode{
			head: 1000, balances: map[string]*big.Int{strings.ToLower(hotWallet): eth("1")}, nonces: map[string]uint64{},
			receipts: map[string]*ports.Receipt{}, txs: map[string]*ports.Tx{},
		},
		signer: &fakeSigner{},
		ledger: &fakeLedger{
			system: map[string]decimal.Decimal{accountGasSupply: d("1")}, available: map[string]decimal.Decimal{"alice": d("2")},
			frozen: map[string]decimal.Decimal{},
		},
		stepUps: &fakeStepUps{tokens: map[string]ports.StepUp{}},
	}
	clock := func() time.Time { return w.now }
	h.svc.Now = clock
	h.svc.W = Withdrawals{
		StepUps: w.stepUps, Profiles: fakeProfiles{"alice": w.now.Add(-24 * time.Hour), "bob": w.now.Add(-24 * time.Hour)},
		Prices: fakePrices{usdt: d("2500")}, Ledger: w.ledger, Cooldown: time.Minute,
	}
	w.proc = NewProcessor(Processor{
		Store: h.store, Chain: w.node, Signer: w.signer, Ledger: w.ledger, Networks: h.nets, Log: slog.New(slog.DiscardHandler),
		Now: clock, Network: net, ChainID: 11155111,
	}, prometheus.NewRegistry())
	return w
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// stepUp issues a step-up token with a user's security context.
func (w *withdrawHarness) stepUp(token string, identities int, totp bool) string {
	w.stepUps.tokens[token] = ports.StepUp{
		Channel: "EMAIL", Identities: identities, TOTPEnabled: totp, DeviceFirstSeen: w.now.Add(-48 * time.Hour),
		IdentityChanged: w.now.Add(-48 * time.Hour), PasswordChanged: w.now.Add(-48 * time.Hour),
	}
	return token
}

func (w *withdrawHarness) round(t *testing.T) {
	t.Helper()
	if err := w.proc.Round(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func eventNames(msgs []proto.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, string(m.ProtoReflect().Descriptor().Name()))
	}
	return out
}

// An authenticator app bound a second ago does not raise the limits yet
// (variant B, 2026-10-04): with both identities, 500 USDT is over the 400
// a day of 20%, and the refusal says when the full limits come; bound a
// day and a second ago, the same withdrawal is within the full 2,000.
func TestANewAuthenticatorWaitsADayForTheLimits(t *testing.T) {
	w := newWithdrawHarness(t)
	ctx := context.Background()
	payee := "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
	if _, err := w.svc.AddAddress(ctx, "alice", AddressInput{Network: net, Address: payee, StepUp: w.stepUp("s1", 2, false)}); err != nil {
		t.Fatal(err)
	}
	w.now = w.now.Add(2 * time.Minute)
	w.ledger.available["alice"] = d("1")
	bound := func(token string, activated time.Time) string {
		w.stepUp(token, 2, true)
		su := w.stepUps.tokens[token]
		su.TOTPActivated = activated
		w.stepUps.tokens[token] = su
		return token
	}
	req := WithdrawalInput{Asset: "ETH", Network: net, Address: payee, Amount: d("0.2"), StepUp: bound("s2", w.now.Add(-time.Second))} // 500 USDT
	_, err := w.svc.RequestWithdrawal(ctx, "alice", req)
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != "WALLET_LIMIT_EXCEEDED" || e.Details["daily_limit"] != "400" ||
		e.Details["full_limits_at"] != w.now.Add(-time.Second).Add(24*time.Hour).UTC().Format(time.RFC3339) ||
		e.Details["full_daily_limit"] != "2000" || e.Details["full_monthly_limit"] != "20000" {
		t.Fatalf("a second after binding the app: %v %+v", err, e)
	}
	// The limits in effect, before any withdrawal (GET /v1/wallet/limits).
	activated := w.now.Add(-time.Hour)
	w.svc.W.Securities = fakeSecurities{"alice": {Identities: 2, TOTPEnabled: true, TOTPActivated: activated}}
	v, err := w.svc.Limits(ctx, "alice")
	if err != nil || v.Limits.Daily.String() != "400" || v.Full.Daily.String() != "2000" || !v.FullAt.Equal(activated.Add(24*time.Hour)) ||
		v.Settling != 24*time.Hour || !v.UsedToday.IsZero() || v.Identities != 2 || !v.TOTP {
		t.Fatalf("the limits while the app settles: %+v %v", v, err)
	}
	req.StepUp = bound("s3", w.now.Add(-24*time.Hour-time.Second))
	if _, err := w.svc.RequestWithdrawal(ctx, "alice", req); err != nil {
		t.Fatalf("a day and a second after binding the app: %v", err)
	}
	w.svc.W.Securities = fakeSecurities{"alice": {Identities: 2, TOTPEnabled: true, TOTPActivated: w.now.Add(-48 * time.Hour)}}
	if v, err := w.svc.Limits(ctx, "alice"); err != nil || v.Limits.Daily.String() != "2000" || !v.FullAt.IsZero() ||
		v.UsedToday.String() != "500" {
		t.Fatalf("the limits a day on, with the withdrawal: %+v %v", v, err)
	}
}

// fakeSecurities answers each user's security context.
type fakeSecurities map[string]ports.StepUp

func (f fakeSecurities) Security(_ context.Context, userID string) (ports.StepUp, error) {
	return f[userID], nil
}

func TestWithdrawalToChain(t *testing.T) {
	w := newWithdrawHarness(t)
	ctx := context.Background()
	payee := "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
	if _, err := w.svc.AddAddress(ctx, "alice", AddressInput{Network: net, Address: payee, Label: "cold"}); !apperr.Is(err, "AUTH_STEP_UP_REQUIRED") {
		t.Fatalf("adding needs a step-up: %v", err)
	}
	entry, err := w.svc.AddAddress(ctx, "alice", AddressInput{Network: net, Address: strings.ToLower(payee), Label: "cold", StepUp: w.stepUp("s1", 1, false)})
	if err != nil || entry.Address != payee || !entry.UsableAt.Equal(w.now.Add(time.Minute)) {
		t.Fatalf("entry %+v %v", entry, err)
	}
	req := WithdrawalInput{Asset: "ETH", Network: net, Address: payee, Amount: d("0.01")}
	if _, err := w.svc.RequestWithdrawal(ctx, "alice", req); !apperr.Is(err, "WALLET_ADDRESS_COOLDOWN") {
		t.Fatalf("within the cooling-off period: %v", err)
	}
	w.now = w.now.Add(2 * time.Minute)
	other := req
	other.Address = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	if _, err := w.svc.RequestWithdrawal(ctx, "alice", other); !apperr.Is(err, "WALLET_ADDRESS_NOT_WHITELISTED") {
		t.Fatalf("an address outside the book: %v", err)
	}
	small := req
	small.Amount = d("0.0009")
	if _, err := w.svc.RequestWithdrawal(ctx, "alice", small); !apperr.Is(err, "WALLET_BELOW_MINIMUM") {
		t.Fatalf("below the minimum: %v", err)
	}
	big := req
	big.Amount, big.StepUp = d("0.2"), w.stepUp("s2", 1, false) // 500 USDT > 400 a day at 20%
	if _, err := w.svc.RequestWithdrawal(ctx, "alice", big); !apperr.Is(err, "WALLET_LIMIT_EXCEEDED") {
		t.Fatalf("over the single-identity daily limit: %v", err)
	}
	if _, err := w.svc.RequestWithdrawal(ctx, "alice", req); !apperr.Is(err, "AUTH_STEP_UP_REQUIRED") {
		t.Fatalf("a request needs a step-up: %v", err)
	}

	req.StepUp = w.stepUp("s3", 1, false)
	wd, err := w.svc.RequestWithdrawal(ctx, "alice", req)
	if err != nil {
		t.Fatal(err)
	}
	// A day-old account and an address added minutes ago: one reviewer.
	if wd.Status != domain.WithdrawalReview || !slices.Equal(wd.RiskReasons, []string{domain.RiskNewAccount, domain.RiskNewAddress}) ||
		wd.ApprovalsRequired != 1 || wd.Fee.String() != "0.0002" || wd.ValueUSDT.String() != "25" {
		t.Fatalf("scored %+v", wd)
	}
	if !w.ledger.frozen["alice"].Equal(d("0.0102")) {
		t.Fatalf("frozen %s", w.ledger.frozen["alice"])
	}
	w.round(t)
	if len(w.signer.requests) != 0 {
		t.Fatal("nothing is signed before the review")
	}
	if _, err := ReviewWithdrawal(ctx, w.store, Review{ID: wd.ID, Reviewer: "ops-1", Reason: "looks fine", Approve: true}, w.now); err != nil {
		t.Fatal(err)
	}
	w.node.nonces[strings.ToLower(hotWallet)] = 5
	w.round(t)
	if len(w.signer.requests) != 1 {
		t.Fatalf("signed once: %+v", w.signer.requests)
	}
	sr := w.signer.requests[0]
	if sr.Purpose != "WITHDRAWAL" || sr.Nonce != 5 || sr.To != payee || sr.ApprovedBy != "ops-1" || sr.Value.Cmp(eth("0.01")) != 0 {
		t.Fatalf("sign request %+v", sr)
	}
	got := w.store.wds[wd.ID]
	if got.Status != domain.WithdrawalBroadcast || got.Nonce != 5 || got.SettleJournal == "" || len(w.node.sent) != 1 {
		t.Fatalf("broadcast and settled: %+v", got)
	}
	if !w.ledger.frozen["alice"].IsZero() || !w.ledger.system[accountWithdrawalPending].Equal(d("0.01")) {
		t.Fatalf("settled in the ledger: frozen %s, pending %s", w.ledger.frozen["alice"], w.ledger.system[accountWithdrawalPending])
	}
	if w.store.nonce[strings.ToLower(hotWallet)] != 6 {
		t.Fatal("the nonce is taken after signing")
	}

	// Stuck for ten minutes: a replacement on the same nonce, fees up.
	w.now = w.now.Add(11 * time.Minute)
	w.round(t)
	if len(w.signer.requests) != 2 || w.signer.requests[1].Nonce != 5 || w.signer.requests[1].MaxFee.Cmp(sr.MaxFee) <= 0 {
		t.Fatalf("replacement %+v", w.signer.requests)
	}
	replaced := w.store.wds[wd.ID].TxHash
	if replaced == got.TxHash {
		t.Fatal("the withdrawal follows its newest attempt")
	}
	// The first attempt gets mined after all.
	w.node.head = 1001
	w.node.receipts[got.TxHash] = &ports.Receipt{Succeeded: true, BlockNumber: 1001, GasUsed: transferGas, EffectiveGasPrice: gwei(2)}
	w.round(t)
	if s := w.store.wds[wd.ID]; s.Status != domain.WithdrawalConfirming || s.TxHash != got.TxHash || s.Confirmations != 1 {
		t.Fatalf("confirming on the mined attempt: %+v", s)
	}
	if f := w.store.fees[got.TxHash]; f.Purpose != domain.FeeWithdrawal || f.Amount.String() != "0.000042" || f.JournalID == "" {
		t.Fatalf("the gas of the mined attempt is booked: %+v", f)
	}
	w.node.head = 1003 // the harness network wants 3 confirmations
	w.round(t)
	if s := w.store.wds[wd.ID]; s.Status != domain.WithdrawalConfirmed || s.Confirmations != 3 {
		t.Fatalf("confirmed %+v", s)
	}
	if names := eventNames(w.store.wevents); names[len(names)-1] != "WithdrawalConfirmed" {
		t.Fatalf("events %v", names)
	}
}

func TestWithdrawalCancelRejectAndInternal(t *testing.T) {
	w := newWithdrawHarness(t)
	ctx := context.Background()
	payee := "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
	if _, err := w.svc.AddAddress(ctx, "alice", AddressInput{Network: net, Address: payee, StepUp: w.stepUp("a1", 2, true)}); err != nil {
		t.Fatal(err)
	}
	bobs := w.address("bob")
	if _, err := w.svc.AddAddress(ctx, "alice", AddressInput{Network: net, Address: bobs, StepUp: w.stepUp("a2", 2, true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.AddAddress(ctx, "bob", AddressInput{Network: net, Address: bobs, StepUp: w.stepUp("a3", 2, true)}); !apperr.Is(err, "WALLET_OWN_ADDRESS") {
		t.Fatalf("one's own deposit address: %v", err)
	}
	w.now = w.now.Add(2 * time.Minute)

	// Canceled before review: the funds come back.
	c, err := w.svc.RequestWithdrawal(ctx, "alice", WithdrawalInput{Asset: "ETH", Network: net, Address: payee, Amount: d("0.1"), StepUp: w.stepUp("b1", 2, true)})
	if err != nil {
		t.Fatal(err)
	}
	if c, err = w.svc.CancelWithdrawal(ctx, "alice", c.ID); err != nil || c.Status != domain.WithdrawalCanceled || c.UnfreezeJournal == "" {
		t.Fatalf("canceled %+v %v", c, err)
	}
	if !w.ledger.available["alice"].Equal(d("2")) {
		t.Fatalf("released: %s", w.ledger.available["alice"])
	}
	if _, err := w.svc.CancelWithdrawal(ctx, "bob", c.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("someone else's withdrawal: %v", err)
	}

	// Rejected by a reviewer: released by the processor.
	r, err := w.svc.RequestWithdrawal(ctx, "alice", WithdrawalInput{Asset: "ETH", Network: net, Address: payee, Amount: d("0.1"), StepUp: w.stepUp("b2", 2, true)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReviewWithdrawal(ctx, w.store, Review{ID: r.ID, Reviewer: "ops-1", Reason: "unusual", Approve: false}, w.now); err != nil {
		t.Fatal(err)
	}
	w.round(t)
	if s := w.store.wds[r.ID]; s.Status != domain.WithdrawalRejected || s.UnfreezeJournal == "" || !w.ledger.available["alice"].Equal(d("2")) {
		t.Fatalf("rejected and released %+v", s)
	}
	if len(w.store.audits) != 1 {
		t.Fatal("a review is audited")
	}

	// To bob's deposit address: no fee, completed in the ledger.
	in, err := w.svc.RequestWithdrawal(ctx, "alice", WithdrawalInput{Asset: "ETH", Network: net, Address: bobs, Amount: d("0.1"), StepUp: w.stepUp("b3", 2, true)})
	if err != nil || !in.Fee.IsZero() || in.InternalUserID != "bob" {
		t.Fatalf("internal %+v %v", in, err)
	}
	if _, err := ReviewWithdrawal(ctx, w.store, Review{ID: in.ID, Reviewer: "ops-1", Reason: "fine", Approve: true}, w.now); err != nil {
		t.Fatal(err)
	}
	w.round(t)
	if s := w.store.wds[in.ID]; s.Status != domain.WithdrawalConfirmed || s.SettleJournal == "" {
		t.Fatalf("internal completed %+v", s)
	}
	if !w.ledger.available["bob"].Equal(d("0.1")) || len(w.signer.requests) != 0 {
		t.Fatalf("bob got 0.1 in the ledger without a signature: %s", w.ledger.available["bob"])
	}
	var internal *domain.Deposit
	for _, dep := range w.store.deposits {
		if dep.UserID == "bob" && dep.Kind == domain.KindInternal {
			internal = &dep
		}
	}
	if internal == nil || internal.Status != domain.StatusCredited || internal.TxHash != "internal:"+in.ID {
		t.Fatalf("bob's internal deposit %+v", internal)
	}

	// The ledger refuses a freeze beyond the balance.
	w.ledger.available["alice"] = d("0.05")
	_, err = w.svc.RequestWithdrawal(ctx, "alice", WithdrawalInput{Asset: "ETH", Network: net, Address: payee, Amount: d("0.1"), StepUp: w.stepUp("b4", 2, true)})
	if !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("refused freeze: %v", err)
	}
	names := eventNames(w.store.wevents)
	for _, want := range []string{"WithdrawalRequested", "WithdrawalRiskScored", "WithdrawalCanceled", "WithdrawalRejected", "WithdrawalApproved", "WithdrawalConfirmed"} {
		if !slices.Contains(names, want) {
			t.Errorf("no %s in %v", want, names)
		}
	}
}

func TestWithdrawalWaitsAndFails(t *testing.T) {
	w := newWithdrawHarness(t)
	ctx := context.Background()
	payee := "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
	if _, err := w.svc.AddAddress(ctx, "alice", AddressInput{Network: net, Address: payee, StepUp: w.stepUp("a1", 2, true)}); err != nil {
		t.Fatal(err)
	}
	w.now = w.now.Add(2 * time.Minute)
	wd, err := w.svc.RequestWithdrawal(ctx, "alice", WithdrawalInput{Asset: "ETH", Network: net, Address: payee, Amount: d("0.5"), StepUp: w.stepUp("b1", 2, true)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReviewWithdrawal(ctx, w.store, Review{ID: wd.ID, Reviewer: "ops-1", Reason: "fine", Approve: true}, w.now); err != nil {
		t.Fatal(err)
	}
	w.node.balances[strings.ToLower(hotWallet)] = eth("0.1")
	w.round(t)
	if s := w.store.wds[wd.ID]; s.Status != domain.WithdrawalApproved || len(w.signer.requests) != 0 {
		t.Fatalf("a short hot wallet: it waits %+v", s)
	}
	w.node.balances[strings.ToLower(hotWallet)] = eth("1")
	w.proc.Signer = refusingSigner{}
	w.round(t)
	if s := w.store.wds[wd.ID]; s.Status != domain.WithdrawalFailed || !strings.HasPrefix(s.RejectReason, "SIGNER_REFUSED") {
		t.Fatalf("a signer refusal fails it: %+v", s)
	}
	w.round(t)
	if s := w.store.wds[wd.ID]; s.UnfreezeJournal == "" || !w.ledger.available["alice"].Equal(d("2")) {
		t.Fatalf("nothing was broadcast: the funds come back %+v", s)
	}
	if w.store.nonce[strings.ToLower(hotWallet)] != 0 {
		t.Fatal("a refused signature takes no nonce")
	}
}

type refusingSigner struct{}

func (refusingSigner) HotWallet(context.Context) (string, error) { return hotWallet, nil }

func (refusingSigner) Sign(context.Context, ports.SignRequest) (ports.Signed, error) {
	return ports.Signed{}, apperr.New(apperr.KindForbidden, "SIGNER_REFUSED", "no").WithDetail("reason", "daily limit")
}

func TestValidateAddress(t *testing.T) {
	w := newWithdrawHarness(t)
	ctx := context.Background()
	bobAddr, _, err := w.svc.DepositAddress(ctx, "bob", "ETH", net)
	if err != nil {
		t.Fatal(err)
	}
	aliceAddr, _, err := w.svc.DepositAddress(ctx, "alice", "ETH", net)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, address, reason string
		internal              bool
	}{
		{"outside", "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", "", false},
		{"another user's deposit address", bobAddr.Address, "", true},
		{"own deposit address", aliceAddr.Address, domain.ReasonAddressOwn, false},
		{"typo", "0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", domain.ReasonAddressChecksum, false},
	} {
		v, err := w.svc.ValidateAddress(ctx, "alice", "", net, c.address, "")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if v.Valid != (c.reason == "") || v.Reason != c.reason || v.Internal != c.internal {
			t.Errorf("%s: %+v", c.name, v)
		}
	}
	if _, err := w.svc.ValidateAddress(ctx, "alice", "BTC", net, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", ""); err == nil {
		t.Error("an asset the network does not carry")
	}
	nets, err := w.svc.NetworksOf(ctx, "eth")
	if err != nil || len(nets) != 1 || nets[0].Network != net {
		t.Fatalf("networks %+v %v", nets, err)
	}
}

// On the platform's own wallets a withdrawal of a suspended asset is passed
// over and counted waiting, not left holding up the ones behind it: an
// internal transfer after it completes (review of ebb8aaa).
func TestASuspendedAssetDoesNotHoldUpTheHotWallet(t *testing.T) {
	w := newWithdrawHarness(t)
	ctx := context.Background()
	payee := "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
	bobs := w.address("bob")
	for i, a := range []string{payee, bobs} {
		if _, err := w.svc.AddAddress(ctx, "alice", AddressInput{Network: net, Address: a, StepUp: w.stepUp(fmt.Sprintf("a%d", i), 2, true)}); err != nil {
			t.Fatal(err)
		}
	}
	w.now = w.now.Add(2 * time.Minute)
	var ids []string
	for i, a := range []string{payee, bobs} {
		wd, err := w.svc.RequestWithdrawal(ctx, "alice", WithdrawalInput{Asset: "ETH", Network: net, Address: a, Amount: d("0.1"), StepUp: w.stepUp(fmt.Sprintf("b%d", i), 2, true)})
		if err != nil {
			t.Fatal(err)
		}
		if wd.Status == domain.WithdrawalReview {
			if _, err := ReviewWithdrawal(ctx, w.store, Review{ID: wd.ID, Reviewer: "ops-1", Reason: "fine", Approve: true}, w.now); err != nil {
				t.Fatal(err)
			}
		}
		ids = append(ids, wd.ID)
		w.now = w.now.Add(time.Second)
	}
	w.store.suspended["ETH"] = domain.Suspension{Asset: "ETH", Reason: "a test", SuspendedBy: "ops", SuspendedAt: w.now, Shortfall: decimal.Zero}
	w.round(t)
	if s := w.store.wds[ids[0]]; s.Status != domain.WithdrawalApproved || len(w.signer.requests) != 0 {
		t.Fatalf("sent while suspended: %+v", s)
	}
	if s := w.store.wds[ids[1]]; s.Status != domain.WithdrawalConfirmed {
		t.Fatalf("the internal transfer behind it waited: %+v", s)
	}
	var m dto.Metric
	if err := w.proc.waiting.Write(&m); err != nil || m.GetGauge().GetValue() != 1 {
		t.Fatalf("waiting %v %v", m.GetGauge().GetValue(), err)
	}
}
