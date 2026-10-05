package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// TestWritesOnTheirWay checks that a borrow or a transfer out checked
// while another of the user's writes is on its way to the ledger counts
// that write (review CK ①).
func TestWritesOnTheirWay(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.ledger.fund(user, "USDT", d("1000"))
	transfer := func(key string, dir domain.Direction, amount string) error {
		_, err := r.svc.Transfer(ctx, application.TransferInput{
			UserID: user, IdemKey: key, Direction: dir, Account: cross, Asset: "USDT", Amount: d(amount),
		})
		return err
	}
	if err := transfer("t-in", domain.DirectionIn, "1000"); err != nil {
		t.Fatal(err)
	}
	// A borrow of 1500 on its way: the ledger takes no postings.
	r.svc.Ledger = &failingLedger{ledger: r.ledger}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b", Account: cross, Asset: "USDT", Amount: d("1500")}); code(err) != apperr.CodeUnavailable {
		t.Fatalf("borrow during the outage: %v", err)
	}
	r.svc.Ledger = r.ledger
	// It counts as lent: 1000 own, 1500 borrowed and 0.015 owed for its
	// first hour leave 999.985 x 2 - 1500.015 = 499.955 at 3x.
	room, err := r.svc.MaxBorrowable(ctx, user, cross, "USDT")
	if err != nil || !room.Amount.Equal(d("499.955")) || room.LimitedBy != domain.LimitLeverage {
		t.Fatalf("max borrowable %+v %v", room, err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-2", Account: cross, Asset: "USDT", Amount: d("500")}); code(err) != "MARGIN_LIMIT" {
		t.Fatalf("a second borrow beyond the room: %v", err)
	}
	// Out, as though the 1500 were lent: at most 2500 - 1.3 x 1500.015.
	if err := transfer("t-out-big", domain.DirectionOut, "600"); code(err) != "MARGIN_LEVEL_TOO_LOW" {
		t.Fatalf("out beyond the level: %v", err)
	}
	// A transfer out on its way counts as gone: 2000 - 1950.0195 left.
	r.svc.Ledger = &failingLedger{ledger: r.ledger}
	if err := transfer("t-out", domain.DirectionOut, "500"); code(err) != apperr.CodeUnavailable {
		t.Fatalf("out during the outage: %v", err)
	}
	r.svc.Ledger = r.ledger
	if err := transfer("t-out-2", domain.DirectionOut, "100"); code(err) != "MARGIN_LEVEL_TOO_LOW" {
		t.Fatalf("out beside one on its way: %v", err)
	}
	// Recovery books both; the account stays at its warning level or above.
	r.at(r.now().Add(time.Minute))
	if n, err := r.svc.Recover(ctx); err != nil || n != 2 {
		t.Fatalf("recover %d %v", n, err)
	}
	b := r.ledger.owed(user, cross, "USDT")
	if !b.free.Equal(d("2000")) || !b.borrowed.Equal(d("1500")) || !b.interest.Equal(d("0.015")) {
		t.Fatalf("ledger %+v", b)
	}
	if level := b.free.DivRound(b.borrowed.Add(b.interest), 8); level.LessThan(d("1.3")) {
		t.Fatalf("level %s", level)
	}
}

// accrueDown takes margin postings but no interest.
type accrueDown struct{ *ledger }

func (accrueDown) Accrue(context.Context, string, string, string, []ports.Accrual) (string, error) {
	return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "ledger down")
}

// TestInterestOnItsWay checks that an hour's charges stored before the
// ledger took them count against the account and are posted as stored
// on the next pass (review CK ①②).
func TestInterestOnItsWay(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.ledger.fund(user, "USDT", d("1000"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t", Direction: domain.DirectionIn, Account: cross, Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	// 10:15: 1000 borrowed, 0.01 for the first hour.
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b", Account: cross, Asset: "USDT", Amount: d("1000")}); err != nil {
		t.Fatal(err)
	}
	r.at(time.Date(2026, 10, 6, 11, 0, 35, 0, time.UTC))
	r.svc.Ledger = accrueDown{ledger: r.ledger}
	if n, err := r.svc.ChargeInterest(ctx); err != nil || n != 0 {
		t.Fatalf("11:00 with the ledger down: %d %v", n, err)
	}
	// The 11:00 charge (0.01) counts already: 999.98 x 2 - 1000.02.
	want := d("999.94")
	if room, err := r.svc.MaxBorrowable(ctx, user, cross, "USDT"); err != nil || !room.Amount.Equal(want) {
		t.Fatalf("max borrowable with the charge on its way %+v %v", room, err)
	}
	r.svc.Ledger = r.ledger
	if n, err := r.svc.ChargeInterest(ctx); err != nil || n != 1 {
		t.Fatalf("11:00 again: %d %v", n, err)
	}
	if b := r.ledger.owed(user, cross, "USDT"); !b.interest.Equal(d("0.02")) {
		t.Fatalf("interest %+v", b)
	}
	if room, err := r.svc.MaxBorrowable(ctx, user, cross, "USDT"); err != nil || !room.Amount.Equal(want) {
		t.Fatalf("max borrowable after the charge %+v %v", room, err)
	}
	if n, err := r.svc.ChargeInterest(ctx); err != nil || n != 0 {
		t.Fatalf("a third pass: %d %v", n, err)
	}
}
