package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Holders: what each user holds of an asset now - available and frozen of
// all its accounts - largest first, a user holding nothing left out (B199:
// the console's holders card, which the ClickHouse lines no longer add up
// once they expire); at most the limit listed (0: all, B202), the users
// apart summed apart from the others (B201); one who owes more than it
// holds (margin interest) is listed last and counts in the sum, not among
// those holding some (B203).
func TestHolders(t *testing.T) {
	svc, _, _ := setup(t)
	ctx := context.Background()
	big, small, none, owing := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	fund(t, svc, big, domain.Credit{Asset: "BTC", Amount: d("2"), Decimals: 8})
	fund(t, svc, small, domain.Credit{Asset: "BTC", Amount: d("0.5"), Decimals: 8})
	fund(t, svc, none, domain.Credit{Asset: "USDT", Amount: d("10"), Decimals: 6})
	// Frozen counts: the small holder's order holds part of it.
	freeze(t, svc, "order:small", small, "BTC", "0.2")
	// The welcome credit's 0.1 BTC, 0.2 borrowed and owed, 0.101 of interest
	// owed: -0.001 BTC in all.
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), owing, "SG"); err != nil {
		t.Fatal(err)
	}
	cross := domain.MarginRef{UserID: owing, AccountType: domain.AccountMarginCross}
	for _, req := range []domain.MarginRequest{
		{IdemKey: "in", Account: cross, Reference: "in", Moves: []domain.MarginMove{{Type: domain.MarginTransferIn, Asset: "USDT", Amount: d("100")}}},
		{IdemKey: "b", Account: cross, Reference: "b", Moves: []domain.MarginMove{
			{Type: domain.MarginBorrow, Asset: "BTC", Amount: d("0.2")}, {Type: domain.MarginInterest, Asset: "BTC", Amount: d("0.101")},
		}},
	} {
		if _, err := svc.PostMargin(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	h, err := svc.Holders(ctx, "BTC", 0, []string{small})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Top) != 3 || h.Top[0].UserID != big || !h.Top[0].Amount.Equal(d("2")) || h.Top[1].UserID != small || !h.Top[1].Amount.Equal(d("0.5")) ||
		h.Top[2].UserID != owing || !h.Top[2].Amount.Equal(d("-0.001")) {
		t.Fatalf("holders %+v", h.Top)
	}
	if !h.Others.Amount.Equal(d("1.999")) || h.Others.Holders != 1 || !h.Apart.Amount.Equal(d("0.5")) || h.Apart.Holders != 1 {
		t.Fatalf("sums: others %+v, apart %+v", h.Others, h.Apart)
	}
	if h, err = svc.Holders(ctx, "BTC", 1, nil); err != nil || len(h.Top) != 1 || h.Top[0].UserID != big ||
		!h.Others.Amount.Equal(d("2.499")) || h.Others.Holders != 2 || !h.Apart.Amount.IsZero() || h.Apart.Holders != 0 {
		t.Fatalf("the largest only, none apart: %+v %v", h, err)
	}
	for _, bad := range []struct {
		asset string
		limit int
		apart []string
	}{{" ", 0, nil}, {"BTC", -1, nil}, {"BTC", 1001, nil}, {"BTC", 0, []string{"nobody"}}} {
		if _, err := svc.Holders(ctx, bad.asset, bad.limit, bad.apart); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
}
