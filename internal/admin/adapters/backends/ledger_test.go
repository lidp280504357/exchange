package backends

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/platform/apperr"
)

// holdersClient answers ListHolders with its page and keeps what was asked.
type holdersClient struct {
	ledgerv1.LedgerServiceClient
	asked *ledgerv1.ListHoldersRequest
	page  *ledgerv1.ListHoldersResponse
}

func (c *holdersClient) ListHolders(_ context.Context, in *ledgerv1.ListHoldersRequest, _ ...grpc.CallOption) (*ledgerv1.ListHoldersResponse, error) {
	c.asked = in
	return c.page, nil
}

// Who holds an asset is ledger-service's ListHolders (A123b): the asset,
// the limit and the users apart asked for; the largest holders and the
// two sums as decimals (debts below zero), zero for a sum not given; an
// amount in another shape is unavailable, not a zero.
func TestLedgerHolders(t *testing.T) {
	ctx := context.Background()
	c := &holdersClient{page: &ledgerv1.ListHoldersResponse{
		Holders: []*ledgerv1.Holder{{UserId: "b1", Amount: "990.5"}, {UserId: "u1", Amount: "9.5"}},
		Others:  &ledgerv1.HolderSum{Amount: "9.25", Holders: 1},
		Apart:   &ledgerv1.HolderSum{Amount: "990.5", Holders: 1},
	}}
	got, err := Ledger{C: c}.Holders(ctx, "ASTRA", 1000, []string{"b1"})
	if err != nil || c.asked.GetAsset() != "ASTRA" || c.asked.GetLimit() != 1000 || !slices.Equal(c.asked.GetApartUserIds(), []string{"b1"}) {
		t.Fatalf("asked %v: %v", c.asked, err)
	}
	if len(got.Top) != 2 || got.Top[0].UserID != "b1" || got.Top[0].Amount.String() != "990.5" || got.Others.Amount.String() != "9.25" ||
		got.Others.Holders != 1 || got.Apart.Amount.String() != "990.5" || got.Apart.Holders != 1 {
		t.Fatalf("the page %+v", got)
	}
	c.page = &ledgerv1.ListHoldersResponse{}
	if none, err := (Ledger{C: c}).Holders(ctx, "ASTRA", 1, nil); err != nil || len(none.Top) != 0 || none.Top == nil || !none.Apart.Amount.IsZero() {
		t.Fatalf("nobody %#v %v", none, err)
	}
	c.page = &ledgerv1.ListHoldersResponse{Others: &ledgerv1.HolderSum{Amount: "a lot"}}
	if _, err := (Ledger{C: c}).Holders(ctx, "ASTRA", 1, nil); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("a sum in another shape: %v", err)
	}
	c.page = &ledgerv1.ListHoldersResponse{Holders: []*ledgerv1.Holder{{UserId: "u1", Amount: "-"}}}
	if _, err := (Ledger{C: c}).Holders(ctx, "ASTRA", 1, nil); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("an amount in another shape: %v", err)
	}
}
