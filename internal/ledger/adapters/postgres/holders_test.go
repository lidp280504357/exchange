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
// once they expire).
func TestHolders(t *testing.T) {
	svc, _, _ := setup(t)
	ctx := context.Background()
	big, small, none := uuid.NewString(), uuid.NewString(), uuid.NewString()
	fund(t, svc, big, domain.Credit{Asset: "BTC", Amount: d("2"), Decimals: 8})
	fund(t, svc, small, domain.Credit{Asset: "BTC", Amount: d("0.5"), Decimals: 8})
	fund(t, svc, none, domain.Credit{Asset: "USDT", Amount: d("10"), Decimals: 6})
	// Frozen counts: the small holder's order holds part of it.
	freeze(t, svc, "order:small", small, "BTC", "0.2")
	list, err := svc.Holders(ctx, "BTC")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].UserID != big || !list[0].Amount.Equal(d("2")) || list[1].UserID != small || !list[1].Amount.Equal(d("0.5")) {
		t.Fatalf("holders %+v", list)
	}
	if _, err := svc.Holders(ctx, " "); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no asset: %v", err)
	}
}
