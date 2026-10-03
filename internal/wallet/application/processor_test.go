package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

func (m *memStore) Commands() ports.CommandRepo   { return memCommands{m} }
func (m *memStore) Sweeps() ports.SweepRepo       { return memSweeps{m} }
func (m *memStore) ChainFees() ports.ChainFeeRepo { return memFees{m} }
func (m *memStore) Fundings() ports.FundingRepo   { return memFundings{m} }
func (m *memStore) Checks() ports.CheckRepo       { return memChecks{m} }

func (m *memStore) Suspensions() ports.SuspensionRepo { return memSuspensions{m} }

type memSuspensions struct{ m *memStore }

func (s memSuspensions) Get(_ context.Context, asset string) (*domain.Suspension, error) {
	if x, ok := s.m.suspended[asset]; ok {
		return &x, nil
	}
	return nil, nil
}

func (s memSuspensions) List(context.Context) ([]domain.Suspension, error) {
	var out []domain.Suspension
	for _, x := range s.m.suspended {
		out = append(out, x)
	}
	slices.SortFunc(out, func(a, b domain.Suspension) int { return strings.Compare(a.Asset, b.Asset) })
	return out, nil
}

func (s memSuspensions) Put(_ context.Context, x domain.Suspension) (bool, error) {
	if _, ok := s.m.suspended[x.Asset]; ok {
		return false, nil
	}
	s.m.suspended[x.Asset] = x
	return true, nil
}

func (s memSuspensions) Delete(_ context.Context, asset string) (bool, error) {
	_, ok := s.m.suspended[asset]
	delete(s.m.suspended, asset)
	return ok, nil
}

func (s memSuspensions) Watch(_ context.Context, asset string) (domain.ShortfallWatch, error) {
	if w, ok := s.m.watches[asset]; ok {
		return w, nil
	}
	return domain.ShortfallWatch{Asset: asset, Accepted: decimal.Zero}, nil
}

func (s memSuspensions) Watches(context.Context) ([]domain.ShortfallWatch, error) {
	var out []domain.ShortfallWatch
	for _, w := range s.m.watches {
		if !w.SuspectSince.IsZero() || w.Accepted.IsPositive() {
			out = append(out, w)
		}
	}
	slices.SortFunc(out, func(a, b domain.ShortfallWatch) int { return strings.Compare(a.Asset, b.Asset) })
	return out, nil
}

func (s memSuspensions) Suspect(ctx context.Context, asset string, now time.Time) (time.Time, error) {
	w, _ := s.Watch(ctx, asset)
	if w.SuspectSince.IsZero() {
		w.SuspectSince = now
		s.m.watches[asset] = w
	}
	return w.SuspectSince, nil
}

func (s memSuspensions) Clear(_ context.Context, asset string, accepted bool) error {
	w, ok := s.m.watches[asset]
	if !ok {
		return nil
	}
	w.SuspectSince = time.Time{}
	if accepted {
		w.Accepted, w.AcceptedUntil, w.AcceptedBy = decimal.Zero, time.Time{}, ""
	}
	s.m.watches[asset] = w
	return nil
}

func (s memSuspensions) Accept(ctx context.Context, asset string, amount decimal.Decimal, until time.Time, by string) error {
	w, _ := s.Watch(ctx, asset)
	w.Accepted, w.AcceptedUntil, w.AcceptedBy = amount, until, by
	s.m.watches[asset] = w
	return nil
}

func (a memAddresses) List(_ context.Context, network string) ([]domain.Address, error) {
	var out []domain.Address
	for _, x := range a.m.addresses {
		if x.Network == network {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b domain.Address) int { return int(a.Index) - int(b.Index) })
	return out, nil
}

type memCommands struct{ m *memStore }

func (c memCommands) Insert(_ context.Context, x domain.Command) error {
	c.m.commands = append(c.m.commands, x)
	return nil
}

