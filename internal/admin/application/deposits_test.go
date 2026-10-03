package application

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func backfillOfTrade(trade, amount string) ports.ManualDeposit {
	return ports.ManualDeposit{
		Network: "tron", TradeID: trade, Address: "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7", TxHash: "tx-" + trade,
		Amount: decimal.RequireFromString(amount),
	}
}

func TestBackfillsGoThroughTheFundGuardrails(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	fin, boss, ops := h.login(t, "fin@example.com"), h.login(t, "boss@example.com"), h.login(t, "ops@example.com")

	if _, err := h.svc.Backfill(ctx, ops, "", backfillOfTrade("t1", "40"), "callback lost"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an operator backfills: %v", err)
	}
	if _, err := h.svc.Backfill(ctx, fin, "", ports.ManualDeposit{Network: "TRON", Amount: decimal.NewFromInt(1)}, "callback lost"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no trade: %v", err)
	}
	bad := backfillOfTrade("t0", "1")
	bad.Address = "nobody"
	if _, err := h.svc.Backfill(ctx, fin, "", bad, "callback lost"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("wallet-service's check: %v", err)
	}
	check, err := h.svc.CheckBackfill(ctx, fin, backfillOfTrade("t1", "40"))
	if err != nil || check.UserID != someUser || check.Asset != "USDT" || check.ValueUSDT == nil || check.ValueUSDT.String() != "40" {
		t.Fatalf("check %+v %v", check, err)
	}

	// Within the single-person limit: booked at once, entered by FINANCE.
	a, err := h.svc.Backfill(ctx, fin, "", backfillOfTrade("t1", "40"), "callback lost; seen in the console")
	if err != nil || a.Status != domain.ApprovalExecuted || a.Kind != domain.KindDepositBackfill || !strings.HasPrefix(a.Result, "deposit ") ||
		a.Payload["user_id"] != someUser || a.Payload["entered_by"] != "fin@example.com" || len(h.deposits.booked) != 1 {
		t.Fatalf("backfill %+v %v", a, err)
	}
	if len(h.ledger.calls) != 0 {
		t.Fatal("a backfill books nothing in the ledger directly")
	}

	// Above the limit: a second administrator approves; the deposit is
	// still entered by the one who typed it.
	big, err := h.svc.Backfill(ctx, fin, "", backfillOfTrade("t2", "200000"), "callback lost, large")
	if err != nil || big.Status != domain.ApprovalPending || big.Escalation != domain.EscalationSingleMax || len(h.deposits.booked) != 1 {
		t.Fatalf("large backfill %+v %v", big, err)
	}
	done, err := h.svc.DecideApproval(ctx, boss, big.ID, true, "checked the custodian's console")
	if err != nil || done.Status != domain.ApprovalExecuted || len(h.deposits.booked) != 2 || h.deposits.booked[1].Actor != "fin@example.com" {
		t.Fatalf("approved %+v %v %+v", done, err, h.deposits.booked)
	}
	got := h.auditsOf("admin.deposits.backfill_requested")
	if len(got) != 2 || !strings.Contains(got[0], `"custodian_checked":false`) || !strings.Contains(got[0], `"trade_id":"t1"`) {
		t.Fatalf("backfill audits %v", got)
	}
}

