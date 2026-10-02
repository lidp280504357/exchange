package application

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

func (h *custodyHarness) audited(action string) int {
	n := 0
	for _, m := range h.store.audits {
		if a, ok := m.(*auditv1.AdminActionPerformed); ok && a.GetAction() == action {
			n++
		}
	}
	return n
}

// reviewable is a withdrawal of alice's waiting for review.
func (h *custodyHarness) reviewable(t *testing.T) domain.Withdrawal {
	t.Helper()
	w := h.requestCustody(t, "100")
	w.Status, w.ApprovalsRequired = domain.WithdrawalReview, 1
	h.store.wds[w.ID] = w
	return w
}

func TestManualDepositsAndTheirLateCallbacks(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	discrepancies := &countingCounter{Counter: prometheus.NewCounter(prometheus.CounterOpts{Name: "d"})}
	h.svc.Discrepancies = discrepancies
	addr, _, err := h.svc.DepositAddress(ctx, "alice", "USDT", tron)
	if err != nil {
		t.Fatal(err)
	}
	h.store.take()
	in := ManualDeposit{Network: "tron", TradeID: "m1", Address: strings.ToLower(addr.Address), TxHash: "tx-m1", Amount: d("40"), Actor: "ops@example.com"}

	for _, bad := range []ManualDeposit{
		{Network: "ETH", TradeID: "m1", Address: addr.Address, TxHash: "x", Amount: d("1"), Actor: "a"},        // not a custodian's
		{Network: tron, TradeID: "m1", Address: "TNobody", TxHash: "x", Amount: d("1"), Actor: "a"},            // nobody's address
		{Network: tron, TradeID: "m1", Address: addr.Address, TxHash: "x", Amount: d("1.0000001"), Actor: "a"}, // decimals
		{Network: tron, TradeID: "", Address: addr.Address, TxHash: "x", Amount: d("1"), Actor: "a"},
	} {
		if _, err := h.svc.CheckManualDeposit(ctx, bad); !apperr.Is(err, apperr.CodeInvalidArgument) && !apperr.Is(err, "WALLET_AMOUNT_PRECISION") {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	checked, err := h.svc.CheckManualDeposit(ctx, in)
	if err != nil || checked.UserID != "alice" || checked.ProviderTxID != "UDUN:m1" || checked.Address != addr.Address || len(h.store.deposits) != 0 {
		t.Fatalf("check %+v %v", checked, err)
	}
	if _, err := h.svc.BookManualDeposit(ctx, in, ""); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no reason: %v", err)
	}
	d1, err := h.svc.BookManualDeposit(ctx, in, "callback lost, seen in the custodian's console")
	if err != nil || d1.Status != domain.StatusConfirmed || d1.Source != domain.SourceManual || d1.EnteredBy != "ops@example.com" ||
		!d1.RawAmount.Equal(d("40000000")) {
		t.Fatalf("backfill %+v %v", d1, err)
	}
	again, err := h.svc.BookManualDeposit(ctx, in, "the same again")
	if err != nil || again.ID != d1.ID || len(h.store.deposits) != 1 {
		t.Fatalf("repeat %+v %v", again, err)
	}
	other := in
	other.TradeID = "m2" // the same transfer under another trade
	if _, err := h.svc.BookManualDeposit(ctx, other, "again"); !apperr.Is(err, "WALLET_DEPOSIT_KNOWN") {
		t.Fatalf("a known transfer: %v", err)
	}
	if h.audited("wallet.deposit.backfilled") != 1 {
		t.Fatal("the backfill is audited once")
	}
	if list, _, _ := h.svc.AdminDeposits(ctx, ports.DepositFilter{ManualPending: true}); len(list) != 1 || list[0].ID != d1.ID {
		t.Fatalf("pending backfills %+v", list)
	}
	h.cround(t)
	if got := types(h.store.take()); !slices.Equal(got, []string{"DepositDetected", "DepositConfirmed"}) {
		t.Fatalf("events %v", got)
	}

	// The custodian's own callback matches: applied, nothing booked again.
	cb := h.callback(t, ports.CustodyTrade{
		TradeID: "m1", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("40"), RawAmount: d("40000000"), TxHash: "tx-m1",
	})
	if cb.Result != domain.CallbackApplied || len(h.store.deposits) != 1 || h.store.deposits[d1.ID].CallbackAt.IsZero() {
		t.Fatalf("matching callback %+v", cb)
	}
	if list, _, _ := h.svc.AdminDeposits(ctx, ports.DepositFilter{ManualPending: true}); len(list) != 0 {
		t.Fatalf("no backfill waits: %+v", list)
	}

	// Another backfill whose callback disagrees: a discrepancy, counted.
	in2 := ManualDeposit{Network: tron, TradeID: "m3", Address: addr.Address, TxHash: "tx-m3", Amount: d("10"), Actor: "ops@example.com"}
	d3, err := h.svc.BookManualDeposit(ctx, in2, "callback lost")
	if err != nil {
		t.Fatal(err)
	}
	cb = h.callback(t, ports.CustodyTrade{
		TradeID: "m3", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("100"), RawAmount: d("100000000"), TxHash: "tx-m3",
	})
	got := h.store.deposits[d3.ID]
	if cb.Result != domain.CallbackDiscrepancy || !strings.Contains(got.Discrepancy, "amount 100, entered 10") || !got.Attention() ||
		len(h.store.deposits) != 2 || discrepancies.n != 1 {
		t.Fatalf("disagreeing callback %+v %+v", cb, got)
	}
	if list, _, _ := h.svc.AdminDeposits(ctx, ports.DepositFilter{Attention: true}); len(list) != 1 || list[0].ID != d3.ID {
		t.Fatalf("attention %+v", list)
	}
	if _, err := h.svc.CreditDeposit(ctx, d3.ID, "ops@example.com", "not unclaimed"); !apperr.Is(err, "WALLET_DEPOSIT_NOT_RELEASABLE") {
		t.Fatalf("a discrepancy is not credited: %v", err)
	}
	dismissed, err := h.svc.DismissDeposit(ctx, d3.ID, "ops@example.com", "corrected by an adjustment")
	if err != nil || dismissed.Resolution != domain.ResolutionDismissed || dismissed.Attention() {
		t.Fatalf("dismiss %+v %v", dismissed, err)
	}
	if _, err := h.svc.DismissDeposit(ctx, d3.ID, "ops@example.com", "again"); !apperr.Is(err, "WALLET_DEPOSIT_RESOLVED") {
		t.Fatalf("dismissed twice: %v", err)
	}

	// A backfill entered with a wrong trade ID: the custodian's callback
	// under the real one finds it by the transfer, books nothing again and
	// leaves the trade for a person.
	in4 := ManualDeposit{Network: tron, TradeID: "typo4", Address: addr.Address, TxHash: "tx-m4", Amount: d("5"), Actor: "ops@example.com"}
	d4, err := h.svc.BookManualDeposit(ctx, in4, "callback lost")
	if err != nil {
		t.Fatal(err)
	}
	cb = h.callback(t, ports.CustodyTrade{
		TradeID: "m4", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: strings.ToUpper(addr.Address),
		Amount: d("5"), RawAmount: d("5000000"), TxHash: "TX-M4",
	})
	got = h.store.deposits[d4.ID]
	if cb.Result != domain.CallbackDiscrepancy || !strings.Contains(got.Discrepancy, "trade UDUN:m4, entered UDUN:typo4") || len(h.store.deposits) != 3 {
		t.Fatalf("a backfill under another trade %+v %+v", cb, got)
	}
	// Once matched, the same transfer under yet another trade is not booked.
	cb = h.callback(t, ports.CustodyTrade{
		TradeID: "m4-again", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("5"), RawAmount: d("5000000"), TxHash: "tx-m4",
	})
	if cb.Result != domain.CallbackUnmatched || !strings.Contains(cb.Detail, "the transfer is deposit "+d4.ID) || len(h.store.deposits) != 3 {
		t.Fatalf("the same transfer again %+v", cb)
	}
}

func TestUnclaimedDepositsAreCreditedByHand(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	addr, _, err := h.svc.DepositAddress(ctx, "alice", "USDT", tron)
	if err != nil {
		t.Fatal(err)
	}
	h.callback(t, ports.CustodyTrade{
		TradeID: "u1", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("0.5"), RawAmount: d("500000"), TxHash: "tx-u1",
	})
	var dep domain.Deposit
	for _, x := range h.store.deposits {
		dep = x
	}
	if _, err := h.svc.CreditDeposit(ctx, dep.ID, "ops@example.com", "not booked yet"); !apperr.Is(err, "WALLET_DEPOSIT_NOT_RELEASABLE") {
		t.Fatalf("before the ledger booked it: %v", err)
	}
	h.cround(t) // DepositConfirmed to the ledger (unclaimed: below the minimum)
	if err := h.svc.OnCredited(ctx, dep.ID, "j-unclaimed"); err != nil {
		t.Fatal(err)
	}
	if got := h.store.deposits[dep.ID]; got.Status != domain.StatusRejected || !got.Attention() {
		t.Fatalf("unclaimed %+v", got)
	}
	before := h.ledger.available["alice"]
	credited, err := h.svc.CreditDeposit(ctx, dep.ID, "ops@example.com", "the user asked; minimum waived")
	if err != nil || credited.Status != domain.StatusCredited || credited.Resolution != domain.ResolutionCredited ||
		credited.ReleaseJournalID == "" || !h.ledger.available["alice"].Equal(before.Add(d("0.5"))) {
		t.Fatalf("credit %+v %v", credited, err)
	}
	if got := types(h.store.take()); !slices.Contains(got, "DepositCredited") {
		t.Fatalf("events %v", got)
	}
	if _, err := h.svc.CreditDeposit(ctx, dep.ID, "ops@example.com", "again"); !apperr.Is(err, "WALLET_DEPOSIT_NOT_RELEASABLE") {
		t.Fatalf("credited twice: %v", err)
	}
	if !h.ledger.available["alice"].Equal(before.Add(d("0.5"))) {
		t.Fatal("booked once")
	}

	// An unclaimed backfill whose callback disagrees is in doubt: the
	// ledger is never asked to release it.
	small := ManualDeposit{Network: tron, TradeID: "u2", Address: addr.Address, TxHash: "tx-u2", Amount: d("0.25"), Actor: "ops@example.com"}
	b, err := h.svc.BookManualDeposit(ctx, small, "callback lost")
	if err != nil {
		t.Fatal(err)
	}
	h.cround(t)
	if err := h.svc.OnCredited(ctx, b.ID, "j-small"); err != nil {
		t.Fatal(err)
	}
	h.callback(t, ports.CustodyTrade{
		TradeID: "u2", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("0.3"), RawAmount: d("300000"), TxHash: "tx-u2",
	})
	if got := h.store.deposits[b.ID]; got.Discrepancy == "" || !got.Unclaimed || got.JournalID == "" {
		t.Fatalf("an unclaimed backfill in doubt %+v", got)
	}
	held := h.ledger.available["alice"]
	if _, err := h.svc.CreditDeposit(ctx, b.ID, "ops@example.com", "credit it anyway"); !apperr.Is(err, "WALLET_DEPOSIT_NOT_RELEASABLE") {
		t.Fatalf("a backfill in doubt is not credited: %v", err)
	}
	if !h.ledger.available["alice"].Equal(held) {
		t.Fatal("the ledger released a deposit in doubt")
	}
}

func TestWithdrawalHoldAndDetail(t *testing.T) {
	h := newCustodyHarness(t)
	ctx := context.Background()
	w := h.reviewable(t)
	if _, err := h.svc.HoldWithdrawal(ctx, w.ID, true, "ops@example.com", " "); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no note: %v", err)
	}
	held, err := h.svc.HoldWithdrawal(ctx, w.ID, true, "ops@example.com", "calling the user")
	if err != nil || held.HeldAt.IsZero() || held.HoldNote != "calling the user" || held.Status != domain.WithdrawalReview {
		t.Fatalf("hold %+v %v", held, err)
	}
	if list, _ := h.store.Read().Withdrawals().Page(ctx, "", ports.WithdrawalFilter{Status: domain.WithdrawalReview, Held: "true", Limit: 10}); len(list) != 1 {
		t.Fatalf("held list %+v", list)
	}
	freed, err := h.svc.HoldWithdrawal(ctx, w.ID, false, "ops@example.com", "")
	if err != nil || !freed.HeldAt.IsZero() {
		t.Fatalf("unhold %+v %v", freed, err)
	}
	if h.audited("wallet.withdrawal.hold") != 1 || h.audited("wallet.withdrawal.unhold") != 1 {
		t.Fatal("hold and unhold are audited")
	}
	det, err := h.svc.Detail(ctx, w.ID)
	if err != nil || det.Address == nil || !det.UsedToday.IsPositive() || det.UsedMonth.LessThan(det.UsedToday) {
		t.Fatalf("detail %+v %v", det, err)
	}
	if _, err := ReviewWithdrawal(ctx, h.store, Review{ID: w.ID, Reviewer: "ops@example.com", Reason: "fine"}, h.now); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.HoldWithdrawal(ctx, w.ID, true, "ops@example.com", "too late"); !apperr.Is(err, "WALLET_WITHDRAWAL_NOT_IN_REVIEW") {
		t.Fatalf("a rejected one: %v", err)
	}
}
