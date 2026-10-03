package application

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func TestBalancesAreValued(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	h.svc.Prices = fakePrices{"BTC-USDT": decimal.NewFromInt(60_000)}
	h.users.balances = []ports.Balance{
		{AccountType: "FUTURES", Asset: "USDT", Available: "100", Frozen: "50"},
		{AccountType: "SPOT", Asset: "XYZ", Available: "3", Frozen: "0"},
		{AccountType: "SPOT", Asset: "USDT", Available: "10", Frozen: "0"},
		{AccountType: "SPOT", Asset: "BTC", Available: "0.1", Frozen: "0.05"},
	}
	b, err := h.svc.Balances(ctx, auditor, someUser)
	if err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(b.Balances))
	for _, r := range b.Balances {
		order = append(order, r.AccountType+":"+r.Asset)
	}
	if !slices.Equal(order, []string{"SPOT:BTC", "SPOT:USDT", "SPOT:XYZ", "FUTURES:USDT"}) {
		t.Fatalf("SPOT first, the larger worth first: %v", order)
	}
	if !b.Balances[0].Total.Equal(decimal.RequireFromString("0.15")) || b.Balances[0].ValueUSDT.String() != "9000" {
		t.Fatalf("BTC %+v", b.Balances[0])
	}
	if b.Balances[2].ValueUSDT != nil || !slices.Equal(b.Unpriced, []string{"XYZ"}) {
		t.Fatalf("XYZ has no price: %+v %v", b.Balances[2], b.Unpriced)
	}
	if b.TotalUSDT.String() != "9160" {
		t.Fatalf("total %s", b.TotalUSDT)
	}
	if _, err := h.svc.Balances(ctx, auditor, "nobody"); code(err) != apperr.CodeNotFound {
		t.Fatalf("a bad user ID: %v", err)
	}
}

func TestHoldsGoThroughTheLedger(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	fin, auditor := h.login(t, "fin@example.com"), h.login(t, "audit@example.com")
	other := "01929c3e-7f3a-7d7e-8a1b-000000000099"

	if _, err := h.svc.PlaceHold(ctx, auditor, "", someUser, "USDT", decimal.NewFromInt(10), "chargeback"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an auditor holds funds: %v", err)
	}
	if _, err := h.svc.PlaceHold(ctx, fin, "", someUser, "USDT", decimal.NewFromInt(-1), "chargeback"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a negative hold: %v", err)
	}
	held, err := h.svc.PlaceHold(ctx, fin, "", someUser, " usdt ", decimal.NewFromInt(10), "chargeback under review")
	if err != nil || held.Asset != "USDT" || held.Actor != "fin@example.com" || held.ID == "" {
		t.Fatalf("hold %+v %v", held, err)
	}
	if list, err := h.svc.Holds(ctx, auditor, someUser); err != nil || len(list) != 1 {
		t.Fatalf("holds %+v %v", list, err)
	}
	if _, err := h.svc.ReleaseHold(ctx, fin, "", other, held.ID, "wrong user"); code(err) != apperr.CodeNotFound {
		t.Fatalf("another user's hold: %v", err)
	}
	released, err := h.svc.ReleaseHold(ctx, fin, "", someUser, held.ID, "cleared by the bank")
	if err != nil || released.ReleasedBy != "fin@example.com" {
		t.Fatalf("release %+v %v", released, err)
	}
}