// TestADepositOfNobodyIsCreditedToAUser: a deposit to an address no user
// has (B7a) is credited to the user an administrator names, a fund
// operation like a backfill: alone within the limits, else decided by a
// second administrator; after a lost answer the same request finishes it
// without booking it twice (C5.5 ㉑).
func TestADepositOfNobodyIsCreditedToAUser(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	fin, boss, ops := h.login(t, "fin@example.com"), h.login(t, "boss@example.com"), h.login(t, "ops@example.com")
	small, large, lost := "0192a000-0000-7000-8000-0000000000e1", "0192a000-0000-7000-8000-0000000000e2", "0192a000-0000-7000-8000-0000000000e3"
	other, probe, spelled := "0192a000-0000-7000-8000-0000000000e4", "0192a000-0000-7000-8000-0000000000e5", "0192a000-0000-7000-8000-0000000000e6"
	owned := "0192a000-0000-7000-8000-0000000000d9"
	h.deposits.nobody = map[string]string{small: "50", large: "200000", lost: "7", other: "5", probe: "6", spelled: "3"}
	h.deposits.probes = map[string]bool{probe: true}
	stranger := "0192a000-0000-7000-8000-00000000cafe"

	if _, err := h.svc.AssignDeposit(ctx, ops, "", small, someUser, "found its owner"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an operator credits it: %v", err)
	}
	if _, err := h.svc.AssignDeposit(ctx, fin, "", small, domain.NoOwner, "found its owner"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("to nobody: %v", err)
	}
	if _, err := h.svc.AssignDeposit(ctx, fin, "", owned, someUser, "found its owner"); code(err) != "ADMIN_DEPOSIT_NOT_UNOWNED" {
		t.Fatalf("a deposit with its user: %v", err)
	}

	// To its address's former holder within the single-person limit:
	// credited at once, with the journal.
	a, err := h.svc.AssignDeposit(ctx, fin, "k1", small, someUser, "the owner's ticket T-9")
	if err != nil || a.Status != domain.ApprovalExecuted || a.Kind != domain.KindDepositAssign || a.JournalID != "release-"+small ||
		a.Payload["user_id"] != someUser || a.Payload["deposit_id"] != small || a.Payload["former_holder"] != someUser ||
		h.deposits.assigned[small] != someUser || a.ValueUSDT == nil || a.ValueUSDT.String() != "50" {
		t.Fatalf("assigned %+v %v", a, err)
	}
	if again, err := h.svc.AssignDeposit(ctx, fin, "k1", small, someUser, "the owner's ticket T-9"); err != nil || again.ID != a.ID ||
		len(h.deposits.decided) != 1 {
		t.Fatalf("the same request again %+v %v %v", again, err, h.deposits.decided)
	}
	if len(h.ledger.calls) != 0 {
		t.Fatal("wallet-service releases it, the console books nothing")
	}

	// Above the limit: a second administrator decides.
	b, err := h.svc.AssignDeposit(ctx, fin, "k2", large, someUser, "large; the owner proved the transfer")
	if err != nil || b.Status != domain.ApprovalPending || b.Escalation != domain.EscalationSingleMax || h.deposits.assigned[large] != "" {
		t.Fatalf("large %+v %v", b, err)
	}
	// One live request per deposit (review ㉕): another waits for this one,
	// however its ID is spelled (review ㉖).
	for i, spelling := range []string{large, strings.ToUpper(large), "{" + large + "}", "urn:uuid:" + large, strings.ReplaceAll(large, "-", "")} {
		if _, err := h.svc.AssignDeposit(ctx, boss, fmt.Sprintf("k2b-%d", i), spelling, someUser, "the same deposit again"); code(err) != "ADMIN_DEPOSIT_ASSIGN_OPEN" {
			t.Fatalf("a second request (%s) while one waits: %v", spelling, err)
		}
	}
	if done, err := h.svc.DecideApproval(ctx, boss, b.ID, true, "checked the transfer"); err != nil || done.Status != domain.ApprovalExecuted ||
		h.deposits.assigned[large] != someUser || done.JournalID != "release-"+large {
		t.Fatalf("approved %+v %v", done, err)
	}

	// Credited, but the answer lost: the operation waits, attempted; the
	// same request finds the deposit credited to this user and finishes.
	h.deposits.loseAnswer = true
	_, err = h.svc.AssignDeposit(ctx, fin, "k3", lost, someUser, "the owner's ticket T-11")
	if apperr.From(err).Details["approval_id"] == nil {
		t.Fatalf("an unknown outcome names its operation: %v", err)
	}
	c, err := h.svc.AssignDeposit(ctx, fin, "k3", lost, someUser, "the owner's ticket T-11")
	if err != nil || c.Status != domain.ApprovalExecuted || c.JournalID != "release-"+lost || len(h.deposits.decided) != 3 {
		t.Fatalf("finished %+v %v %v", c, err, h.deposits.decided)
	}
	// To a user other than its address's former holder: a second
	// administrator decides, however small (the coordinator's 10-04
	// decision); the trail names both.
	first, err := h.svc.AssignDeposit(ctx, fin, "k4", other, stranger, "the sender's ticket T-12")
	if err != nil || first.Status != domain.ApprovalPending || first.Escalation != domain.EscalationNotHolder || h.deposits.assigned[other] != "" ||
		first.Payload["former_holder"] != someUser || first.Payload["user_id"] != stranger {
		t.Fatalf("to another user %+v %v", first, err)
	}
	// Rejected, it leaves the deposit to a new request.
	if no, err := h.svc.DecideApproval(ctx, boss, first.ID, false, "ask the sender for the transfer"); err != nil || no.Status != domain.ApprovalRejected {
		t.Fatalf("rejected %+v %v", no, err)
	}
	d, err := h.svc.AssignDeposit(ctx, fin, "k4b", other, stranger, "the sender's ticket T-12, with the transfer")
	if err != nil || d.Status != domain.ApprovalPending || d.Escalation != domain.EscalationNotHolder {
		t.Fatalf("asked again %+v %v", d, err)
	}
	if _, err := h.svc.DecideApproval(ctx, fin, d.ID, true, "mine"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("its requester approves it: %v", err)
	}
	if done, err := h.svc.DecideApproval(ctx, boss, d.ID, true, "the sender proved the transfer"); err != nil ||
		done.Status != domain.ApprovalExecuted || h.deposits.assigned[other] != stranger {
		t.Fatalf("approved %+v %v", done, err)
	}
	// An address no user ever had: the usual limits.
	if e, err := h.svc.AssignDeposit(ctx, fin, "k5", probe, stranger, "the sender's ticket T-13"); err != nil ||
		e.Status != domain.ApprovalExecuted || e.Payload["former_holder"] != "" || e.Payload["address_owner"] != "" {
		t.Fatalf("a probe address's %+v %v", e, err)
	}
	// The former holder spelled otherwise is still the holder: the usual
	// limits, the IDs recorded in their canonical form (review ㉖).
	f, err := h.svc.AssignDeposit(ctx, fin, "k6", strings.ToUpper(spelled), "{"+strings.ToUpper(someUser)+"}", "the owner's ticket T-14")
	if err != nil || f.Status != domain.ApprovalExecuted || f.Escalation != "" || f.Payload["user_id"] != someUser || f.Payload["deposit_id"] != spelled ||
		h.deposits.assigned[spelled] != someUser {
		t.Fatalf("spelled otherwise %+v %v", f, err)
	}

	got := h.auditsOf("admin.deposits.assign_requested")
	if len(got) != 7 || !strings.Contains(got[0], `"deposit_id":"`+small+`"`) || !strings.Contains(got[0], `"former_holder":"`+someUser+`"`) {
		t.Fatalf("the requests audited %v", got)
	}
	if !strings.Contains(got[3], `"user_id":"`+stranger+`","former_holder":"`+someUser+`"`) ||
		!strings.Contains(got[3], `"escalation":"NOT_ADDRESS_HOLDER"`) {
		t.Fatalf("a request for another user audited %s", got[3])
	}
	// The decisions name the deposit, the user and the journal (review ㉕).
	approved := h.auditsOf("admin.deposits.assign_approved")
	if len(approved) != 2 || !strings.Contains(approved[1], `"deposit_id":"`+other+`"`) || !strings.Contains(approved[1], `"user_id":"`+stranger+`"`) ||
		!strings.Contains(approved[1], `"journal_id":"release-`+other+`"`) {
		t.Fatalf("the approvals audited %v", approved)
	}
}