func (c memCommands) Pending(_ context.Context, network string) ([]domain.Command, error) {
	var out []domain.Command
	for _, x := range c.m.commands {
		if x.Network == network && x.Status == domain.CommandPending {
			out = append(out, x)
		}
	}
	return out, nil
}

func (c memCommands) Update(_ context.Context, x domain.Command) error {
	for i := range c.m.commands {
		if c.m.commands[i].ID == x.ID {
			c.m.commands[i] = x
		}
	}
	return nil
}

func (c memCommands) Recent(context.Context, int) ([]domain.Command, error) { return c.m.commands, nil }

type memSweeps struct{ m *memStore }

func (s memSweeps) Insert(_ context.Context, x domain.Sweep) error {
	s.m.sweeps[x.ID] = x
	return nil
}

func (s memSweeps) Update(_ context.Context, x domain.Sweep) error {
	s.m.sweeps[x.ID] = x
	return nil
}

func (s memSweeps) Open(_ context.Context, network string) ([]domain.Sweep, error) {
	var out []domain.Sweep
	for _, x := range s.m.sweeps {
		if x.Network == network && x.Status == domain.SweepBroadcast {
			out = append(out, x)
		}
	}
	return out, nil
}

type memFees struct{ m *memStore }

func (f memFees) Insert(_ context.Context, x domain.ChainFee) error {
	if _, ok := f.m.fees[x.TxHash]; !ok {
		if x.Status == "" {
			x.Status = domain.FeeBookable
		}
		f.m.fees[x.TxHash] = x
	}
	return nil
}

func (f memFees) where(keep func(domain.ChainFee) bool) []domain.ChainFee {
	var out []domain.ChainFee
	for _, x := range f.m.fees {
		if keep(x) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b domain.ChainFee) int { return strings.Compare(a.TxHash, b.TxHash) })
	return out
}

func (f memFees) Unbooked(_ context.Context, network string) ([]domain.ChainFee, error) {
	return f.where(func(x domain.ChainFee) bool {
		return x.Network == network && x.JournalID == "" && x.Status == domain.FeeBookable
	}), nil
}

func (f memFees) MarkBooked(_ context.Context, tx, journal string) (string, error) {
	x, ok := f.m.fees[tx]
	if !ok || x.JournalID != "" {
		return "", nil
	}
	was := x.Status
	x.JournalID, x.Status = journal, domain.FeeBookable
	f.m.fees[tx] = x
	return was, nil
}

func (f memFees) Held(context.Context) ([]domain.ChainFee, error) {
	return f.where(func(x domain.ChainFee) bool { return x.Status == domain.FeeHeld }), nil
}

func (f memFees) OfReference(_ context.Context, reference string) ([]domain.ChainFee, error) {
	return f.where(func(x domain.ChainFee) bool { return x.Reference == reference }), nil
}

func (f memFees) Resolve(_ context.Context, x domain.ChainFee) (bool, error) {
	cur, ok := f.m.fees[x.TxHash]
	if !ok || cur.JournalID != "" || cur.Status == domain.FeeWrittenOff {
		return false, nil
	}
	f.m.fees[x.TxHash] = x
	return true, nil
}

func (f memFees) Unit(_ context.Context, provider, asset, network string) (*domain.FeeUnit, error) {
	if u, ok := f.m.units[provider+"|"+asset+"|"+network]; ok {
		return &u, nil
	}
	return nil, nil
}

func (f memFees) PutUnit(_ context.Context, u domain.FeeUnit) error {
	f.m.units[u.Provider+"|"+u.Asset+"|"+u.Network] = u
	return nil
}

func (f memFees) Units(context.Context) ([]domain.FeeUnit, error) {
	var out []domain.FeeUnit
	for _, u := range f.m.units {
		out = append(out, u)
	}
	return out, nil
}

type memFundings struct{ m *memStore }

func (f memFundings) Get(_ context.Context, tx string) (*domain.Funding, error) {
	if x, ok := f.m.fundings[tx]; ok {
		return &x, nil
	}
	return nil, nil
}

