package postgres_test

import (
	"context"
	"log/slog"
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
