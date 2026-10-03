package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

const someUser = "01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b"

func usdt(n int64) decimal.Decimal { return decimal.NewFromInt(n) }

// adjust asks for an adjustment of someUser's balance, at once when allowed.
func (h *harness) adjust(t *testing.T, p Principal, asset string, amount decimal.Decimal) domain.Approval {
	t.Helper()
	a, err := h.svc.SubmitFunds(context.Background(), p, FundRequest{
		Kind: domain.KindLedgerAdjustment, UserID: someUser, Asset: asset, Amount: amount, Reason: "goodwill credit", Direct: true,
	})
	if err != nil {
		t.Fatalf("adjust %s %s: %v", amount, asset, err)
	}
	return a
}

func TestOneAdministratorAdjustsAloneWithinTheLimits(t *testing.T) {
	h := newHarness(t)
	h.svc.Features, h.svc.Prices = onFlags{}, fakePrices{"BTC-USDT": decimal.NewFromInt(60_000)}
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	boss := h.login(t, "boss@example.com")

	a := h.adjust(t, boss, "usdt", decimal.RequireFromString("100.5"))
	if a.Status != domain.ApprovalExecuted || a.Mode != domain.ModeSingle || a.JournalID != "journal-1" || a.DecidedBy != boss.Admin.ID ||
		a.ValueUSDT == nil || a.ValueUSDT.String() != "100.5" || a.Escalation != "" {
		t.Fatalf("executed at once: %+v", a)
	}
	if c := h.ledger.calls[0]; c.key != "approval:"+a.ID || c.userID != someUser || c.asset != "USDT" || c.memo != "goodwill credit" ||
		c.actor != "boss@example.com" {
		t.Fatalf("ledger call %+v", c)
	}
	if got := h.actions(); !slices.Contains(got, "admin.ledger.adjustment_requested") || !slices.Contains(got, "admin.ledger.adjustment_executed") {
		t.Fatalf("audit %v", got)
	}
	// A debit is worth its absolute value; BTC at its USDT pair's last price.
	if b := h.adjust(t, boss, "BTC", decimal.RequireFromString("-1")); b.Status != domain.ApprovalExecuted || b.ValueUSDT.String() != "60000" {
		t.Fatalf("a BTC debit: %+v", b)
	}

	// Above the single-operation limit, of unknown worth: a second administrator decides.
	calls := len(h.ledger.calls)
	if big := h.adjust(t, boss, "BTC", usdt(2)); big.Status != domain.ApprovalPending || big.Mode != domain.ModeTwoPerson ||
		big.Escalation != domain.EscalationSingleMax {
		t.Fatalf("over one operation's limit: %+v", big)
	}
	if odd := h.adjust(t, boss, "XYZ", usdt(5)); odd.Status != domain.ApprovalPending || odd.Escalation != domain.EscalationNoPrice {
		t.Fatalf("no price: %+v", odd)
	}
	if len(h.ledger.calls) != calls {
		t.Fatal("an escalated operation reached the ledger")
	}

	// 60,100.5 so far; four of 99,000 bring it to 456,100.5, and 50,000 more would pass 500,000.
	for range 4 {
		h.adjust(t, boss, "USDT", usdt(99_000))
	}
	if over := h.adjust(t, boss, "USDT", usdt(50_000)); over.Status != domain.ApprovalPending || over.Escalation != domain.EscalationDailyMax {
		t.Fatalf("over the 24-hour limit: %+v", over)
	}
	view, err := h.svc.Settings(context.Background(), boss)
	if err != nil || view.Used.String() != "456100.5" || view.TwoPerson {
		t.Fatalf("settings %+v %v", view, err)
	}
	h.now = h.now.Add(24*time.Hour + time.Minute)
	if later := h.adjust(t, boss, "USDT", usdt(50_000)); later.Status != domain.ApprovalExecuted {
		t.Fatalf("a day later: %+v", later)
	}

	// Two-person approval on, or a request asked for: always a second administrator.
	h.svc.Features = onFlags{flags.KeyTwoPerson: true}
	if two := h.adjust(t, boss, "USDT", usdt(1)); two.Status != domain.ApprovalPending || two.Escalation != domain.EscalationTwoPerson {
		t.Fatalf("two-person mode: %+v", two)
	}
	h.svc.Features = onFlags{}
	asked, err := h.svc.RequestAdjustment(context.Background(), boss, Adjustment{UserID: someUser, Asset: "USDT", Amount: usdt(1), Reason: "check"})
	if err != nil || asked.Status != domain.ApprovalPending || asked.Escalation != domain.EscalationRequested {
		t.Fatalf("asked for a second administrator: %+v %v", asked, err)
	}
	if _, err := h.svc.DecideApproval(context.Background(), boss, asked.ID, true, "my own request"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("approving one's own request: %v", err)
	}
	withdrawn, err := h.svc.DecideApproval(context.Background(), boss, asked.ID, false, "not needed")
	if err != nil || withdrawn.Status != domain.ApprovalRejected {
		t.Fatalf("withdrawing one's own request: %+v %v", withdrawn, err)
	}
}