func (f memFundings) Insert(_ context.Context, x domain.Funding) error {
	f.m.fundings[x.TxHash] = x
	return nil
}

type memChecks struct{ m *memStore }

func (c memChecks) Insert(_ context.Context, x domain.ChainCheck) error {
	c.m.checks = append(c.m.checks, x)
	return nil
}

func (c memChecks) Latest(context.Context, string) ([]domain.ChainCheck, error) {
	return c.m.checks, nil
}

// fakeNode is the chain the processor talks to.
type fakeNode struct {
	head     uint64
	balances map[string]*big.Int // lower-case
	nonces   map[string]uint64
	sent     []string
	receipts map[string]*ports.Receipt
	txs      map[string]*ports.Tx
}

func (n *fakeNode) Head(context.Context) (uint64, error) { return n.head, nil }

func (n *fakeNode) Balance(_ context.Context, a string) (*big.Int, error) {
	if b, ok := n.balances[strings.ToLower(a)]; ok {
		return b, nil
	}
	return new(big.Int), nil
}

func (n *fakeNode) PendingNonce(_ context.Context, a string) (uint64, error) {
	return n.nonces[strings.ToLower(a)], nil
}

func (n *fakeNode) Fees(context.Context) (*big.Int, *big.Int, error) { return gwei(1), gwei(1), nil }

func (n *fakeNode) SendRaw(_ context.Context, raw string) error {
	n.sent = append(n.sent, raw)
	return nil
}

func (n *fakeNode) Receipt(_ context.Context, h string) (*ports.Receipt, error) {
	return n.receipts[h], nil
}

func (n *fakeNode) Transaction(_ context.Context, h string) (*ports.Tx, error) { return n.txs[h], nil }

const hotWallet = "0x00000000000000000000000000000000000000Aa"

type fakeSigner struct{ requests []ports.SignRequest }

func (s *fakeSigner) HotWallet(context.Context) (string, error) { return hotWallet, nil }

func (s *fakeSigner) Sign(_ context.Context, r ports.SignRequest) (ports.Signed, error) {
	s.requests = append(s.requests, r)
	sum := sha256.Sum256([]byte(r.ID))
	return ports.Signed{Raw: "raw-" + r.ID, TxHash: "0x" + hex.EncodeToString(sum[:]), From: "deposit"}, nil
}

type fakeLedger struct {
	system map[string]decimal.Decimal
	booked []string
	// balances[user] is the available/frozen of the user's asset.
	available map[string]decimal.Decimal
	frozen    map[string]decimal.Decimal
	journals  map[string]string // key -> journal
	down      bool
}

func (l *fakeLedger) once(key string, fn func() error) (string, error) {
	if l.down {
		return "", errors.New("ledger unreachable")
	}
	if l.journals == nil {
		l.journals = map[string]string{}
	}
	if j, ok := l.journals[key]; ok {
		return j, nil
	}
	if err := fn(); err != nil {
		return "", err
	}
	l.journals[key] = "j-" + key
	return l.journals[key], nil
}

func (l *fakeLedger) Freeze(_ context.Context, key, user, _ string, amount decimal.Decimal, _ string) (string, error) {
	return l.once(key, func() error {
		if l.available[user].LessThan(amount) {
			return apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")
		}
		l.available[user] = l.available[user].Sub(amount)
		l.frozen[user] = l.frozen[user].Add(amount)
		return nil
	})
}

func (l *fakeLedger) Unfreeze(_ context.Context, key, user, _ string, amount decimal.Decimal, _ string) (string, error) {
	return l.once(key, func() error {
		l.frozen[user] = l.frozen[user].Sub(amount)
		l.available[user] = l.available[user].Add(amount)
		return nil
	})
}

func (l *fakeLedger) Settle(_ context.Context, key, user, _ string, amount, fee decimal.Decimal, _ string) (string, error) {
	return l.once("settle:"+key, func() error {
		l.frozen[user] = l.frozen[user].Sub(amount.Add(fee))
		l.system[accountWithdrawalPending] = l.system[accountWithdrawalPending].Add(amount)
		return nil
	})
}

