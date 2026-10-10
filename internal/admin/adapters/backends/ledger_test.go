package backends

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/platform/apperr"
)

// holdersClient answers ListHolders with its holders and keeps the asset
// asked for.
type holdersClient struct {
	ledgerv1.LedgerServiceClient
	asked   string
	holders []*ledgerv1.Holder
}

func (c *holdersClient) ListHolders(_ context.Context, in *ledgerv1.ListHoldersRequest, _ ...grpc.CallOption) (*ledgerv1.ListHoldersResponse, error) {
	c.asked = in.GetAsset()
	return &ledgerv1.ListHoldersResponse{Holders: c.holders}, nil
}

// Who holds an asset is ledger-service's ListHolders (A123): the asset
// asked for, each holder's amount as a decimal (a debt below zero), none
// for none; an amount in another shape is unavailable, not a zero.
func TestLedgerHolders(t *testing.T) {
	ctx := context.Background()
	c := &holdersClient{holders: []*ledgerv1.Holder{{UserId: "u1", Amount: "990.5"}, {UserId: "u2", Amount: "-0.25"}}}
	got, err := Ledger{C: c}.Holders(ctx, "ASTRA")
	if err != nil || c.asked != "ASTRA" || len(got) != 2 || got[0].UserID != "u1" || got[0].Amount.String() != "990.5" ||
		got[1].Amount.String() != "-0.25" {
		t.Fatalf("holders %+v %v (asked %q)", got, err, c.asked)
	}
	c.holders = nil
	if none, err := (Ledger{C: c}).Holders(ctx, "ASTRA"); err != nil || len(none) != 0 || none == nil {
		t.Fatalf("no holders %#v %v", none, err)
	}
	c.holders = []*ledgerv1.Holder{{UserId: "u1", Amount: "a lot"}}
	if _, err := (Ledger{C: c}).Holders(ctx, "ASTRA"); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("an amount in another shape: %v", err)
	}
}
