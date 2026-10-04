package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// approvalOf reads the operation an error names.
func approvalOf(t *testing.T, err error) string {
	t.Helper()
	var e *apperr.Error
	if !errors.As(err, &e) {
		t.Fatalf("not an operation's error: %v", err)
	}
	id, _ := e.Details["approval_id"].(string)
	if id == "" {
		t.Fatalf("no operation named: %v", err)
	}
	return id
}

func TestAKeyedFundRequestIsMadeOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	boss, fin := h.login(t, "boss@example.com"), h.login(t, "fin@example.com")
	req := FundRequest{
		Kind: domain.KindLedgerAdjustment, UserID: someUser, Asset: "USDT", Amount: usdt(10), Reason: "refund", Direct: true, Key: "k1",
	}
	a, err := h.svc.SubmitFunds(ctx, boss, req)
	if err != nil || a.Status != domain.ApprovalExecuted {
		t.Fatalf("first: %+v %v", a, err)
	}
	b, err := h.svc.SubmitFunds(ctx, boss, req)
	if err != nil || b.ID != a.ID || b.Status != domain.ApprovalExecuted || len(h.ledger.calls) != 1 || len(h.store.approvals) != 1 {
		t.Fatalf("repeated: %+v %v, %d ledger calls", b, err, len(h.ledger.calls))
	}
	other := req
	other.Amount = usdt(11)
	if _, err := h.svc.SubmitFunds(ctx, boss, other); code(err) != apperr.CodeIdempotencyConflict {
		t.Fatalf("the key with another amount: %v", err)
	}
	// A key is its administrator's own.
	if c, err := h.svc.SubmitFunds(ctx, fin, req); err != nil || c.ID == a.ID {
		t.Fatalf("another administrator's key: %+v %v", c, err)
	}

	// The ledger does not answer: the same request again finishes the same operation.
	h.ledger.err = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "down")
	unknown := req
	unknown.Key, unknown.Amount = "k2", usdt(5)
	_, err = h.svc.SubmitFunds(ctx, boss, unknown)
	id := approvalOf(t, err)
	h.ledger.err = nil
	done, err := h.svc.SubmitFunds(ctx, boss, unknown)
	if err != nil || done.ID != id || done.Status != domain.ApprovalExecuted || done.AttemptedAt.IsZero() {
		t.Fatalf("finished by the repeat: %+v %v", done, err)
	}
	last := h.ledger.calls[len(h.ledger.calls)-2:]
	if last[0].key != "approval:"+id || last[1].key != last[0].key {
		t.Fatalf("both attempts under the operation's key: %+v", last)
	}

	// Waiting for a second administrator: the repeat finds it waiting.
	asked := req
	asked.Key, asked.Direct = "k3", false
	p1, err := h.svc.SubmitFunds(ctx, boss, asked)
	if err != nil || p1.Status != domain.ApprovalPending {
		t.Fatalf("asked: %+v %v", p1, err)
	}
	n := len(h.store.approvals)
	if p2, err := h.svc.SubmitFunds(ctx, boss, asked); err != nil || p2.ID != p1.ID || len(h.store.approvals) != n {
		t.Fatalf("asked again: %+v %v", p2, err)
	}

	// A first request stopped before it recorded anything: the repeat makes it under the key's ID.
	stopped := req
	stopped.Key, stopped.Amount = "k4", usdt(7)
	norm := stopped
	if err := norm.validate(); err != nil {
		t.Fatal(err)
	}
	c, err := h.svc.claimKey(ctx, boss, norm.Key, scopeFunds, norm.fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if made, err := h.svc.SubmitFunds(ctx, boss, stopped); err != nil || made.ID != c.Ref || made.Status != domain.ApprovalExecuted {
		t.Fatalf("made under the key's ID %s: %+v %v", c.Ref, made, err)
	}

	// Purged after a day.
	h.now = h.now.Add(25 * time.Hour)
	if n, err := h.svc.PurgeKeys(ctx); err != nil || n != 5 {
		t.Fatalf("purged %d %v", n, err)
	}
}

