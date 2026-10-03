package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
)

// TestASuspendedAssetsWithdrawalsWaitForAnAdmin: the console lists the
// suspended assets; a withdrawal of one is approved all the same and says
// it waits; only an ADMIN lifts the suspension, with a reason (C5.5 ⑯).
func TestASuspendedAssetsWithdrawalsWaitForAnAdmin(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.Features, h.svc.Prices = onFlags{}, fakePrices{"BTC-USDT": decimal.NewFromInt(60_000)}
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	boss, fin, auditor := h.login(t, "boss@example.com"), h.login(t, "fin@example.com"), h.login(t, "audit@example.com")
	since := h.now.Add(-time.Hour)
	h.wallet.suspended = map[string]ports.Suspension{
		"USDT": {Asset: "USDT", Shortfall: "12.5", Reason: "funds missing on two custody checks", SuspendedBy: "wallet-service", SuspendedAt: since},
	}
	h.wallet.withdrawals = map[string]reviewedWithdrawal{
		"w-usdt": {Asset: "USDT", Amount: "100", Status: "PENDING_REVIEW", Approvals: []string{}},
		"w-btc":  {Asset: "BTC", Amount: "0.01", Status: "PENDING_REVIEW", Approvals: []string{}},
		"w-more": {Asset: "USDT", Amount: "50", Status: "PENDING_REVIEW", Approvals: []string{}},
	}

	if list, err := h.svc.WithdrawalSuspensions(ctx, auditor); err != nil || len(list) != 1 || list[0].Asset != "USDT" {
		t.Fatalf("every reader sees them: %+v %v", list, err)
	}

	raw, err := h.svc.ReviewWithdrawal(ctx, fin, "", "w-usdt", true, "looks fine")
	var w struct {
		Status           string `json:"status"`
		SuspendedAt      string `json:"suspended_at"`
		SuspensionReason string `json:"suspension_reason"`
	}
	if err != nil || json.Unmarshal(raw, &w) != nil || w.Status != "APPROVED" || w.SuspendedAt != since.UTC().Format(time.RFC3339) ||
		w.SuspensionReason != "funds missing on two custody checks" {
		t.Fatalf("approved, waiting: %s %v", raw, err)
	}
	if raw, err := h.svc.ReviewWithdrawal(ctx, fin, "", "w-btc", true, "looks fine"); err != nil || strings.Contains(string(raw), "suspended_at") {
		t.Fatalf("another asset: %s %v", raw, err)
	}
	res, err := h.svc.ReviewBatch(ctx, fin, "", []string{"w-more"}, true, "low risk batch")
	if err != nil || len(res) != 1 || !res[0].OK || !res[0].Suspended {
		t.Fatalf("a batch says it too: %+v %v", res, err)
	}

	if _, err := h.svc.ResumeWithdrawals(ctx, fin, "usdt", "the balance is back"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("FINANCE lifts it: %v", err)
	}
	if _, err := h.svc.ResumeWithdrawals(ctx, boss, "usdt", "ok"); code(err) != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("without a reason: %v", err)
	}
	if x, err := h.svc.ResumeWithdrawals(ctx, boss, "usdt", "the custodian credited it back"); err != nil || x.Asset != "USDT" ||
		h.wallet.reviewed[len(h.wallet.reviewed)-1] != "resume USDT boss@example.com the custodian credited it back" {
		t.Fatalf("lifted by the ADMIN: %+v %v %v", x, err, h.wallet.reviewed)
	}
	if _, err := h.svc.ResumeWithdrawals(ctx, boss, "USDT", "the custodian credited it back"); code(err) != "COMMON_NOT_FOUND" {
		t.Fatalf("lifted twice: %v", err)
	}
}
