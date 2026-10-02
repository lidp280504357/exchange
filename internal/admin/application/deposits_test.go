package application

import (
	"context"
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

	if _, err := h.svc.Backfill(ctx, ops, backfillOfTrade("t1", "40"), "callback lost"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an operator backfills: %v", err)
	}
	if _, err := h.svc.Backfill(ctx, fin, ports.ManualDeposit{Network: "TRON", Amount: decimal.NewFromInt(1)}, "callback lost"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no trade: %v", err)
	}
	bad := backfillOfTrade("t0", "1")
	bad.Address = "nobody"
	if _, err := h.svc.Backfill(ctx, fin, bad, "callback lost"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("wallet-service's check: %v", err)
	}
	check, err := h.svc.CheckBackfill(ctx, fin, backfillOfTrade("t1", "40"))
	if err != nil || check.UserID != someUser || check.Asset != "USDT" || check.ValueUSDT == nil || check.ValueUSDT.String() != "40" {
		t.Fatalf("check %+v %v", check, err)
	}

	// Within the single-person limit: booked at once, entered by FINANCE.
	a, err := h.svc.Backfill(ctx, fin, backfillOfTrade("t1", "40"), "callback lost; seen in the console")
	if err != nil || a.Status != domain.ApprovalExecuted || a.Kind != domain.KindDepositBackfill || !strings.HasPrefix(a.Result, "deposit ") ||
		a.Payload["user_id"] != someUser || a.Payload["entered_by"] != "fin@example.com" || len(h.deposits.booked) != 1 {
		t.Fatalf("backfill %+v %v", a, err)
	}
	if len(h.ledger.calls) != 0 {
		t.Fatal("a backfill books nothing in the ledger directly")
	}

	// Above the limit: a second administrator approves; the deposit is
	// still entered by the one who typed it.
	big, err := h.svc.Backfill(ctx, fin, backfillOfTrade("t2", "200000"), "callback lost, large")
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

func TestDepositDecisionsAndWithdrawalHolds(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	fin, auditor := h.login(t, "fin@example.com"), h.login(t, "audit@example.com")
	dep := "0192a000-0000-7000-8000-0000000000d9"

	if _, err := h.svc.CreditDeposit(ctx, auditor, dep, "the user's"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an auditor credits: %v", err)
	}
	if _, err := h.svc.CreditDeposit(ctx, fin, dep, ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := h.svc.CreditDeposit(ctx, fin, dep, "below the minimum, waived"); err != nil {
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
