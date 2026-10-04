package application

import (
	"context"
	"slices"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

func TestCustodiansAndTheirFees(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	fin, ops, auditor := h.login(t, "fin@example.com"), h.login(t, "ops@example.com"), h.login(t, "audit@example.com")
	w := "0192a000-0000-7000-8000-0000000000f1"
	h.wallet.held = map[string][2]string{w: {"USDT", "1.5"}}
	h.svc.Prices = fakePrices{"TRX-USDT": decimal.RequireFromString("0.25")}

	// Each custodian has its page; the name is normalized, a bad one refused.
	if _, err := h.svc.Custody(ctx, auditor, " udunmock "); err != nil || h.wallet.provider != "UDUNMOCK" {
		t.Fatalf("the stand-in's page: %v %q", err, h.wallet.provider)
	}
	if _, err := h.svc.Custody(ctx, auditor, ""); err != nil || h.wallet.provider != "" {
		t.Fatalf("the default custodian's page: %v %q", err, h.wallet.provider)
	}
	if _, err := h.svc.Custody(ctx, auditor, "udun;drop"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a bad name: %v", err)
	}
	if _, err := h.svc.CustodyCallbacks(ctx, auditor, ports.CallbackQuery{Provider: "udunmock"}); err != nil || h.wallet.callbacks != "UDUNMOCK" {
		t.Fatalf("the stand-in's callbacks: %v %q", err, h.wallet.callbacks)
	}

	// Anyone who reads withdrawals reads the fees; the status is checked.
	if _, err := h.svc.CustodyFees(ctx, auditor, ports.FeeQuery{Status: "held", Provider: "udunmock"}); err != nil || h.wallet.feesOf != "UDUNMOCK" {
		t.Fatalf("an auditor reads the stand-in's held fees: %v %q", err, h.wallet.feesOf)
	}
	if _, err := h.svc.CustodyFees(ctx, auditor, ports.FeeQuery{Status: "PAID"}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown status: %v", err)
	}

	// Deciding one moves GAS_SUPPLY: ledger.adjust.approve, a reason, a withdrawal.
	book := func(p Principal, id, asset, amount, reason string) error {
		_, err := h.svc.BookCustodyFee(ctx, p, ports.FeeBooking{WithdrawalID: id, Asset: asset, Amount: decimal.RequireFromString(amount), Reason: reason})
		return err
	}
	if err := book(ops, w, "", "0", "as reported"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an operator books: %v", err)
	}
	if err := book(fin, w, "", "0", ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	if err := book(fin, "fee-1", "", "0", "as reported"); code(err) != apperr.CodeNotFound {
		t.Fatalf("not a withdrawal: %v", err)
	}
	for _, bad := range [][2]string{{"TRX", "-1"}, {"TRX", "0.0000000000000000001"}, {"TRX", "1000000000001"}, {"TR X", "1"}} {
		if err := book(fin, w, bad[0], bad[1], "found charged"); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("%v: %v", bad, err)
		}
	}
	// As found charged: at most 5 times what was reported, by amount in its
	// asset, by worth in USDT in another; without a price, not at all
	// (review ㉖).
	for _, over := range [][2]string{{"", "7.51"}, {"USDT", "8"}, {"TRX", "30.04"}} {
		if err := book(fin, w, over[0], over[1], "found charged more"); code(err) != "ADMIN_FEE_ABOVE_REPORTED" {
			t.Fatalf("%v: %v", over, err)
		}
	}
	if err := book(fin, w, "ETH", "0.001", "found charged in ETH"); code(err) != "ADMIN_FEE_UNPRICED" {
		t.Fatalf("an asset without a price: %v", err)
	}
	// A fee not among the held ones has no reported amount to bound: as
	// charged, refused (review ㉗); as reported, wallet-service decides.
	unheld := "0192a000-0000-7000-8000-0000000000f2"
	if err := book(fin, unheld, "", "1", "found charged 1"); code(err) != "ADMIN_FEE_NOT_HELD" {
		t.Fatalf("an unheld fee as charged: %v", err)
	}
	if err := book(fin, w, "", "7.5", "found charged 7.5"); err != nil {
		t.Fatal(err)
	}
	if err := book(fin, w, "trx", "1.5", "  found charged 1.5 TRX  "); err != nil {
		t.Fatal(err)
	}
	if err := book(fin, w, "", "0", "as reported"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.WriteOffCustodyFee(ctx, ops, w, "not taken"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an operator writes off: %v", err)
	}
	if _, err := h.svc.WriteOffCustodyFee(ctx, fin, w, "not taken from the balances"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"book " + w + "  7.5 by fin@example.com", "book " + w + " TRX 1.5 by fin@example.com", "book " + w + "  0 by fin@example.com",
		"write-off " + w + " by fin@example.com",
	}
	if !slices.Equal(h.wallet.fees, want) {
		t.Fatalf("decided %q", h.wallet.fees)
	}
}
