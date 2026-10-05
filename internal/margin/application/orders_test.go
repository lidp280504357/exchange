package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// TestOrderReplays checks a repeat of ReserveOrder after an answer that
// never came (review CR ②, C11): the order's borrow is finished, not
// checked again against the balances it changed; and the keys of the
// service's own writes are not the clients'.
func TestOrderReplays(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.features.set(flags.KeyMarginAutoBorrow, true)
	r.ledger.fund(user, "USDT", d("1000"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t", Direction: domain.DirectionIn, Account: cross, Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	buy := application.OrderInput{
		UserID: user, OrderID: uuid.Must(uuid.NewV7()).String(), Account: cross, Symbol: "BTC-USDT", Side: application.SideBuy,
		FreezeAsset: "USDT", FreezeAmount: d("1500"), SideEffect: domain.SideEffectAutoBorrow, Price: d("30000"), Quantity: d("0.05"),
	}
	// The ledger takes no posting: the borrow stays PENDING, the answer
	// unknown.
	r.svc.Ledger = &failingLedger{ledger: r.ledger}
	if _, err := r.svc.ReserveOrder(ctx, buy); code(err) != apperr.CodeUnavailable {
		t.Fatalf("with the ledger down: %v", err)
	}
	r.svc.Ledger = r.ledger
	// The repeat books that borrow and answers it, though the account,
	// valued with it on its way, no longer lacks anything.
	out, err := r.svc.ReserveOrder(ctx, buy)
	if err != nil || !out.Borrow.Equal(d("500")) || out.BorrowID == "" {
		t.Fatalf("the repeat %+v %v", out, err)
	}
	if b := r.ledger.owed(user, cross, "USDT"); !b.borrowed.Equal(d("500")) {
		t.Fatalf("borrowed once: %+v", b)
	}
	if again, err := r.svc.ReserveOrder(ctx, buy); err != nil || again.BorrowID != out.BorrowID {
		t.Fatalf("a third time %+v %v", again, err)
	}
	other := buy
	other.FreezeAmount = d("1499")
	if _, err := r.svc.ReserveOrder(ctx, other); code(err) != apperr.CodeIdempotencyConflict {
		t.Fatalf("the order with other content: %v", err)
	}
	// A client's key may not take the form of the service's own.
	for _, key := range []string{"order:" + buy.OrderID, "trade-repay:x:buyer", "liquidation:x"} {
		if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: key, Account: cross, Asset: "USDT", Amount: d("1")}); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("borrow under %s: %v", key, err)
		}
	}
	if _, err := r.svc.Repay(ctx, application.RepayInput{UserID: user, IdemKey: "trade-repay:x:seller", Account: cross, Asset: "USDT", Amount: d("1")}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("repay under an automatic repayment's key: %v", err)
	}
}