func TestSingleOperationWithAnUnknownOutcome(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	boss := h.login(t, "boss@example.com")
	h.ledger.err = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "down")
	_, err := h.svc.SubmitFunds(ctx, boss, FundRequest{
		Kind: domain.KindLedgerAdjustment, UserID: someUser, Asset: "USDT", Amount: usdt(10), Reason: "refund", Reference: "T-42", Direct: true,
	})
	var e *apperr.Error
	if !errors.As(err, &e) || e.Kind != apperr.KindUnavailable || e.Details["approval_id"] == nil {
		t.Fatalf("unknown outcome: %v", err)
	}
	id, _ := e.Details["approval_id"].(string)
	if a := h.store.approvals[id]; a.Status != domain.ApprovalPending || a.Mode != domain.ModeSingle || a.AttemptedAt.IsZero() ||
		a.Result != "COMMON_UNAVAILABLE: down" {
		t.Fatalf("left pending, attempted: %+v", a)
	}
	// It may have booked: withdrawing it is refused (C5.5 ⑥).
	if _, err := h.svc.DecideApproval(ctx, boss, id, false, "never mind"); code(err) != "ADMIN_APPROVAL_ATTEMPTED" {
		t.Fatalf("withdrawn: %v", err)
	}
	// Its requester finishes it; the journal's memo is the same both times (the ledger compares it).
	h.ledger.err = nil
	done, err := h.svc.DecideApproval(ctx, boss, id, true, "the ledger is back")
	if err != nil || done.Status != domain.ApprovalExecuted || done.JournalID != "journal-1" {
		t.Fatalf("finished: %+v %v", done, err)
	}
	if len(h.ledger.calls) != 2 || h.ledger.calls[0].memo != h.ledger.calls[1].memo || h.ledger.calls[0].memo != "refund [T-42]" {
		t.Fatalf("ledger calls %+v", h.ledger.calls)
	}

	// A refusal by the ledger fails it for good.
	h.ledger.err = apperr.New(apperr.KindUnprocessable, "LEDGER_ADJUSTMENT_DISABLED", "off")
	failed, err := h.svc.SubmitFunds(ctx, boss, FundRequest{
		Kind: domain.KindLedgerAdjustment, UserID: someUser, Asset: "USDT", Amount: usdt(-3), Reason: "fee", Direct: true,
	})
	if err != nil || failed.Status != domain.ApprovalFailed || !strings.HasPrefix(failed.Result, "LEDGER_ADJUSTMENT_DISABLED") {
		t.Fatalf("refused: %+v %v", failed, err)
	}
	if !slices.Contains(h.actions(), "admin.ledger.adjustment_failed") {
		t.Fatalf("audit %v", h.actions())
	}
}