func TestAnAttemptedOperationIsFinishedNeverRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	boss, fin := h.login(t, "boss@example.com"), h.login(t, "fin@example.com")
	asked, err := h.svc.RequestAdjustment(ctx, fin, Adjustment{UserID: someUser, Asset: "USDT", Amount: usdt(20), Reason: "refund"})
	if err != nil {
		t.Fatal(err)
	}
	// A refused decision marks nothing: the requester may still withdraw it.
	if _, err := h.svc.DecideApproval(ctx, fin, asked.ID, true, "my own"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("self approval: %v", err)
	}
	if a := h.store.approvals[asked.ID]; !a.AttemptedAt.IsZero() {
		t.Fatalf("marked by a refused decision: %+v", a)
	}

	h.ledger.err = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "down")
	_, err = h.svc.DecideApproval(ctx, boss, asked.ID, true, "checked the ticket")
	if approvalOf(t, err) != asked.ID {
		t.Fatalf("the error names another operation: %v", err)
	}
	a := h.store.approvals[asked.ID]
	if a.Status != domain.ApprovalPending || a.AttemptedAt.IsZero() || a.Result != "COMMON_UNAVAILABLE: down" {
		t.Fatalf("attempted: %+v", a)
	}
	// The unfinished attempt is audited at once (C5.5 ⑮), with the
	// decider's reason (⑰).
	if got := h.auditsOf("admin.ledger.adjustment_unfinished"); len(got) != 1 || !strings.Contains(got[0], "approval:"+asked.ID+" checked the ticket ") {
		t.Fatalf("unfinished audit %v", got)
	}
	for _, p := range []Principal{boss, fin} {
		if _, err := h.svc.DecideApproval(ctx, p, asked.ID, false, "never mind"); code(err) != "ADMIN_APPROVAL_ATTEMPTED" {
			t.Fatalf("rejected by %s: %v", p.Admin.Email, err)
		}
	}
	// Another approver finishes it.
	h.admin(t, "boss2@example.com", domain.RoleAdmin)
	boss2 := h.login(t, "boss2@example.com")
	h.ledger.err = nil
	done, err := h.svc.DecideApproval(ctx, boss2, asked.ID, true, "the ledger is back")
	if err != nil || done.Status != domain.ApprovalExecuted || done.JournalID != "journal-1" {
		t.Fatalf("finished: %+v %v", done, err)
	}
	boss = boss2
	// The decision repeated returns the operation as it left it, booking nothing more.
	calls := len(h.ledger.calls)
	if again, err := h.svc.DecideApproval(ctx, boss, asked.ID, true, "the ledger is back"); err != nil || again.Status != domain.ApprovalExecuted ||
		len(h.ledger.calls) != calls {
		t.Fatalf("repeated: %+v %v", again, err)
	}
	if _, err := h.svc.DecideApproval(ctx, boss, asked.ID, false, "the ledger is back"); code(err) != "ADMIN_APPROVAL_DECIDED" {
		t.Fatalf("the other decision afterwards: %v", err)
	}
	// A rejection repeated, likewise.
	other, err := h.svc.RequestAdjustment(ctx, fin, Adjustment{UserID: someUser, Asset: "USDT", Amount: usdt(3), Reason: "fee"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if r, err := h.svc.DecideApproval(ctx, boss, other.ID, false, "not owed"); err != nil || r.Status != domain.ApprovalRejected {
			t.Fatalf("rejected: %+v %v", r, err)
		}
	}
	// An attempted one the ledger then refuses for good: FAILED, nothing booked.
	third, err := h.svc.RequestAdjustment(ctx, fin, Adjustment{UserID: someUser, Asset: "USDT", Amount: usdt(-4), Reason: "fee"})
	if err != nil {
		t.Fatal(err)
	}
	h.ledger.err = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "down")
	if _, err := h.svc.DecideApproval(ctx, boss, third.ID, true, "checked"); approvalOf(t, err) != third.ID {
		t.Fatal(err)
	}
	h.ledger.err = apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "not enough")
	if failed, err := h.svc.DecideApproval(ctx, boss, third.ID, true, "checked again"); err != nil || failed.Status != domain.ApprovalFailed ||
		!strings.HasPrefix(failed.Result, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("refused on the retry: %+v %v", failed, err)
	}
}

