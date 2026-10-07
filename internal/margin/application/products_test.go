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

// While spot trading is closed (design 2026-10-07 product switches §1 #7)
// a transfer into a margin account takes only an asset the account owes,
// however much of it - a debt must stay payable - and refuses any other
// with PRODUCT_CLOSED; transfers out go on. Opened again, anything goes
// in.
func TestClosedSpotTakesOnlyWhatPaysADebt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.ledger.fund(user, "USDT", d("5000"))
	r.ledger.fund(user, "BTC", d("1"))
	transfer := func(key string, dir domain.Direction, account domain.Account, asset, amount string) error {
		_, err := r.svc.Transfer(ctx, application.TransferInput{
			UserID: user, IdemKey: key, Direction: dir, Account: account, Asset: asset, Amount: d(amount),
		})
		return err
	}
	if err := transfer("in-usdt", domain.DirectionIn, cross, "USDT", "1000"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-1", Account: cross, Asset: "USDT", Amount: d("500")}); err != nil {
		t.Fatal(err)
	}

	r.features.shut(flags.KeyProductSpot, true)
	err := transfer("in-btc", domain.DirectionIn, cross, "BTC", "0.1")
	if e := apperr.From(err); e.Code != flags.CodeProductClosed || e.Kind != apperr.KindForbidden || e.Details["product"] != "spot" {
		t.Fatalf("BTC, owed by no one, in while spot is closed: %v", err)
	}
	if err := transfer("in-iso", domain.DirectionIn, domain.Isolated("BTC-USDT"), "USDT", "10"); code(err) != flags.CodeProductClosed {
		t.Fatalf("a new isolated account, owing nothing: %v", err)
	}
	// USDT is owed (500 and its interest): it goes in, beyond the debt too.
	if err := transfer("in-repay", domain.DirectionIn, cross, "USDT", "2000"); err != nil {
		t.Fatalf("an owed asset in while spot is closed: %v", err)
	}
	if b := r.ledger.owed(user, cross, "USDT"); !b.free.Equal(d("3500")) {
		t.Fatalf("the cross account's USDT %+v", b)
	}
	if err := transfer("out", domain.DirectionOut, cross, "USDT", "100"); err != nil {
		t.Fatalf("out while spot is closed: %v", err)
	}

	r.features.shut(flags.KeyProductSpot, false)
	if err := transfer("in-btc-open", domain.DirectionIn, cross, "BTC", "0.1"); err != nil {
		t.Fatalf("BTC in once spot is open again: %v", err)
	}
}