func TestInsuranceFundContributionAlone(t *testing.T) {
	h := newHarness(t)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	fin := h.login(t, "fin@example.com")
	a, err := h.svc.SubmitFunds(context.Background(), fin, FundRequest{
		Kind: domain.KindInsuranceFund, Amount: usdt(1000), Reason: "after the drill", Direct: true,
	})
	if err != nil || a.Status != domain.ApprovalExecuted || a.JournalID != "journal-2" || a.Payload["asset"] != "USDT" {
		t.Fatalf("contribution %+v %v", a, err)
	}
	if !slices.Contains(h.actions(), "admin.derivatives.insurance_executed") {
		t.Fatalf("audit %v", h.actions())
	}
}

func TestSettings(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.Features = onFlags{}
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	boss, fin, ops := h.login(t, "boss@example.com"), h.login(t, "fin@example.com"), h.login(t, "ops@example.com")
	v, err := h.svc.Settings(ctx, ops)
	if err != nil || v.SingleMax.String() != "100000" || v.DailyMax.String() != "500000" || v.WithdrawalMax.String() != "100000" {
		t.Fatalf("defaults %+v %v", v, err)
	}
	more := usdt(200_000)
	if _, err := h.svc.UpdateSettings(ctx, fin, SettingsPatch{SingleMax: &more, Reason: "bigger refunds"}); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("finance changes the settings: %v", err)
	}
	huge := usdt(600_000)
	if _, err := h.svc.UpdateSettings(ctx, boss, SettingsPatch{SingleMax: &huge, Reason: "bigger refunds"}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a single limit above the day's: %v", err)
	}
	day := usdt(1_000_000)
	v, err = h.svc.UpdateSettings(ctx, boss, SettingsPatch{SingleMax: &more, DailyMax: &day, Reason: "bigger refunds"})
	if err != nil || v.SingleMax.String() != "200000" || v.DailyMax.String() != "1000000" || v.UpdatedBy != "boss@example.com" {
		t.Fatalf("changed %+v %v", v, err)
	}
	last := h.store.audits[len(h.store.audits)-1]
	if last.GetAction() != "admin.settings.changed" || !strings.Contains(last.GetDetails(), `"single_max_usdt":"200000"`) {
		t.Fatalf("audit %v", last)
	}
	on := true
	if _, err := h.svc.UpdateSettings(ctx, boss, SettingsPatch{TwoPerson: &on, Reason: "a second admin joined"}); err != nil ||
		!slices.Equal(h.flags.switched, []string{flags.KeyTwoPerson}) {
		t.Fatalf("two-person approval on: %v %v", err, h.flags.switched)
	}
	// The console's own flags take the right to change its settings.
	if _, err := h.svc.SwitchFlag(ctx, ops, flags.KeyTwoPerson, false, "faster refunds"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an operator switches two-person approval: %v", err)
	}
}

func TestWithdrawalReviewAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.Features, h.svc.Prices = onFlags{}, fakePrices{"BTC-USDT": decimal.NewFromInt(60_000)}
	h.admin(t, "fin@example.com", domain.RoleFinance)
	fin := h.login(t, "fin@example.com")
	if _, err := h.svc.ReviewWithdrawal(ctx, fin, "", "w1", true, "looks fine"); err != nil || h.wallet.soleMax.String() != "100000" {
		t.Fatalf("single-person mode: %v, sole max %s", err, h.wallet.soleMax)
	}
	if _, err := h.svc.ReviewWithdrawal(ctx, fin, "", "w2", false, "odd address"); err != nil || !h.wallet.soleMax.IsZero() {
		t.Fatalf("a rejection: %v, sole max %s", err, h.wallet.soleMax)
	}
	// Worth more than the limit at the current price, or of no fresh price: the approval counts, not alone.
	h.wallet.withdrawals = map[string]reviewedWithdrawal{
		"big":   {Asset: "BTC", Amount: "2", Status: "PENDING_REVIEW"},
		"odd":   {Asset: "XYZ", Amount: "1", Status: "PENDING_REVIEW"},
		"small": {Asset: "BTC", Amount: "1", Status: "PENDING_REVIEW"},
	}
	for id, sole := range map[string]string{"big": "0", "odd": "0", "small": "100000"} {
		if _, err := h.svc.ReviewWithdrawal(ctx, fin, "", id, true, "looks fine"); err != nil || h.wallet.soleMax.String() != sole {
			t.Fatalf("%s: %v, sole max %s", id, err, h.wallet.soleMax)
		}
	}
	h.svc.Features = onFlags{flags.KeyTwoPerson: true}
	if _, err := h.svc.ReviewWithdrawal(ctx, fin, "", "w3", true, "looks fine"); err != nil || !h.wallet.soleMax.IsZero() {
		t.Fatalf("two-person mode: %v, sole max %s", err, h.wallet.soleMax)
	}

	// The same review again with its key answers with the withdrawal as it left it; without the key the
	// wallet refuses a second approval by the same reviewer; the key with another review is refused.
	first, err := h.svc.ReviewWithdrawal(ctx, fin, "review-w4", "w4", true, "looks fine")
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.svc.ReviewWithdrawal(ctx, fin, "review-w4", "w4", true, "looks fine")
	if err != nil || string(again) != string(first) {
		t.Fatalf("repeated: %s %v (first %s)", again, err, first)
	}
	if _, err := h.svc.ReviewWithdrawal(ctx, fin, "", "w4", true, "looks fine"); code(err) != apperr.CodeConflict {
		t.Fatalf("approved twice: %v", err)
	}
	if _, err := h.svc.ReviewWithdrawal(ctx, fin, "review-w4", "w5", true, "looks fine"); code(err) != apperr.CodeIdempotencyConflict {
		t.Fatalf("the key with another withdrawal: %v", err)
	}
	if _, err := h.svc.ReviewWithdrawal(ctx, fin, "reject-w6", "w6", false, "odd address"); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.ReviewWithdrawal(ctx, fin, "reject-w6", "w6", false, "odd address"); err != nil || !strings.Contains(string(got), "REJECTED") {
		t.Fatalf("a rejection repeated: %s %v", got, err)
	}
}

func TestReviewBatch(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	fin, ops := h.login(t, "fin@example.com"), h.login(t, "ops@example.com")
	if _, err := h.svc.ReviewBatch(ctx, ops, "", []string{"w1"}, true, "low risk batch"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an operator reviews: %v", err)
	}
	if _, err := h.svc.ReviewBatch(ctx, fin, "", nil, true, "low risk batch"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an empty batch: %v", err)
	}
	got, err := h.svc.ReviewBatch(ctx, fin, "", []string{"w1", "busy", "w2", "w1"}, true, "low risk batch")
	if err != nil || len(got) != 3 || !got[0].OK || got[0].Status != "APPROVED" || got[1].OK || got[1].Code != apperr.CodeConflict || !got[2].OK {
		t.Fatalf("batch %+v %v", got, err)
	}
	if !slices.Equal(h.wallet.reviewed, []string{"w1", "busy", "w2"}) {
		t.Fatalf("reviewed %v: each once", h.wallet.reviewed)
	}
}

func TestTodo(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	fin, ops := h.login(t, "fin@example.com"), h.login(t, "ops@example.com")
	h.wallet.pending = 3
	if _, err := h.svc.RequestAdjustment(ctx, fin, Adjustment{UserID: someUser, Asset: "USDT", Amount: usdt(1), Reason: "check"}); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.Todo(ctx, fin); err != nil || got.Withdrawals != 3 || got.Approvals != 1 || len(got.Partial) != 0 {
		t.Fatalf("finance %+v %v", got, err)
	}
	// An operator reads withdrawals but decides no fund operations.
	if got, err := h.svc.Todo(ctx, ops); err != nil || got.Withdrawals != 3 || got.Approvals != 0 {
		t.Fatalf("operator %+v %v", got, err)
	}
	h.wallet.err = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "down")
	if got, err := h.svc.Todo(ctx, fin); err != nil || got.Withdrawals != 0 || !slices.Equal(got.Partial, []string{"withdrawals"}) {
		t.Fatalf("wallet down %+v %v", got, err)
	}
}
