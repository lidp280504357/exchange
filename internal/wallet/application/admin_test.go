package application

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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
	// The same backfill twice at once: the one that loses the race to the
	// unique index finds the winner's deposit (C5.5 ⑦).
	raced := ManualDeposit{Network: tron, TradeID: "m5", Address: addr.Address, TxHash: "tx-m5", Amount: d("3"), Actor: "ops@example.com"}
	var winner domain.Deposit
	h.store.race = func(x domain.Deposit) error {
		winner = x
		winner.ID = uuid.Must(uuid.NewV7()).String()
		h.store.deposits[winner.ID] = winner
		return &pgconn.PgError{Code: "23505", ConstraintName: "deposits_provider_tx_id_key"}
	}
	lost, err := h.svc.BookManualDeposit(ctx, raced, "callback lost, entered twice")
	if err != nil || lost.ID != winner.ID {
		t.Fatalf("the race's loser %+v %v (winner %s)", lost, err, winner.ID)
	}
	delete(h.store.deposits, winner.ID)
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
	// The custodian's retry keeps the mark, and the processor does not
	// credit the backfill: a person decides (C5.5 ⑦).
	resent := h.callback(t, ports.CustodyTrade{
		TradeID: "m3", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("100"), RawAmount: d("100000000"), TxHash: "tx-m3",
	})
	if resent.Result != domain.CallbackDiscrepancy {
		t.Fatalf("the callback again %+v", resent)
	}
	h.store.take()
	h.cround(t)
	if got := h.store.deposits[d3.ID]; !got.CreditRequested.IsZero() || slices.Contains(types(h.store.take()), "DepositConfirmed") {
		t.Fatalf("a disagreed backfill sent to the ledger %+v", got)
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

	// The ledger released one but its record here failed: it is not
	// closed, and crediting it again (in other words) records the release
	// without booking it twice (C5.5 ⑦).
	h.callback(t, ports.CustodyTrade{
		TradeID: "u3", Kind: domain.CallbackDeposit, Status: 3, Word: domain.CustodySuccess, Coin: usdtCoin, Address: addr.Address,
		Amount: d("0.4"), RawAmount: d("400000"), TxHash: "tx-u3",
	})
	var lost domain.Deposit
	for _, x := range h.store.deposits {
		if x.TxHash == "tx-u3" {
			lost = x
		}
	}
	h.cround(t)
	if err := h.svc.OnCredited(ctx, lost.ID, "j-lost"); err != nil {
		t.Fatal(err)
	}
	released, err := h.ledger.ReleaseUnclaimed(ctx, lost.ID, "alice", "USDT", d("0.4"), "ops@example.com", "the first attempt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DismissDeposit(ctx, lost.ID, "fin@example.com", "stays unclaimed"); !apperr.Is(err, "WALLET_DEPOSIT_RELEASED") {
		t.Fatalf("closed after the ledger released it: %v", err)
	}
	after := h.ledger.available["alice"]
	recorded, err := h.svc.CreditDeposit(ctx, lost.ID, "fin@example.com", "recording the release")
	if err != nil || recorded.ReleaseJournalID != released || !h.ledger.available["alice"].Equal(after) {
		t.Fatalf("the release recorded %+v %v", recorded, err)
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
	// Released before the doubt came, the release unrecorded: neither
	// closed nor released again, but recorded (C5.5 ⑮).
	if _, err := h.ledger.ReleaseUnclaimed(ctx, b.ID, "alice", "USDT", d("0.25"), "ops@example.com", "the first attempt"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DismissDeposit(ctx, b.ID, "fin@example.com", "stays unclaimed"); !apperr.Is(err, "WALLET_DEPOSIT_RELEASED") {
		t.Fatalf("an in-doubt deposit the ledger released, closed: %v", err)
	}
	moved := h.ledger.available["alice"]
	inDoubt, err := h.svc.CreditDeposit(ctx, b.ID, "fin@example.com", "recording the release")
	if err != nil || inDoubt.Status != domain.StatusCredited || inDoubt.ReleaseJournalID == "" || !h.ledger.available["alice"].Equal(moved) {
		t.Fatalf("its release recorded %+v %v", inDoubt, err)
	}
	if n := h.audited("wallet.deposit.release_recorded"); n != 1 {
		t.Fatalf("the recorded release audited %d times", n)
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
	// The console asks for two reviewers (C5.5 ⑮): one approval leaves it in review; more than
	// two asked by mistake are two (⑰).
	other := h.reviewable(t)
	first, err := ReviewWithdrawal(ctx, h.store, Review{ID: other.ID, Reviewer: "fin@example.com", Reason: "fine", Approve: true, AtLeast: 1000}, h.now)
	if err != nil || first.Status != domain.WithdrawalReview || first.ApprovalsRequired != 2 {
		t.Fatalf("raised to two reviewers %+v %v", first, err)
	}
	second, err := ReviewWithdrawal(ctx, h.store, Review{ID: other.ID, Reviewer: "boss@example.com", Reason: "fine too", Approve: true}, h.now)
	if err != nil || second.Status != domain.WithdrawalApproved {
		t.Fatalf("the second reviewer %+v %v", second, err)
	}
	// A review ends a hold (C5.5 ⑦).
	if _, err := h.svc.HoldWithdrawal(ctx, w.ID, true, "ops@example.com", "calling the user again"); err != nil {
		t.Fatal(err)
	}
	rejected, err := ReviewWithdrawal(ctx, h.store, Review{ID: w.ID, Reviewer: "ops@example.com", Reason: "fine"}, h.now)
	if err != nil || !rejected.HeldAt.IsZero() || rejected.HoldNote != "" {
		t.Fatalf("reviewed while held %+v %v", rejected, err)
	}
	if _, err := h.svc.HoldWithdrawal(ctx, w.ID, true, "ops@example.com", "too late"); !apperr.Is(err, "WALLET_WITHDRAWAL_NOT_IN_REVIEW") {
		t.Fatalf("a rejected one: %v", err)
	}
}
