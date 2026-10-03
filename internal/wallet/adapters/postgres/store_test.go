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
	if was, err := read.ChainFees().MarkBooked(ctx, tx, uuid.NewString()); err != nil || was != domain.FeeBookable {
		t.Fatalf("booked: %q %v", was, err)
	}
	if was, err := read.ChainFees().MarkBooked(ctx, tx, uuid.NewString()); err != nil || was != "" {
		t.Fatalf("booked again: %q %v", was, err)
	}
	if fees, _ := read.ChainFees().Unbooked(ctx, net); len(fees) != 0 {
		t.Fatal("booked fees are not listed")
	}

	// A custodian's fee held for a person (review ④): not bookable until
	// resolved, resolved once.
	held := domain.ChainFee{
		TxHash: "UDUN:t-1", Network: "TRON", Asset: "USDT", Amount: decimal.RequireFromString("1500"), Purpose: domain.FeeWithdrawal,
		Reference: "w-1", Status: domain.FeeHeld, HoldReason: "above 5 USDT",
	}
	if err := read.ChainFees().Insert(ctx, held); err != nil {
		t.Fatal(err)
	}
	if fees, err := read.ChainFees().Unbooked(ctx, "TRON"); err != nil || len(fees) != 0 {
		t.Fatalf("a held fee is bookable: %v %v", fees, err)
	}
	if list, err := read.ChainFees().Held(ctx); err != nil || len(list) != 1 || list[0].HoldReason != "above 5 USDT" || list[0].CreatedAt.IsZero() {
		t.Fatalf("held %+v %v", list, err)
	}
	held.Status, held.Amount, held.ResolvedBy, held.Resolution, held.ResolvedAt = domain.FeeBookable, decimal.RequireFromString("1.5"), "ops",
		"the statement", now
	if done, err := read.ChainFees().Resolve(ctx, held); err != nil || !done {
		t.Fatalf("resolved %v %v", done, err)
	}
	if fees, err := read.ChainFees().OfReference(ctx, "w-1"); err != nil || len(fees) != 1 || fees[0].Status != domain.FeeBookable ||
		!fees[0].Amount.Equal(decimal.RequireFromString("1.5")) || fees[0].ResolvedBy != "ops" || fees[0].HoldReason != "above 5 USDT" {
		t.Fatalf("of the withdrawal %+v %v", fees, err)
	}
	if fees, err := read.ChainFees().Unbooked(ctx, "TRON"); err != nil || len(fees) != 1 {
		t.Fatalf("bookable once resolved: %v %v", fees, err)
	}
	// Booked, it takes no more decisions.
	if _, err := read.ChainFees().MarkBooked(ctx, "UDUN:t-1", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if done, err := read.ChainFees().Resolve(ctx, held); err != nil || done {
		t.Fatalf("resolved once booked %v %v", done, err)
	}
	// Written off while it waited to be booked, then booked by the ledger
	// all the same: the booking stands.
	waiting := domain.ChainFee{
		TxHash: "UDUN:t-3", Network: "TRON", Asset: "USDT", Amount: decimal.RequireFromString("0.7"),
		Purpose: domain.FeeWithdrawal, Reference: "w-3",
	}
	if err := read.ChainFees().Insert(ctx, waiting); err != nil {
		t.Fatal(err)
	}
	waiting.Status, waiting.HoldReason, waiting.ResolvedBy, waiting.Resolution, waiting.ResolvedAt = domain.FeeWrittenOff,
		"waited to be booked", "ops", "nothing to fund it", now
	if done, err := read.ChainFees().Resolve(ctx, waiting); err != nil || !done {
		t.Fatalf("written off while waiting: %v %v", done, err)
	}
	if was, err := read.ChainFees().MarkBooked(ctx, "UDUN:t-3", uuid.NewString()); err != nil || was != domain.FeeWrittenOff {
		t.Fatalf("the ledger booked it meanwhile: %q %v", was, err)
	}
	if fees, err := read.ChainFees().OfReference(ctx, "w-3"); err != nil || len(fees) != 1 || fees[0].Status != domain.FeeBookable ||
		fees[0].JournalID == "" {
		t.Fatalf("the booking stands: %+v %v", fees, err)
	}
	// Written off without a time: refused by the table.
	off := domain.ChainFee{
		TxHash: "UDUN:t-2", Network: "TRON", Asset: "USDT", Amount: decimal.RequireFromString("2"), Purpose: domain.FeeWithdrawal,
		Reference: "w-2", Status: domain.FeeWrittenOff,
	}
	if err := read.ChainFees().Insert(ctx, off); err == nil {
		t.Fatal("a written-off fee without its decision")
	}
	// An asset's withdrawals suspended once, then lifted (review B4).
	sus := domain.Suspension{
		Asset: "USDT", Shortfall: decimal.RequireFromString("10"), Reason: "missing on two checks", SuspendedBy: domain.SuspendedBySystem,
		SuspendedAt: now,
	}
	if done, err := read.Suspensions().Put(ctx, sus); err != nil || !done {
		t.Fatalf("suspended %v %v", done, err)
	}
	if done, err := read.Suspensions().Put(ctx, sus); err != nil || done {
		t.Fatalf("suspended twice %v %v", done, err)
	}
	if got, err := read.Suspensions().Get(ctx, "USDT"); err != nil || got == nil || !got.Shortfall.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("get %+v %v", got, err)
	}
	if list, err := read.Suspensions().List(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
	if done, err := read.Suspensions().Delete(ctx, "USDT"); err != nil || !done {
		t.Fatalf("lifted %v %v", done, err)
	}
	if got, err := read.Suspensions().Get(ctx, "USDT"); err != nil || got != nil {
		t.Fatalf("still suspended %+v %v", got, err)
	}
	// What the checks keep between checks (review of ebb8aaa): the first
	// sighting stands, a clear forgets it, an acceptance has an end.
	if w, err := read.Suspensions().Watch(ctx, "BTC"); err != nil || !w.SuspectSince.IsZero() || !w.Accepted.IsZero() {
		t.Fatalf("nothing kept %+v %v", w, err)
	}
	if since, err := read.Suspensions().Suspect(ctx, "BTC", now); err != nil || !since.Equal(now) {
		t.Fatalf("first sighting %v %v", since, err)
	}
	if since, err := read.Suspensions().Suspect(ctx, "BTC", now.Add(time.Hour)); err != nil || !since.Equal(now) {
		t.Fatalf("the first sighting stands %v %v", since, err)
	}
	until := now.Add(24 * time.Hour)
	if err := read.Suspensions().Accept(ctx, "BTC", decimal.RequireFromString("0.0003"), until, "ops"); err != nil {
		t.Fatal(err)
	}
	if list, err := read.Suspensions().Watches(ctx); err != nil || len(list) != 1 || !list[0].AcceptedUntil.Equal(until) || list[0].AcceptedBy != "ops" {
		t.Fatalf("watches %+v %v", list, err)
	}
	if err := read.Suspensions().Clear(ctx, "BTC", false); err != nil {
		t.Fatal(err)
	}
	if w, err := read.Suspensions().Watch(ctx, "BTC"); err != nil || !w.SuspectSince.IsZero() || !w.Accepted.Equal(decimal.RequireFromString("0.0003")) {
		t.Fatalf("cleared the suspicion only %+v %v", w, err)
	}
	if err := read.Suspensions().Clear(ctx, "BTC", true); err != nil {
		t.Fatal(err)
	}
	if list, err := read.Suspensions().Watches(ctx); err != nil || len(list) != 0 {
		t.Fatalf("nothing kept after a full clear %+v %v", list, err)
	}
	unit := domain.FeeUnit{Provider: "UDUN", Asset: "USDT", Network: "TRON", Unit: domain.FeeUnitSelf, ConfirmedBy: "ops", Reason: "tronscan", ConfirmedAt: now}
	if got, err := read.ChainFees().Unit(ctx, "UDUN", "USDT", "TRON"); err != nil || got != nil {
		t.Fatalf("no unit yet %+v %v", got, err)
	}
	if err := read.ChainFees().PutUnit(ctx, unit); err != nil {
		t.Fatal(err)
	}
	unit.Unit = domain.FeeUnitMain
	if err := read.ChainFees().PutUnit(ctx, unit); err != nil {
		t.Fatal(err)
	}
	if got, err := read.ChainFees().Unit(ctx, "UDUN", "USDT", "TRON"); err != nil || got == nil || got.Unit != domain.FeeUnitMain {
		t.Fatalf("unit %+v %v", got, err)
	}
	if list, err := read.ChainFees().Units(ctx); err != nil || len(list) != 1 {
		t.Fatalf("units %+v %v", list, err)
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
	// Two more of another user: the admin pages filter and page by ID.
	other := uuid.NewString()
	var more []string
	for range 2 {
		x := w
		x.ID, x.UserID, x.Status, x.Nonce, x.TxHash, x.FreezeJournal = uuid.Must(uuid.NewV7()).String(), other, domain.WithdrawalRequested, -1, "", ""
		x.Approvals = nil
		if err := store.Tx(ctx, func(r ports.Repos) error { return r.Withdrawals().Insert(ctx, x) }); err != nil {
			t.Fatal(err)
		}
		more = append(more, x.ID)
	}
	page, err := read.Withdrawals().Page(ctx, net, ports.WithdrawalFilter{Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != more[1] || page[1].ID != more[0] {
		t.Fatalf("newest first %v %v", page, err)
	}
	if rest, _ := read.Withdrawals().Page(ctx, net, ports.WithdrawalFilter{After: page[1].ID, Limit: 2}); len(rest) != 1 || rest[0].ID != w.ID {
		t.Fatalf("second page %v", rest)
	}
	if oldest, _ := read.Withdrawals().Page(ctx, net, ports.WithdrawalFilter{UserID: other, Oldest: true, Limit: 5}); len(oldest) != 2 ||
		oldest[0].ID != more[0] {
		t.Fatalf("oldest of a user %v", oldest)
	}
	if one, _ := read.Withdrawals().Page(ctx, net, ports.WithdrawalFilter{Status: domain.WithdrawalBroadcast, Limit: 5}); len(one) != 1 {
		t.Fatalf("by status %v", one)
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

func TestCustodyStorage(t *testing.T) {
	store, ctx := newStore(t), context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	read := store.Read()
	alice, bob := uuid.NewString(), uuid.NewString()
	const tron = "TRON"
	for user, addr := range map[string]string{alice: "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7", bob: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"} {
		err := store.Tx(ctx, func(r ports.Repos) error {
			if err := r.Addresses().Lock(ctx, user, tron); err != nil {
				return err
			}
			return r.Addresses().Insert(ctx, domain.Address{UserID: user, Network: tron, Provider: domain.ProviderUdun, Address: addr, CreatedAt: now})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if a, err := read.Addresses().Get(ctx, alice, tron); err != nil || a == nil || a.Provider != domain.ProviderUdun || a.Index != 0 {
		t.Fatalf("a custodian's address %+v %v", a, err)
	}
	if owner, err := read.Addresses().Owner(ctx, tron, "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7"); err != nil || owner != alice {
		t.Fatalf("owner %q %v", owner, err)
	}
	if n, err := read.Addresses().Count(ctx, []string{tron, net}); err != nil || n != 2 {
		t.Fatalf("count %d %v", n, err)
	}

	// One transaction paying two users: two deposits, keyed by the trade.
	for i, user := range []string{alice, bob} {
		d := domain.Deposit{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Asset: "USDT", Network: tron, Address: "T-" + user, TxHash: "batch1",
			LogIndex: domain.NativeLog, Amount: decimal.NewFromInt(25), RawAmount: decimal.NewFromInt(25_000_000), Confirmations: 20,
			Required: 20, Status: domain.StatusConfirmed, ProviderTxID: "UDUN:t" + strconv.Itoa(i), DetectedAt: now, ConfirmedAt: now,
		}
		if err := store.Tx(ctx, func(r ports.Repos) error { return r.Deposits().Insert(ctx, d) }); err != nil {
			t.Fatal(err)
		}
	}
	got, err := read.Deposits().ByProviderTx(ctx, "UDUN:t1")
	if err != nil || got == nil || got.UserID != bob || got.ProviderTxID != "UDUN:t1" || got.BlockNumber != 0 {
		t.Fatalf("by trade %+v %v", got, err)
	}
	dup := *got
	dup.ID = uuid.Must(uuid.NewV7()).String()
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Deposits().Insert(ctx, dup) }); err == nil {
		t.Fatal("a trade is recorded once")
	}
	if list, err := read.Deposits().Unrequested(ctx, tron); err != nil || len(list) != 2 {
		t.Fatalf("unrequested %v %v", list, err)
	}

	w := domain.Withdrawal{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: alice, Asset: "USDT", Network: tron, Address: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
		Amount: decimal.NewFromInt(100), Fee: decimal.NewFromInt(1), Provider: domain.ProviderUdun, Status: domain.WithdrawalApproved,
		Nonce: -1, Required: 20, FreezeJournal: uuid.NewString(), CreatedAt: now, UpdatedAt: now, ApprovedAt: now,
	}
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Withdrawals().Insert(ctx, w) }); err != nil {
		t.Fatal(err)
	}
	w.Submit(now)
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Withdrawals().Update(ctx, w) }); err != nil {
		t.Fatal(err)
	}
	if list, err := read.Withdrawals().Submitted(ctx, domain.ProviderUdun); err != nil || len(list) != 1 || list[0].ProviderStatus != domain.CustodySubmitted ||
		!list[0].SubmittedAt.Equal(now) {
		t.Fatalf("submitted %+v %v", list, err)
	}
	w.Custodian(domain.CustodySuccess, "f00d", now)
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Withdrawals().Update(ctx, w) }); err != nil {
		t.Fatalf("a custodian's confirmed withdrawal has no nonce: %v", err)
	}
	if page, err := read.Withdrawals().Page(ctx, "", ports.WithdrawalFilter{Limit: 5}); err != nil || len(page) != 1 || page[0].TxHash != "f00d" {
		t.Fatalf("every network's page %+v %v", page, err)
	}
	if list, err := read.Withdrawals().Unsettled(ctx, tron); err != nil || len(list) != 1 {
		t.Fatalf("to settle %v %v", list, err)
	}

	amount := decimal.NewFromInt(100)
	cb := domain.Callback{
		ID: uuid.Must(uuid.NewV7()).String(), Provider: domain.ProviderUdun, TradeID: "w-1", Kind: domain.CallbackWithdrawal, Status: 3,
		BusinessID: w.ID, Coin: "195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Amount: &amount, TxHash: "f00d", Raw: "timestamp=1&body=x",
		SignatureOK: true, Result: domain.CallbackReceived, ReceivedAt: now,
	}
	var stored domain.Callback
	var fresh bool
	receive := func(c domain.Callback) {
		t.Helper()
		if err := store.Tx(ctx, func(r ports.Repos) error {
			var err error
			stored, fresh, err = r.Callbacks().Receive(ctx, c)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	receive(cb)
	if !fresh || stored.Attempts != 1 {
		t.Fatalf("first %+v %v", stored, fresh)
	}
	if err := read.Callbacks().Finish(ctx, cb.ID, domain.CallbackApplied, "sent", now); err != nil {
		t.Fatal(err)
	}
	retry := cb
	retry.ID = uuid.Must(uuid.NewV7()).String()
	receive(retry)
	if fresh || stored.ID != cb.ID || stored.Attempts != 2 || stored.Result != domain.CallbackApplied || !stored.Amount.Equal(amount) {
		t.Fatalf("the custodian's retry %+v %v", stored, fresh)
	}
	for range 2 {
		forged := domain.Callback{
			ID: uuid.Must(uuid.NewV7()).String(), Provider: domain.ProviderUdun, TradeID: "w-1", Status: 3, Raw: "x",
			Result: domain.CallbackRejected, Detail: "signature", ReceivedAt: now,
		}
		receive(forged)
		if !fresh {
			t.Fatal("a refused callback is logged every time")
		}
	}
	if page, err := read.Callbacks().Page(ctx, ports.CallbackFilter{Query: "f00d", Limit: 10}); err != nil || len(page) != 1 || page[0].ID != cb.ID {
		t.Fatalf("by transaction %+v %v", page, err)
	}
	if page, err := read.Callbacks().Page(ctx, ports.CallbackFilter{Result: domain.CallbackRejected, Limit: 1}); err != nil || len(page) != 1 ||
		page[0].Status != 3 || page[0].SignatureOK {
		t.Fatalf("refused, newest first %+v %v", page, err)
	}
	if n, last, err := read.Callbacks().Attention(ctx); err != nil || n != 0 || !last.Equal(now) {
		t.Fatalf("attention %d %v %v", n, last, err)
	}

	for _, c := range []domain.ChainCheck{
		domain.NewChainCheck(domain.ProviderUdun, "ETH", decimal.NewFromInt(3), decimal.NewFromInt(4), decimal.Zero, 2, now).
			Beside(decimal.NewFromInt(1), decimal.Zero),
		domain.NewChainCheck(net, "ETH", decimal.NewFromInt(1), decimal.NewFromInt(4), decimal.Zero, 3, now).
			Beside(decimal.NewFromInt(3), decimal.Zero),
	} {
		if err := read.Checks().Insert(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	checks, err := read.Checks().Latest(ctx, "")
	if err != nil || len(checks) != 2 || !checks[0].Elsewhere.Equal(decimal.NewFromInt(3)) && !checks[1].Elsewhere.Equal(decimal.NewFromInt(3)) {
		t.Fatalf("every holder's checks %+v %v", checks, err)
	}
	for _, c := range checks {
		if !c.Shortfall.IsZero() {
			t.Fatalf("both holders together hold what is expected: %+v", c)
		}
	}
}
