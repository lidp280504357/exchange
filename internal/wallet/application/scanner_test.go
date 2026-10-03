package application

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// memStore is an in-memory ports.Store for single-goroutine tests.
type memStore struct {
	addresses map[string]domain.Address // user|network
	deposits  map[string]domain.Deposit
	hashes    map[uint64]string
	cursor    uint64
	events    []proto.Message
	commands  []domain.Command
	sweeps    map[string]domain.Sweep
	fees      map[string]domain.ChainFee
	units     map[string]domain.FeeUnit
	suspended map[string]domain.Suspension
	fundings  map[string]domain.Funding
	checks    []domain.ChainCheck
	book      map[string]domain.WithdrawAddress
	wds       map[string]domain.Withdrawal
	attempts  []domain.Attempt
	nonce     map[string]uint64
	prices    map[string]decimal.Decimal
	wevents   []proto.Message
	audits    []proto.Message
	callbacks []domain.Callback
	// race, when set, runs before the next deposit is inserted and may
	// refuse it (a concurrent request that won the unique index).
	race func(x domain.Deposit) error
}

func newMemStore() *memStore {
	return &memStore{
		addresses: map[string]domain.Address{}, deposits: map[string]domain.Deposit{}, hashes: map[uint64]string{},
		sweeps: map[string]domain.Sweep{}, fees: map[string]domain.ChainFee{}, units: map[string]domain.FeeUnit{}, suspended: map[string]domain.Suspension{},
		fundings: map[string]domain.Funding{},
		book:     map[string]domain.WithdrawAddress{}, wds: map[string]domain.Withdrawal{}, nonce: map[string]uint64{},
		prices: map[string]decimal.Decimal{},
	}
}

