package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/wallet/domain"
	"github.com/skill/exchange/internal/wallet/ports"
)

// TestAdminStorage stores what the admin console decides: backfilled
// deposits, their late callbacks and decisions, and holds of withdrawals.
func TestAdminStorage(t *testing.T) {
	store, ctx := newStore(t), context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	read := store.Read()
	alice := uuid.NewString()
	const tron = "TRON"
	insert := func(d domain.Deposit) {
		t.Helper()
		if err := store.Tx(ctx, func(r ports.Repos) error { return r.Deposits().Insert(ctx, d) }); err != nil {
			t.Fatal(err)
		}
	}
	update := func(d domain.Deposit) error {
		return store.Tx(ctx, func(r ports.Repos) error { return r.Deposits().Update(ctx, d) })
	}
	manual := domain.Deposit{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: alice, Asset: "USDT", Network: tron, Address: "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7",
		TxHash: "tx-manual", LogIndex: domain.NativeLog, Amount: decimal.NewFromInt(40), RawAmount: decimal.NewFromInt(40_000_000),
		Confirmations: 20, Required: 20, Status: domain.StatusConfirmed, ProviderTxID: "UDUN:m1", DetectedAt: now, ConfirmedAt: now,
		Source: domain.SourceManual, EnteredBy: "ops@example.com",
	}
	insert(manual)
	unclaimed := domain.Deposit{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: alice, Asset: "USDT", Network: tron, Address: manual.Address, TxHash: "tx-small",
		LogIndex: domain.NativeLog, Amount: decimal.RequireFromString("0.5"), RawAmount: decimal.NewFromInt(500_000), Confirmations: 20,
		Required: 20, Status: domain.StatusRejected, Unclaimed: true, Reason: domain.ReasonBelowMinimum, JournalID: uuid.NewString(),
		ProviderTxID: "UDUN:u1", DetectedAt: now.Add(time.Second), ConfirmedAt: now, CreditedAt: now,
	}
	insert(unclaimed)

	got, err := read.Deposits().ByTransfer(ctx, tron, "TX-MANUAL", "tla2f6vpqdgre67v1736s7bj8ray5wyju7")
	if err != nil || got == nil || got.ID != manual.ID || got.Source != domain.SourceManual || got.EnteredBy != "ops@example.com" {
		t.Fatalf("by transfer %+v %v", got, err)
	}
	if page, err := read.Deposits().Page(ctx, ports.DepositFilter{ManualPending: true, Limit: 10}); err != nil || len(page) != 1 || page[0].ID != manual.ID {
		t.Fatalf("pending backfills %+v %v", page, err)
	}
	if page, err := read.Deposits().Page(ctx, ports.DepositFilter{Attention: true, Limit: 10}); err != nil || len(page) != 1 || page[0].ID != unclaimed.ID {
		t.Fatalf("attention %+v %v", page, err)
	}

	// The callback disagrees: a discrepancy, then dismissed.
	if manual.MatchCallback(manual.ProviderTxID, manual.Address, "USDT", decimal.NewFromInt(41), now) {
		t.Fatal("41 is not 40")
	}
	if err := update(manual); err != nil {
		t.Fatal(err)
	}
	if page, _ := read.Deposits().Page(ctx, ports.DepositFilter{Attention: true, Limit: 10}); len(page) != 2 {
		t.Fatalf("the discrepancy needs attention: %+v", page)
	}
	if err := manual.Dismiss("ops@example.com", "adjusted by hand", now); err != nil {
		t.Fatal(err)
	}
	if err := update(manual); err != nil {
		t.Fatal(err)
	}

	// The unclaimed deposit is released: CREDITED though unclaimed.
	if err := unclaimed.Release(uuid.NewString(), "fin@example.com", "minimum waived", now); err != nil {
		t.Fatal(err)
	}
	if err := update(unclaimed); err != nil {
		t.Fatalf("a released unclaimed deposit is CREDITED: %v", err)
	}
	back, err := read.Deposits().Get(ctx, unclaimed.ID)
	if err != nil || back == nil || back.Status != domain.StatusCredited || back.Resolution != domain.ResolutionCredited ||
		back.ReleaseJournalID == "" || back.ResolvedBy != "fin@example.com" || !back.ResolvedAt.Equal(now) {
		t.Fatalf("released %+v %v", back, err)
	}
	if page, _ := read.Deposits().Page(ctx, ports.DepositFilter{Attention: true, Limit: 10}); len(page) != 0 {
		t.Fatalf("nothing waits: %+v", page)
	}
	if page, _ := read.Deposits().Page(ctx, ports.DepositFilter{UserID: alice, Limit: 1}); len(page) != 1 || page[0].ID != unclaimed.ID {
		t.Fatalf("newest first, a page of one: %+v", page)
	}
	// L2: only some users' (none for an empty list), or past them; the two
	// split the list.
	all, _ := read.Deposits().Page(ctx, ports.DepositFilter{Limit: 50})
	hers, _ := read.Deposits().Page(ctx, ports.DepositFilter{Users: ports.UserIDs{Only: []string{alice}}, Limit: 50})
	rest, _ := read.Deposits().Page(ctx, ports.DepositFilter{Users: ports.UserIDs{Exclude: []string{alice}}, Limit: 50})
	if len(hers) == 0 || len(hers)+len(rest) != len(all) {
		t.Fatalf("hers %d, the rest %d, all %d", len(hers), len(rest), len(all))
	}
	for _, d := range hers {
		if d.UserID != alice {
			t.Fatalf("only %s's: %+v", alice, d)
		}
	}
	for _, d := range rest {
		if d.UserID == alice {
			t.Fatalf("past %s: %+v", alice, d)
		}
	}
	if none, err := read.Deposits().Page(ctx, ports.DepositFilter{Users: ports.UserIDs{Only: []string{}}, Limit: 50}); err != nil || len(none) != 0 {
		t.Fatalf("only nobody's %+v %v", none, err)
	}
	// A CREDITED unclaimed deposit without a release is refused.
	bad := *back
	bad.Resolution, bad.ReleaseJournalID = "", ""
	if err := update(bad); err == nil {
		t.Fatal("CREDITED and unclaimed needs a release")
	}

	w := domain.Withdrawal{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: alice, Asset: "USDT", Network: tron, Address: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
		Amount: decimal.NewFromInt(100), Fee: decimal.NewFromInt(1), Provider: domain.ProviderUdun, Status: domain.WithdrawalReview,
		RiskScore: 70, ValueUSDT: decimal.NewFromInt(100), Nonce: -1, Required: 20, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Withdrawals().Insert(ctx, w) }); err != nil {
		t.Fatal(err)
	}
	if err := w.Hold("ops@example.com", "calling the user", now); err != nil {
		t.Fatal(err)
	}
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Withdrawals().Update(ctx, w) }); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		f    ports.WithdrawalFilter
		want int
	}{
		{ports.WithdrawalFilter{Held: "true"}, 1},
		{ports.WithdrawalFilter{Held: "false"}, 0},
		{ports.WithdrawalFilter{MinValue: decimal.NewFromInt(101)}, 0},
		{ports.WithdrawalFilter{MinValue: decimal.NewFromInt(50), MaxValue: decimal.NewFromInt(100)}, 1},
		{ports.WithdrawalFilter{MinRisk: 71}, 0},
		{ports.WithdrawalFilter{MinRisk: 70}, 1},
	} {
		c.f.Limit = 10
		page, err := read.Withdrawals().Page(ctx, "", c.f)
		if err != nil || len(page) != c.want {
			t.Fatalf("%+v: %d %v", c.f, len(page), err)
		}
		if c.want == 1 && (page[0].HoldNote != "calling the user" || !page[0].HeldAt.Equal(now)) {
			t.Fatalf("held %+v", page[0])
		}
	}
	// Out of review (a hold left over), it is no longer listed as held (C5.5 ⑦).
	if err := w.Reject("REVIEW: odd address", now); err != nil {
		t.Fatal(err)
	}
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Withdrawals().Update(ctx, w) }); err != nil {
		t.Fatal(err)
	}
	if page, err := read.Withdrawals().Page(ctx, "", ports.WithdrawalFilter{Held: "true", Limit: 10}); err != nil || len(page) != 0 {
		t.Fatalf("a decided withdrawal listed as held: %d %v", len(page), err)
	}
}