func TestOrderCancelsAndForceClose(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.CloseWait = time.Millisecond
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	ops, fin := h.login(t, "ops@example.com"), h.login(t, "fin@example.com")
	order := "0192a000-0000-7000-8000-0000000000d1"

	if _, err := h.svc.CancelOrder(ctx, fin, someUser, order, "stuck order"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("finance cancels: %v", err)
	}
	if _, err := h.svc.CancelOrder(ctx, ops, someUser, "not-an-id", "stuck order"); code(err) != apperr.CodeNotFound {
		t.Fatalf("a bad order ID: %v", err)
	}
	if _, err := h.svc.CancelOrder(ctx, ops, someUser, order, "stuck order"); err != nil || !slices.Contains(h.orders.canceled, someUser+"/"+order) {
		t.Fatalf("spot cancel %v %v", h.orders.canceled, err)
	}
	if _, err := h.svc.CancelContractOrder(ctx, ops, someUser, order, "stuck order"); err != nil || len(h.derivatives.canceled) != 1 {
		t.Fatalf("contract cancel %v %v", h.derivatives.canceled, err)
	}

	if _, err := h.svc.ClosePosition(ctx, fin, "", someUser, "BTC-USDT-PERP", "BOTH", "margin call missed"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("finance closes: %v", err)
	}
	h.derivatives.pending = 2
	raw, err := h.svc.ClosePosition(ctx, ops, "", someUser, "btc-usdt-perp", "both", "margin call missed")
	if err != nil || orderID(raw) == "" || len(h.derivatives.closes) != 3 {
		t.Fatalf("close after two pending answers: %s %v %v", raw, h.derivatives.closes, err)
	}
	// Every attempt carries the same client order ID: a repeat is the same order.
	client := strings.Fields(h.derivatives.closes[0])[2]
	for _, c := range h.derivatives.closes {
		if !strings.HasSuffix(c, client) || !strings.HasPrefix(c, "BTC-USDT-PERP BOTH ") {
			t.Fatalf("attempts %v", h.derivatives.closes)
		}
	}
	h.derivatives.pending = closeAttempts
	if _, err := h.svc.ClosePosition(ctx, ops, "", someUser, "BTC-USDT-PERP", "BOTH", "margin call missed"); code(err) != "DERIV_CLOSE_PENDING" {
		t.Fatalf("still pending after every attempt: %v", err)
	}

	got := h.auditsOf("admin.derivatives.position_closed")
	if len(got) != 1 || !strings.Contains(got[0], `"order_id":"0192a000-0000-7000-8000-0000000000c1"`) {
		t.Fatalf("close audit %v", got)
	}
	if got := h.auditsOf("admin.orders.canceled"); len(got) != 1 || !strings.Contains(got[0], order) {
		t.Fatalf("cancel audit %v", got)
	}
}

func TestFuturesAdjustments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	fin := h.login(t, "fin@example.com")

	if _, err := h.svc.SubmitFunds(ctx, fin, FundRequest{
		Kind: domain.KindLedgerAdjustment, UserID: someUser, AccountType: "MARGIN", Asset: "USDT", Amount: decimal.NewFromInt(5), Reason: "goodwill",
	}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown account: %v", err)
	}
	a, err := h.svc.SubmitFunds(ctx, fin, FundRequest{
		Kind: domain.KindLedgerAdjustment, UserID: someUser, AccountType: "futures", Asset: "USDT", Amount: decimal.NewFromInt(5),
		Reason: "goodwill on fees", Direct: true,
	})
	if err != nil || a.Status != domain.ApprovalExecuted || a.Payload["account_type"] != "FUTURES" {
		t.Fatalf("futures adjustment %+v %v", a, err)
	}
	last := h.ledger.calls[len(h.ledger.calls)-1]
	if last.account != "FUTURES" || last.key != "approval:"+a.ID {
		t.Fatalf("ledger call %+v", last)
	}
	spot, err := h.svc.SubmitFunds(ctx, fin, FundRequest{
		Kind: domain.KindLedgerAdjustment, UserID: someUser, Asset: "USDT", Amount: decimal.NewFromInt(5), Reason: "goodwill", Direct: true,
	})
	if err != nil || spot.Payload["account_type"] != "" || h.ledger.calls[len(h.ledger.calls)-1].account != "SPOT" {
		t.Fatalf("spot by default %+v %v", spot, err)
	}
}