func TestDepositDecisionsAndWithdrawalHolds(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	fin, auditor := h.login(t, "fin@example.com"), h.login(t, "audit@example.com")
	dep := "0192a000-0000-7000-8000-0000000000d9"

	if _, err := h.svc.CreditDeposit(ctx, auditor, "", dep, "the user's"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an auditor credits: %v", err)
	}
	if _, err := h.svc.CreditDeposit(ctx, fin, "", dep, ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := h.svc.CreditDeposit(ctx, fin, "", dep, "below the minimum, waived"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DismissDeposit(ctx, fin, dep, "a wrong token, refunded off-platform"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.deposits.decided, []string{"credit " + dep + " by fin@example.com", "dismiss " + dep + " by fin@example.com"}) {
		t.Fatalf("decided %v", h.deposits.decided)
	}
	if _, err := h.svc.DepositsForReview(ctx, auditor, ports.DepositReviewQuery{Attention: true}); err != nil {
		t.Fatalf("an auditor reads the queue: %v", err)
	}

	h.deposits.attention = 2
	if todo, err := h.svc.Todo(ctx, fin); err != nil || todo.Deposits != 2 {
		t.Fatalf("todo %+v %v", todo, err)
	}
	if todo, _ := h.svc.Todo(ctx, auditor); todo.Deposits != 0 {
		t.Fatalf("an auditor decides no deposits: %+v", todo)
	}

	w := "0192a000-0000-7000-8000-0000000000e9"
	if _, err := h.svc.HoldWithdrawal(ctx, auditor, w, true, "calling the user"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an auditor holds: %v", err)
	}
	if _, err := h.svc.HoldWithdrawal(ctx, fin, w, true, ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a hold without a note: %v", err)
	}
	if _, err := h.svc.HoldWithdrawal(ctx, fin, w, true, "calling the user"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.HoldWithdrawal(ctx, fin, w, false, ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.wallet.reviewed, []string{"hold " + w + " true", "hold " + w + " false"}) || h.wallet.reviewer != "fin@example.com" {
		t.Fatalf("holds %v by %s", h.wallet.reviewed, h.wallet.reviewer)
	}
	if _, err := h.svc.WithdrawalDetail(ctx, auditor, w); err != nil {
		t.Fatal(err)
	}
}