func TestKeyedHoldsClosesCreditsAndMessages(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.CloseWait = time.Millisecond
	content := &fakeContent{}
	h.svc.Content = content
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	fin, ops := h.login(t, "fin@example.com"), h.login(t, "ops@example.com")

	held, err := h.svc.PlaceHold(ctx, fin, "hold-1", someUser, "USDT", decimal.NewFromInt(10), "chargeback")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := h.svc.PlaceHold(ctx, fin, "hold-1", someUser, "USDT", decimal.NewFromInt(10), "chargeback"); err != nil ||
		again.ID != held.ID || len(h.ledger.holds) != 1 {
		t.Fatalf("a hold repeated: %+v %v (%d holds)", again, err, len(h.ledger.holds))
	}
	if _, err := h.svc.PlaceHold(ctx, fin, "hold-1", someUser, "USDT", decimal.NewFromInt(20), "chargeback"); code(err) != apperr.CodeIdempotencyConflict {
		t.Fatalf("the key with another amount: %v", err)
	}
	if _, err := h.svc.ReleaseHold(ctx, fin, "release-1", someUser, held.ID, "cleared"); err != nil {
		t.Fatal(err)
	}
	if r, err := h.svc.ReleaseHold(ctx, fin, "release-1", someUser, held.ID, "cleared"); err != nil || r.ReleasedBy != "fin@example.com" {
		t.Fatalf("a release repeated: %+v %v", r, err)
	}
	if _, err := h.svc.ReleaseHold(ctx, fin, "release-2", someUser, held.ID, "cleared"); code(err) != "LEDGER_HOLD_RELEASED" {
		t.Fatalf("released again under another key: %v", err)
	}

	first, err := h.svc.ClosePosition(ctx, ops, "close-1", someUser, "BTC-USDT-PERP", "BOTH", "margin call missed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ClosePosition(ctx, ops, "close-1", someUser, "BTC-USDT-PERP", "BOTH", "margin call missed"); err != nil {
		t.Fatal(err)
	}
	if len(h.derivatives.closes) != 2 || h.derivatives.closes[0] != h.derivatives.closes[1] {
		t.Fatalf("one client order ID for both: %v (%s)", h.derivatives.closes, first)
	}
	if got := h.auditsOf("admin.derivatives.position_closed"); len(got) != 2 || !strings.Contains(got[1], `"repeated":true`) {
		t.Fatalf("close audits %v", got)
	}

	dep := "0192a000-0000-7000-8000-0000000000d9"
	if _, err := h.svc.CreditDeposit(ctx, fin, "credit-1", dep, "below the minimum, waived"); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.CreditDeposit(ctx, fin, "credit-1", dep, "below the minimum, waived"); err != nil || !strings.Contains(string(got), "CREDITED") {
		t.Fatalf("a credit repeated: %s %v", got, err)
	}
	if _, err := h.svc.CreditDeposit(ctx, fin, "credit-2", dep, "below the minimum, waived"); code(err) != "WALLET_DEPOSIT_NOT_RELEASABLE" {
		t.Fatalf("credited again under another key: %v", err)
	}

	msg := BroadcastInput{Audience: "ALL", Title: map[string]string{"zh-CN": "通知"}, Body: map[string]string{"zh-CN": "正文"}, Key: "msg-1"}
	sent := len(h.auditsOf("admin.notices.sent"))
	raw, err := h.svc.SendBroadcast(ctx, ops, msg, "everyone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SendBroadcast(ctx, ops, msg, "everyone"); err != nil {
		t.Fatal(err)
	}
	var b struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || uuid.Validate(b.ID) != nil || len(content.broadcasts) != 2 ||
		content.broadcasts[0].ID != b.ID || content.broadcasts[1].ID != b.ID {
		t.Fatalf("one message ID for both: %s %+v", raw, content.broadcasts)
	}
	if got := h.auditsOf("admin.notices.sent"); len(got) != sent+1 {
		t.Fatalf("audited once: %v", got)
	}
	changed := msg
	changed.Link = "/markets"
	if _, err := h.svc.SendBroadcast(ctx, ops, changed, "everyone"); code(err) != apperr.CodeIdempotencyConflict {
		t.Fatalf("the key with another message: %v", err)
	}
}