func (l *fakeLedger) TransferInternal(_ context.Context, key, from, to, _ string, amount decimal.Decimal, _ string) (string, error) {
	return l.once("internal:"+key, func() error {
		l.frozen[from] = l.frozen[from].Sub(amount)
		l.available[to] = l.available[to].Add(amount)
		return nil
	})
}

func (l *fakeLedger) BookChainFee(_ context.Context, key, _ string, amount decimal.Decimal, _ string) (string, error) {
	if l.system[accountGasSupply].LessThan(amount) {
		return "", errors.New("LEDGER_INSUFFICIENT_BALANCE")
	}
	l.system[accountGasSupply] = l.system[accountGasSupply].Sub(amount)
	l.system[accountWithdrawalPending] = l.system[accountWithdrawalPending].Add(amount)
	l.booked = append(l.booked, key)
	return "j-" + key[:8], nil
}

func (l *fakeLedger) Fund(_ context.Context, key, account, _ string, amount decimal.Decimal, _ string) (string, error) {
	l.system[account] = l.system[account].Add(amount)
	l.system[accountDepositPending] = l.system[accountDepositPending].Sub(amount)
	return "j-fund-" + key[:8], nil
}

func (l *fakeLedger) SystemBalances(context.Context, string) (map[string]decimal.Decimal, error) {
	return l.system, nil
}

func (l *fakeLedger) ReleaseUnclaimed(_ context.Context, id, user, _ string, amount decimal.Decimal, _, _ string) (string, error) {
	return l.once("deposit-release:"+id, func() error {
		l.available[user] = l.available[user].Add(amount)
		return nil
	})
}

func (l *fakeLedger) UnclaimedRelease(_ context.Context, id string) (string, error) {
	if l.down {
		return "", errors.New("ledger unreachable")
	}
	return l.journals["deposit-release:"+id], nil
}

func gwei(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000)) }

