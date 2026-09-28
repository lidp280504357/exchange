package postgres_test

import (
	"context"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
	"github.com/lidp280504357/exchange/migrations"
)

const net = "ETH-SEPOLIA"

func newStore(t *testing.T) *postgres.Store {
	t.Helper()
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Wallet(), log); err != nil {
		t.Fatal(err)
	}
	return postgres.NewStore(db, event.NewFactory("wallet-test", "t"))
}

func TestAddresses(t *testing.T) {
	store, ctx := newStore(t), context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	for i, user := range []string{alice, bob} {
		err := store.Tx(ctx, func(r ports.Repos) error {
			idx, err := r.Addresses().NextIndex(ctx, net)
			if err != nil || idx != uint32(i) { //nolint:gosec // two users
				t.Fatalf("index %d %v", idx, err)
			}
			addr := "0x" + strings.Repeat("0", 39) + strconv.Itoa(i+1)
			if err := r.Addresses().Insert(ctx, domain.Address{UserID: user, Network: net, Index: idx, Address: addr, CreatedAt: time.Now()}); err != nil {
				return err
			}
			return r.Emit(ctx, &walletv1.DepositAddressAssigned{UserId: user, Network: net, Address: addr, DerivationIndex: idx}, user)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := store.Read().Addresses().Get(ctx, bob, net)
	if err != nil || a == nil || a.Index != 1 || a.Address != "0x0000000000000000000000000000000000000002" {
		t.Fatalf("bob's address %+v %v", a, err)
	}
	if a, err := store.Read().Addresses().Get(ctx, bob, "OTHER"); err != nil || a != nil {
		t.Fatalf("no address on another network: %+v %v", a, err)
	}
	owners, err := store.Read().Addresses().Owners(ctx, net)
	if err != nil || len(owners) != 2 || owners["0x0000000000000000000000000000000000000001"] != alice {
		t.Fatalf("owners %v %v", owners, err)
	}
}

func TestDepositsAndBlocks(t *testing.T) {
	store, ctx := newStore(t), context.Background()
	user := uuid.NewString()
	hash := func(b byte) string { return "0x" + strings.Repeat(string("0123456789abcdef"[b%16]), 64) }
	now := time.Now().UTC().Truncate(time.Microsecond)
	mk := func(tx string, block uint64, status string) domain.Deposit {
		return domain.Deposit{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Asset: "ETH", Network: net,
			Address: "0x0000000000000000000000000000000000000001", TxHash: tx, LogIndex: domain.NativeLog, BlockNumber: block,
			BlockHash: "0xb", Amount: decimal.RequireFromString("0.002"), RawAmount: decimal.RequireFromString("2000000000000000"),
			Required: 12, Status: status, DetectedAt: now,
		}
	}
	d1, d2, d3 := mk(hash(1), 100, domain.StatusDetected), mk(hash(2), 105, domain.StatusConfirming), mk(hash(3), 90, domain.StatusConfirmed)
	junk := mk(hash(4), 101, domain.StatusRejected)
	junk.Asset, junk.Amount, junk.Reason, junk.Contract, junk.LogIndex = "", decimal.Zero, domain.ReasonUnsupportedToken, "0xjunk", 3
	err := store.Tx(ctx, func(r ports.Repos) error {
		for _, d := range []domain.Deposit{d1, d2, d3, junk} {
			if err := r.Deposits().Insert(ctx, d); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	read := store.Read().Deposits()
	got, err := read.Find(ctx, net, strings.ToUpper(hash(1))[:2]+hash(1)[2:], domain.NativeLog)
	if err != nil || got == nil || got.ID != d1.ID || !got.Amount.Equal(d1.Amount) || got.RawAmount.String() != "2000000000000000" {
		t.Fatalf("find %+v %v", got, err)
	}
	if j, err := read.Find(ctx, net, hash(4), 3); err != nil || j == nil || j.Asset != "" || j.Contract != "0xjunk" || j.Reason != domain.ReasonUnsupportedToken {
		t.Fatalf("unsupported token %+v %v", j, err)
	}
	if pending, err := read.Pending(ctx, net); err != nil || len(pending) != 2 {
		t.Fatalf("pending %v %v", pending, err)
	}
	if list, err := read.Unrequested(ctx, net); err != nil || len(list) != 1 || list[0].ID != d3.ID {
		t.Fatalf("unrequested %v %v", list, err)
	}
	if list, err := read.FromBlock(ctx, net, 101); err != nil || len(list) != 1 || list[0].ID != d2.ID {
		t.Fatalf("from block %v %v", list, err)
	}

	d3.RequestCredit("", now)
	d3.Credit(uuid.NewString(), now)
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Deposits().Update(ctx, d3) }); err != nil {
		t.Fatal(err)
	}
	back, err := read.GetForUpdate(ctx, d3.ID)
	if err != nil || back.Status != domain.StatusCredited || back.JournalID != d3.JournalID || !back.CreditRequested.Equal(now) {
		t.Fatalf("updated %+v %v", back, err)
	}

	page, err := read.ByUser(ctx, user, "", 2)
	if err != nil || len(page) != 2 || page[0].ID != junk.ID || page[1].ID != d3.ID {
		t.Fatalf("newest first %v %v", page, err)
	}
	page, err = read.ByUser(ctx, user, page[1].ID, 10)
	if err != nil || len(page) != 2 || page[0].ID != d2.ID {
		t.Fatalf("next page %v %v", page, err)
	}

	blocks := store.Read().Blocks()
	if c, err := blocks.Cursor(ctx, net); err != nil || c != 0 {
		t.Fatalf("no cursor yet: %d %v", c, err)
	}
	for n := uint64(10); n <= 15; n++ {
		if err := blocks.Save(ctx, net, n, hash(byte(n))); err != nil {
			t.Fatal(err)
		}
	}
	if c, _ := blocks.Cursor(ctx, net); c != 15 {
		t.Fatalf("cursor %d", c)
	}
	if err := blocks.Rewind(ctx, net, 13); err != nil {
		t.Fatal(err)
	}
	if c, _ := blocks.Cursor(ctx, net); c != 12 {
		t.Fatalf("rewound cursor %d", c)
	}
	if h, _ := blocks.Hash(ctx, net, 13); h != "" {
		t.Fatal("rewound blocks are forgotten")
	}
	if err := blocks.Prune(ctx, net, 12); err != nil {
		t.Fatal(err)
	}
	if h, _ := blocks.Hash(ctx, net, 11); h != "" {
		t.Fatal("pruned")
	}
	if h, _ := blocks.Hash(ctx, net, 12); h != hash(12) {
		t.Fatalf("kept %q", h)
	}
}

func TestOperations(t *testing.T) {
	store, ctx := newStore(t), context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	tx := "0x" + strings.Repeat("ab", 32)
	cmd := domain.Command{
		ID: uuid.NewString(), Network: net, Kind: domain.CommandSweep, Args: map[string]string{"min": "0.001"},
		Status: domain.CommandPending, RequestedBy: "cli:ops", CreatedAt: now,
	}
	sweep := domain.Sweep{
		ID: uuid.NewString(), Network: net, Address: "0x0000000000000000000000000000000000000001", Index: 0, Asset: "ETH",
		Amount: decimal.RequireFromString("0.0012"), Nonce: 0, TxHash: tx, Raw: "0x02", Status: domain.SweepBroadcast, CommandID: cmd.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	err := store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Commands().Insert(ctx, cmd); err != nil {
			return err
		}
		if err := r.Sweeps().Insert(ctx, sweep); err != nil {
			return err
		}
		fee := domain.ChainFee{TxHash: tx, Network: net, Asset: "ETH", Amount: decimal.RequireFromString("0.000042"), Purpose: domain.FeeSweep, Reference: sweep.ID}
		if err := r.ChainFees().Insert(ctx, fee); err != nil {
			return err
		}
		if err := r.ChainFees().Insert(ctx, fee); err != nil { // once per transaction
			return err
		}
		return r.Checks().Insert(ctx, domain.NewChainCheck(net, "ETH", decimal.RequireFromString("1"), decimal.RequireFromString("0.9"),
			decimal.Zero, 3, now))
	})
	if err != nil {
		t.Fatal(err)
	}
	read := store.Read()
	if pending, err := read.Commands().Pending(ctx, net); err != nil || len(pending) != 1 || pending[0].Args["min"] != "0.001" {
		t.Fatalf("pending commands %v %v", pending, err)
	}
	cmd.Status, cmd.Result, cmd.DoneAt = domain.CommandDone, "1 addresses swept", now
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Commands().Update(ctx, cmd) }); err != nil {
		t.Fatal(err)
	}
	if pending, _ := read.Commands().Pending(ctx, net); len(pending) != 0 {
		t.Fatal("a done command is not pending")
	}
	if open, err := read.Sweeps().Open(ctx, net); err != nil || len(open) != 1 || open[0].CommandID != cmd.ID || !open[0].Amount.Equal(sweep.Amount) {
		t.Fatalf("open sweeps %v %v", open, err)
	}
	fees, err := read.ChainFees().Unbooked(ctx, net)
	if err != nil || len(fees) != 1 || fees[0].Amount.String() != "0.000042" {
		t.Fatalf("unbooked fees %v %v", fees, err)
	}
	if err := read.ChainFees().MarkBooked(ctx, tx, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if fees, _ := read.ChainFees().Unbooked(ctx, net); len(fees) != 0 {
		t.Fatal("booked fees are not listed")
	}
	f := domain.Funding{
		TxHash: tx, Network: net, Asset: "ETH", AccountType: "GAS_SUPPLY", Amount: decimal.RequireFromString("0.005"),
		JournalID: uuid.NewString(), CreatedAt: now,
	}
	if err := read.Fundings().Insert(ctx, f); err != nil {
		t.Fatal(err)
	}
	if got, err := read.Fundings().Get(ctx, tx); err != nil || got == nil || got.AccountType != "GAS_SUPPLY" {
		t.Fatalf("funding %+v %v", got, err)
	}
	if err := read.Fundings().Insert(ctx, f); err == nil {
		t.Fatal("a transaction funds once")
	}
	checks, err := read.Checks().Latest(ctx, net)
	if err != nil || len(checks) != 1 || checks[0].Shortfall.String() != "-0.1" {
		t.Fatalf("checks %v %v", checks, err)
	}
}

func TestWithdrawalStorage(t *testing.T) {
	store, ctx := newStore(t), context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	user := uuid.NewString()
	payee := "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
	entry := domain.WithdrawAddress{
		ID: uuid.NewString(), UserID: user, Network: net, Address: payee, Label: "cold", CreatedAt: now,
		UsableAt: now.Add(time.Hour),
	}
	read := store.Read()
	if err := read.WithdrawAddresses().Insert(ctx, entry); err != nil {
		t.Fatal(err)
	}
	dup := entry
	dup.ID = uuid.NewString()
	if err := read.WithdrawAddresses().Insert(ctx, dup); err == nil {
		t.Fatal("an address is in a book once")
	}
	if got, err := read.WithdrawAddresses().Find(ctx, user, net, strings.ToLower(payee)); err != nil || got == nil || got.Label != "cold" {
		t.Fatalf("find %+v %v", got, err)
	}

	w := domain.Withdrawal{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Asset: "ETH", Network: net, Address: payee,
		Amount: decimal.RequireFromString("0.0011"), Fee: decimal.RequireFromString("0.0002"), Status: domain.WithdrawalRequested,
		RiskScore: 60, RiskReasons: []string{domain.RiskNewAccount, domain.RiskNewAddress}, ApprovalsRequired: 1,
		ValueUSDT: decimal.RequireFromString("3.3"), Nonce: -1, Required: 12, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Withdrawals().Insert(ctx, w) }); err != nil {
		t.Fatal(err)
	}
	w.FreezeJournal = uuid.NewString()
	w.Scored(domain.RiskResult{Score: 60, Reasons: w.RiskReasons, Approvals: 1}, now)
	if _, err := w.Approve("ops-1", now); err != nil {
		t.Fatal(err)
	}
	w.Broadcasted(7, "0x"+strings.Repeat("c", 64), now)
	if err := store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Attempts().Insert(ctx, domain.Attempt{
			TxHash: w.TxHash, WithdrawalID: w.ID, Nonce: 7, MaxFee: big.NewInt(3e9),
			MaxTip: big.NewInt(1e9), Raw: "0x02", CreatedAt: now,
		}); err != nil {
			return err
		}
		return r.Withdrawals().Update(ctx, w)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := read.Withdrawals().Get(ctx, w.ID)
	if err != nil || got.Status != domain.WithdrawalBroadcast || got.Nonce != 7 || len(got.Approvals) != 1 || got.RiskReasons[1] != domain.RiskNewAddress ||
		!got.Fee.Equal(w.Fee) || got.FreezeJournal != w.FreezeJournal {
		t.Fatalf("round trip %+v %v", got, err)
	}
	if list, err := read.Withdrawals().Unsettled(ctx, net); err != nil || len(list) != 1 {
		t.Fatalf("unsettled %v %v", list, err)
	}
	if list, err := read.Withdrawals().ByStatus(ctx, net, domain.WithdrawalBroadcast, domain.WithdrawalConfirming); err != nil || len(list) != 1 {
		t.Fatalf("by status %v %v", list, err)
	}
	if atts, err := read.Attempts().Of(ctx, w.ID); err != nil || len(atts) != 1 || atts[0].MaxFee.Int64() != 3e9 {
		t.Fatalf("attempts %v %v", atts, err)
	}
	if v, err := read.Withdrawals().ValueSince(ctx, user, now.Add(-time.Hour)); err != nil || v.String() != "3.3" {
		t.Fatalf("value %s %v", v, err)
	}

	hot := "0x00000000000000000000000000000000000000Aa"
	if n, err := read.Nonces().Peek(ctx, hot); err != nil || n != 0 {
		t.Fatalf("no nonce yet %d %v", n, err)
	}
	if err := read.Nonces().Advance(ctx, hot, 8); err != nil {
		t.Fatal(err)
	}
	if err := read.Nonces().Advance(ctx, hot, 5); err != nil {
		t.Fatal(err)
	}
	if n, _ := read.Nonces().Peek(ctx, strings.ToLower(hot)); n != 8 {
		t.Fatalf("nonces never go back: %d", n)
	}

	day := now.Truncate(24 * time.Hour)
	if p, err := read.Prices().Put(ctx, day, "ETH", decimal.NewFromInt(3000), "test"); err != nil || p.String() != "3000" {
		t.Fatalf("put %s %v", p, err)
	}
	if p, _ := read.Prices().Put(ctx, day, "ETH", decimal.NewFromInt(4000), "test"); p.String() != "3000" {
		t.Fatalf("the day's first price holds: %s", p)
	}
	if p, err := read.Prices().Get(ctx, day, "ETH"); err != nil || p == nil || p.String() != "3000" {
		t.Fatalf("get %v %v", p, err)
	}

	internal := domain.Deposit{
		ID: uuid.NewString(), Kind: domain.KindInternal, UserID: uuid.NewString(), Asset: "ETH", Network: net,
		Address: "0x0000000000000000000000000000000000000002", TxHash: "internal:" + w.ID, LogIndex: domain.NativeLog,
		Amount: decimal.RequireFromString("0.02"), RawAmount: decimal.RequireFromString("20000000000000000"), Status: domain.StatusCredited,
		JournalID: uuid.NewString(), DetectedAt: now, CreditedAt: now,
	}
	if err := read.Deposits().Insert(ctx, internal); err != nil {
		t.Fatalf("an internal deposit has no block: %v", err)
	}
	if got, err := read.Deposits().Find(ctx, net, "internal:"+w.ID, domain.NativeLog); err != nil || got == nil || got.Kind != domain.KindInternal {
		t.Fatalf("internal deposit %+v %v", got, err)
	}
}