func (m *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error { return fn(m) }
func (m *memStore) Read() ports.Repos                                      { return m }
func (m *memStore) Addresses() ports.AddressRepo                           { return memAddresses{m} }
func (m *memStore) Deposits() ports.DepositRepo                            { return memDeposits{m} }
func (m *memStore) Blocks() ports.BlockRepo                                { return memBlocks{m} }

func (m *memStore) Emit(_ context.Context, msg proto.Message, _ string) error {
	m.events = append(m.events, msg)
	return nil
}

// take returns and forgets the events emitted so far.
func (m *memStore) take() []proto.Message {
	out := m.events
	m.events = nil
	return out
}

type memAddresses struct{ m *memStore }

func (a memAddresses) Get(_ context.Context, userID, network string) (*domain.Address, error) {
	if x, ok := a.m.addresses[userID+"|"+network]; ok {
		return &x, nil
	}
	return nil, nil
}

func (a memAddresses) NextIndex(context.Context, string) (uint32, error) {
	return uint32(len(a.m.addresses)), nil //nolint:gosec // a handful in tests
}

func (a memAddresses) Insert(_ context.Context, x domain.Address) error {
	a.m.addresses[x.UserID+"|"+x.Network] = x
	return nil
}

func (a memAddresses) Owners(_ context.Context, network string) (map[string]string, error) {
	out := map[string]string{}
	for _, x := range a.m.addresses {
		if x.Network == network {
			out[strings.ToLower(x.Address)] = x.UserID
		}
	}
	return out, nil
}

type memDeposits struct{ m *memStore }

func (d memDeposits) Insert(_ context.Context, x domain.Deposit) error {
	if race := d.m.race; race != nil {
		d.m.race = nil
		if err := race(x); err != nil {
			return err
		}
	}
	d.m.deposits[x.ID] = x
	return nil
}

func (d memDeposits) Update(_ context.Context, x domain.Deposit) error {
	d.m.deposits[x.ID] = x
	return nil
}

func (d memDeposits) Find(_ context.Context, network, txHash string, logIndex int64) (*domain.Deposit, error) {
	for _, x := range d.m.deposits {
		if x.Network == network && x.TxHash == txHash && x.LogIndex == logIndex {
			return &x, nil
		}
	}
	return nil, nil
}

func (d memDeposits) GetForUpdate(_ context.Context, id string) (*domain.Deposit, error) {
	if x, ok := d.m.deposits[id]; ok {
		return &x, nil
	}
	return nil, nil
}

func (d memDeposits) list(keep func(domain.Deposit) bool) []domain.Deposit {
	var out []domain.Deposit
	for _, x := range d.m.deposits {
		if keep(x) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b domain.Deposit) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (d memDeposits) Pending(_ context.Context, network string) ([]domain.Deposit, error) {
	return d.list(func(x domain.Deposit) bool { return x.Network == network && x.Pending() }), nil
}

func (d memDeposits) Unrequested(_ context.Context, network string) ([]domain.Deposit, error) {
	return d.list(func(x domain.Deposit) bool {
		return x.Network == network && x.Status == domain.StatusConfirmed && x.CreditRequested.IsZero()
	}), nil
}

func (d memDeposits) FromBlock(_ context.Context, network string, n uint64) ([]domain.Deposit, error) {
	return d.list(func(x domain.Deposit) bool { return x.Network == network && x.Pending() && x.BlockNumber >= n }), nil
}

func (d memDeposits) ByUser(context.Context, string, string, int) ([]domain.Deposit, error) {
	return nil, nil
}

func (d memDeposits) ByTransfer(_ context.Context, network, txHash, address string) (*domain.Deposit, error) {
	for _, x := range d.list(func(x domain.Deposit) bool {
		return x.Network == network && strings.EqualFold(x.TxHash, txHash) && strings.EqualFold(x.Address, address)
	}) {
		return &x, nil
	}
	return nil, nil
}

func (d memDeposits) Get(ctx context.Context, id string) (*domain.Deposit, error) {
	return d.GetForUpdate(ctx, id)
}

func (d memDeposits) Page(_ context.Context, f ports.DepositFilter) ([]domain.Deposit, error) {
	out := d.list(func(x domain.Deposit) bool {
		return (f.UserID == "" || x.UserID == f.UserID) && (f.Status == "" || x.Status == f.Status) &&
			(f.Network == "" || x.Network == f.Network) && (!f.Attention || x.Attention()) &&
			(!f.ManualPending || (x.Source == domain.SourceManual && x.CallbackAt.IsZero())) && (f.After == "" || x.ID < f.After)
	})
	slices.Reverse(out)
	return out[:min(len(out), f.Limit)], nil
}

type memBlocks struct{ m *memStore }

func (b memBlocks) Cursor(context.Context, string) (uint64, error) { return b.m.cursor, nil }

func (b memBlocks) Hash(_ context.Context, _ string, n uint64) (string, error) {
	return b.m.hashes[n], nil
}

func (b memBlocks) Save(_ context.Context, _ string, n uint64, hash string) error {
	b.m.hashes[n], b.m.cursor = hash, n
	return nil
}

func (b memBlocks) Rewind(_ context.Context, _ string, n uint64) error {
	for k := range b.m.hashes {
		if k >= n {
			delete(b.m.hashes, k)
		}
	}
	b.m.cursor = n - 1
	return nil
}

func (b memBlocks) Prune(_ context.Context, _ string, below uint64) error {
	for k := range b.m.hashes {
		if k < below {
			delete(b.m.hashes, k)
		}
	}
	return nil
}

// fakeChain is a chain of blocks named by fork: block n of fork f has
// hash f<n>; forks[n] says which fork block n is on.
type fakeChain struct {
	head   uint64
	forks  map[uint64]string
	native map[string][]ports.Transfer // by block hash
	tokens map[string][]ports.Transfer // by block hash
}

func (c *fakeChain) hash(n uint64) string {
	f := c.forks[n]
	if f == "" {
		f = "h"
	}
	return fmt.Sprintf("%s%d", f, n)
}

func (c *fakeChain) Head(context.Context) (uint64, error) { return c.head, nil }

func (c *fakeChain) Block(_ context.Context, n uint64, watched map[string]string) (ports.Block, error) {
	b := ports.Block{Number: n, Hash: c.hash(n), ParentHash: c.hash(n - 1)}
	for _, t := range c.native[b.Hash] {
		if _, ok := watched[t.To]; ok {
			t.BlockNumber, t.BlockHash = n, b.Hash
			b.Transfers = append(b.Transfers, t)
		}
	}
	return b, nil
}

func (c *fakeChain) TokenTransfers(_ context.Context, from, to uint64, watched []string) ([]ports.Transfer, error) {
	var out []ports.Transfer
	for n := from; n <= to; n++ {
		for _, t := range c.tokens[c.hash(n)] {
			if slices.Contains(watched, t.To) {
				t.BlockNumber, t.BlockHash = n, c.hash(n)
				out = append(out, t)
			}
		}
	}
	return out, nil
}

func (c *fakeChain) Decimals(_ context.Context, contract string) (int32, error) {
	if contract == "" {
		return 18, nil
	}
	return 6, nil
}

type fakeNetworks struct{ nets []domain.Network }

func (f *fakeNetworks) Network(_ context.Context, asset, network string) (domain.Network, error) {
	for _, n := range f.nets {
		if n.Asset == asset && n.Network == network {
			return n, nil
		}
	}
	return domain.Network{}, domain.ErrUnknownNetwork
}

func (f *fakeNetworks) OnNetwork(_ context.Context, network string) ([]domain.Network, error) {
	var out []domain.Network
	for _, n := range f.nets {
		if n.Network == network {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeNetworks) ForAsset(_ context.Context, asset string) ([]domain.Network, error) {
	var out []domain.Network
	for _, n := range f.nets {
		if asset == "" || n.Asset == asset {
			out = append(out, n)
		}
	}
	return out, nil
}

type fakeEligibility map[string]string // user -> refusal code

func (f fakeEligibility) Check(_ context.Context, userID, _ string) (bool, string, error) {
	code, refused := f[userID]
	return !refused, code, nil
}

type fakeDeriver struct{}

func (fakeDeriver) Address(i uint32) (string, error) {
	return fmt.Sprintf("0x%040x", 0xa0+i), nil
}

const net = "ETH-SEPOLIA"

func eth(s string) *big.Int {
	v, _ := decimal.RequireFromString(s).Shift(18).BigInt(), 0
	return v
}

type harness struct {
	t     *testing.T
	store *memStore
	chain *fakeChain
	nets  *fakeNetworks
	elig  fakeEligibility
	svc   *Service
	scan  *Scanner
}

func newHarness(t *testing.T) *harness {
	h := &harness{
		t: t, store: newMemStore(), elig: fakeEligibility{},
		chain: &fakeChain{head: 100, forks: map[uint64]string{}, native: map[string][]ports.Transfer{}, tokens: map[string][]ports.Transfer{}},
		nets: &fakeNetworks{nets: []domain.Network{
			{Asset: "ETH", Network: net, Decimals: 18, Confirmations: 3, MinDeposit: decimal.RequireFromString("0.001"), Enabled: true},
			{Asset: "USDC", Network: net, Contract: "0xusdc", Decimals: 6, Confirmations: 3, MinDeposit: decimal.NewFromInt(1), Enabled: true},
		}},
	}
	log := slog.New(slog.DiscardHandler)
	h.svc = &Service{Store: h.store, Networks: h.nets, Eligibility: h.elig, Deriver: fakeDeriver{}, Log: log, Now: time.Now}
	h.scan = NewScanner(Scanner{
		Store: h.store, Chain: h.chain, Networks: h.nets, Eligibility: h.elig, Log: log, Now: time.Now, Network: net, Batch: 5,
	}, prometheus.NewRegistry())
	return h
}

func (h *harness) address(user string) string {
	a, _, err := h.svc.DepositAddress(context.Background(), user, "ETH", net)
	if err != nil {
		h.t.Fatal(err)
	}
	return strings.ToLower(a.Address)
}

// mine moves the head to n and scans until caught up.
func (h *harness) mine(n uint64) {
	h.chain.head = n
	for i := 0; i < 10 && h.store.cursor < n; i++ {
		if err := h.scan.Round(context.Background()); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := h.scan.Round(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) deposit(tx string) domain.Deposit {
	for _, d := range h.store.deposits {
		if d.TxHash == tx {
			return d
		}
	}
	h.t.Fatalf("no deposit for %s", tx)
	return domain.Deposit{}
}

func types(msgs []proto.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, string(m.ProtoReflect().Descriptor().Name()))
	}
	return out
}

func TestScannerDeposits(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.address("alice"), h.address("bob")
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositAddressAssigned", "DepositAddressAssigned"}) {
		t.Fatal(got)
	}
	h.mine(100) // starts at the head
	if h.store.cursor != 100 {
		t.Fatalf("cursor %d", h.store.cursor)
	}

	h.chain.native["h101"] = []ports.Transfer{
		{TxHash: "0xA1", LogIndex: domain.NativeLog, To: alice, Amount: eth("0.002")},
		{TxHash: "0xb1", LogIndex: domain.NativeLog, To: bob, Amount: eth("0.0005")},     // below the minimum
		{TxHash: "0xc1", LogIndex: domain.NativeLog, To: "0xstranger", Amount: eth("1")}, // not ours
	}
	h.chain.tokens["h102"] = []ports.Transfer{
		{TxHash: "0xd1", LogIndex: 4, To: alice, Contract: "0xusdc", Amount: big.NewInt(2_500_000)},
		{TxHash: "0xe1", LogIndex: 7, To: bob, Contract: "0xjunk", Amount: big.NewInt(99)},
	}
	h.mine(102)
	a := h.deposit("0xa1")
	if a.Status != domain.StatusConfirming || a.Confirmations != 2 || a.Amount.String() != "0.002" || a.Asset != "ETH" || a.RawAmount.String() != "2000000000000000" {
		t.Fatalf("alice's deposit %+v", a)
	}
	if b := h.deposit("0xb1"); !b.Unclaimed || b.Reason != domain.ReasonBelowMinimum {
		t.Fatalf("bob's small deposit %+v", b)
	}
	if u := h.deposit("0xd1"); u.Asset != "USDC" || u.Amount.String() != "2.5" || u.Confirmations != 1 {
		t.Fatalf("token deposit %+v", u)
	}
	if j := h.deposit("0xe1"); j.Status != domain.StatusRejected || j.Reason != domain.ReasonUnsupportedToken || j.RawAmount.String() != "99" {
		t.Fatalf("unsupported token %+v", j)
	}
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositDetected", "DepositDetected", "DepositDetected", "DepositRejected"}) {
		t.Fatal(got)
	}

	h.mine(103)
	confirmed := h.store.take()
	if got := types(confirmed); !slices.Equal(got, []string{"DepositConfirmed", "DepositConfirmed"}) {
		t.Fatal(got)
	}
	byTx := map[string]*walletv1.Deposit{}
	for _, m := range confirmed {
		d := m.(*walletv1.DepositConfirmed).GetDeposit()
		byTx[d.GetTxHash()] = d
	}
	if d := byTx["0xa1"]; d.GetUnclaimed() || d.GetAmount() != "0.002" || d.GetStatus() != domain.StatusConfirmed {
		t.Fatalf("alice's confirmation %v", d)
	}
	if d := byTx["0xb1"]; !d.GetUnclaimed() || d.GetReason() != domain.ReasonBelowMinimum {
		t.Fatalf("bob's confirmation %v", d)
	}
	h.mine(104)
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositConfirmed"}) {
		t.Fatalf("the token deposit confirms a block later: %v", got)
	}
	h.mine(110)
	if got := h.store.take(); len(got) != 0 {
		t.Fatalf("sent to the ledger once: %v", types(got))
	}

	// The ledger's journals come back.
	ctx := context.Background()
	if err := h.svc.OnCredited(ctx, a.ID, "j1"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.OnCredited(ctx, a.ID, "j1"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.OnCredited(ctx, h.deposit("0xb1").ID, "j2"); err != nil {
		t.Fatal(err)
	}
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositCredited", "DepositCredited"}) {
		t.Fatal(got)
	}
	if a := h.deposit("0xa1"); a.Status != domain.StatusCredited || a.JournalID != "j1" {
		t.Fatalf("credited %+v", a)
	}
	if b := h.deposit("0xb1"); b.Status != domain.StatusRejected {
		t.Fatalf("an unclaimed deposit ends REJECTED %+v", b)
	}
}

func TestScannerReorganization(t *testing.T) {
	h := newHarness(t)
	alice := h.address("alice")
	h.mine(100)
	h.chain.native["h102"] = []ports.Transfer{
		{TxHash: "0x01", LogIndex: domain.NativeLog, To: alice, Amount: eth("0.01")}, // survives, moved to f103
		{TxHash: "0x02", LogIndex: domain.NativeLog, To: alice, Amount: eth("0.02")}, // dropped
	}
	h.mine(102)
	if h.deposit("0x01").Status != domain.StatusConfirming || h.deposit("0x02").Status != domain.StatusConfirming {
		t.Fatal("both seen")
	}
	h.store.take()

	// Blocks from 102 on are replaced; block 101 stays.
	for n := uint64(102); n <= 120; n++ {
		h.chain.forks[n] = "f"
	}
	h.chain.native["f103"] = []ports.Transfer{{TxHash: "0x01", LogIndex: domain.NativeLog, To: alice, Amount: eth("0.01")}}
	h.mine(104)
	events := types(h.store.take())
	if !slices.Contains(events, "DepositOrphaned") || !slices.Contains(events, "DepositDetected") {
		t.Fatalf("events %v", events)
	}
	if d := h.deposit("0x01"); d.Status != domain.StatusConfirming || d.BlockNumber != 103 || d.BlockHash != "f103" || d.Confirmations != 2 {
		t.Fatalf("the surviving deposit follows its new block %+v", d)
	}
	if d := h.deposit("0x02"); d.Status != domain.StatusOrphaned {
		t.Fatalf("the dropped deposit is orphaned %+v", d)
	}
	if h.store.hashes[102] != "f102" || h.store.cursor != 104 {
		t.Fatalf("rescanned: %v cursor %d", h.store.hashes, h.store.cursor)
	}
	if len(h.store.deposits) != 2 {
		t.Fatal("a rescan does not duplicate deposits")
	}
	h.mine(106)
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositConfirmed"}) {
		t.Fatal(got)
	}
}

func TestScannerHoldsAndRoutes(t *testing.T) {
	h := newHarness(t)
	alice, carol := h.address("alice"), h.address("carol")
	h.mine(100)
	h.nets.nets[0].Enabled = false // ETH deposits closed
	h.elig["carol"] = reasonUserClosed
	h.chain.native["h101"] = []ports.Transfer{
		{TxHash: "0x0a", LogIndex: domain.NativeLog, To: alice, Amount: eth("0.5")},
		{TxHash: "0x0c", LogIndex: domain.NativeLog, To: carol, Amount: eth("0.5")},
	}
	h.mine(105)
	h.store.take()
	if d := h.deposit("0x0a"); d.Status != domain.StatusConfirmed || !d.CreditRequested.IsZero() {
		t.Fatalf("held while deposits are closed %+v", d)
	}
	h.nets.nets[0].Enabled = true
	h.mine(106)
	byTx := map[string]*walletv1.Deposit{}
	for _, m := range h.store.take() {
		d := m.(*walletv1.DepositConfirmed).GetDeposit()
		byTx[d.GetTxHash()] = d
	}
	if d := byTx["0x0a"]; d == nil || d.GetUnclaimed() {
		t.Fatalf("released to the user %v", d)
	}
	if d := byTx["0x0c"]; d == nil || !d.GetUnclaimed() || d.GetReason() != domain.ReasonAccountClosed {
		t.Fatalf("a closed account's deposit is unclaimed %v", d)
	}
}