func TestProcessorSweepsFundsAndChecks(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.address("alice"), h.address("bob")
	node := &fakeNode{
		head:     500,
		balances: map[string]*big.Int{alice: eth("0.0012"), bob: eth("0.0005"), strings.ToLower(hotWallet): eth("0.01")},
		nonces:   map[string]uint64{alice: 0},
		receipts: map[string]*ports.Receipt{}, txs: map[string]*ports.Tx{},
	}
	signer, ledger := &fakeSigner{}, &fakeLedger{system: map[string]decimal.Decimal{
		accountDepositPending: decimal.RequireFromString("-0.0017"),
	}}
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	p := NewProcessor(Processor{
		Store: h.store, Chain: node, Signer: signer, Ledger: ledger, Networks: h.nets, Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return now }, Network: net, ChainID: 11155111,
	}, prometheus.NewRegistry())
	ctx := context.Background()
	queue := func(kind string, args map[string]string) domain.Command {
		c, err := Queue(ctx, h.store, net, kind, args, "ops", now)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	command := func(id string) domain.Command {
		for _, c := range h.store.commands {
			if c.ID == id {
				return c
			}
		}
		t.Fatalf("no command %s", id)
		return domain.Command{}
	}

	sweep := queue(domain.CommandSweep, map[string]string{"min": "0.001"})
	if err := p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if c := command(sweep.ID); c.Status != domain.CommandDone || !strings.HasPrefix(c.Result, "1 addresses swept") {
		t.Fatalf("sweep command %+v", c)
	}
	if len(signer.requests) != 1 {
		t.Fatalf("only alice holds the minimum: %+v", signer.requests)
	}
	req := signer.requests[0]
	cost := new(big.Int).Mul(gwei(3), big.NewInt(transferGas)) // (2 x base + tip) x gas
	if req.Purpose != "SWEEP" || req.To != hotWallet || req.Index != 0 || req.Value.Cmp(new(big.Int).Sub(eth("0.0012"), cost)) != 0 {
		t.Fatalf("sweep request %+v", req)
	}
	if len(node.sent) != 1 || len(h.store.sweeps) != 1 {
		t.Fatalf("broadcast %v", node.sent)
	}
	var s domain.Sweep
	for _, x := range h.store.sweeps {
		s = x
	}

	// A lost broadcast goes out again after a minute; the receipt settles it.
	now = now.Add(2 * time.Minute)
	if err := p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if len(node.sent) != 2 {
		t.Fatalf("rebroadcast: %v", node.sent)
	}
	node.receipts[s.TxHash] = &ports.Receipt{Succeeded: true, BlockNumber: 501, GasUsed: transferGas, EffectiveGasPrice: gwei(2)}
	if err := p.Round(ctx); err == nil {
		t.Fatal("GAS_SUPPLY is empty: the fee cannot be booked yet")
	}
	if h.store.sweeps[s.ID].Status != domain.SweepConfirmed || h.store.fees[s.TxHash].Amount.String() != "0.000042" {
		t.Fatalf("sweep %+v fee %+v", h.store.sweeps[s.ID], h.store.fees[s.TxHash])
	}

	// Funding GAS_SUPPLY from a platform transfer to the hot wallet.
	fundTx := "0x" + strings.Repeat("f", 64)
	node.txs[fundTx] = &ports.Tx{From: "0x0000000000000000000000000000000000000fff", To: strings.ToLower(hotWallet), Value: eth("0.005"), BlockNumber: 499}
	node.receipts[fundTx] = &ports.Receipt{Succeeded: true, BlockNumber: 499}
	early := queue(domain.CommandFund, map[string]string{"tx": fundTx})
	_ = p.Round(ctx)
	if c := command(early.ID); c.Status != domain.CommandPending || c.Result != "waiting for confirmations (2/3)" {
		t.Fatalf("funding before its confirmations: %+v", c)
	}
	node.head = 520
	if err := p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if c := command(early.ID); c.Status != domain.CommandDone || h.store.fundings[fundTx].Amount.String() != "0.005" {
		t.Fatalf("funding %+v", c)
	}
	if len(ledger.booked) != 1 || h.store.fees[s.TxHash].JournalID == "" {
		t.Fatal("once funded, the waiting fee is booked")
	}
	if c := queue(domain.CommandFund, map[string]string{"tx": fundTx}); p.Round(ctx) != nil || !strings.HasPrefix(command(c.ID).Result, "already booked") {
		t.Fatalf("a funding is booked once: %+v", command(c.ID))
	}
	sweepTx := "0x" + strings.Repeat("e", 64)
	node.txs[sweepTx] = &ports.Tx{From: bob, To: strings.ToLower(hotWallet), Value: eth("0.0004"), BlockNumber: 400}
	if c := queue(domain.CommandFund, map[string]string{"tx": sweepTx}); p.Round(ctx) != nil || command(c.ID).Status != domain.CommandFailed {
		t.Fatalf("a sweep is not a funding: %+v", command(c.ID))
	}

	// The chain check: held = hot 0.01 + alice's dust + bob 0.0005.
	node.balances[alice] = new(big.Int).Sub(cost, new(big.Int).Mul(gwei(2), big.NewInt(transferGas)))
	check := queue(domain.CommandReconcile, nil)
	if err := p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	last := h.store.checks[len(h.store.checks)-1]
	// Expected: −(DEPOSIT_PENDING + WITHDRAWAL_PENDING) = 0.0017 + 0.005 − 0.000042.
	if last.Chain.String() != "0.010521" || last.Ledger.String() != "0.006658" || !last.Unbooked.IsZero() || last.Addresses != 3 {
		t.Fatalf("check %+v", last)
	}
	if !last.Shortfall.IsNegative() || command(check.ID).Status != domain.CommandDone {
		t.Fatalf("the wallets hold more than expected (unfunded platform ETH): %+v", last)
	}
}
