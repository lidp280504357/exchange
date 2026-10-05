package margin

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/trading/domain"
)

func TestCheckCarriesWhatMarginServiceWeighs(t *testing.T) {
	d := decimal.RequireFromString
	limit := Check(domain.Order{
		ID: "o1", UserID: "u1", Symbol: "BTC-USDT", Side: domain.SideSell, Type: domain.TypeLimit,
		Price: d("61000"), Quantity: d("0.5"), FrozenAsset: "BTC", FrozenAmount: d("0.5"),
		AccountType: domain.AccountMarginIsolated, SideEffect: domain.SideEffectAutoBorrow,
	})
	if limit.GetOrderSide() != orderv1.Side_SIDE_SELL || limit.GetPrice() != "61000" || limit.GetQuantity() != "0.5" ||
		limit.GetFreezeAsset() != "BTC" || limit.GetFreezeAmount() != "0.5" || limit.GetAccountType() != "MARGIN_ISOLATED" ||
		limit.GetSideEffect() != "AUTO_BORROW" || limit.GetOrderId() != "o1" || limit.GetSymbol() != "BTC-USDT" {
		t.Fatalf("limit sell: %v", limit)
	}
	// A market buy by quote amount has neither price nor quantity.
	market := Check(domain.Order{
		ID: "o2", UserID: "u1", Symbol: "BTC-USDT", Side: domain.SideBuy, Type: domain.TypeMarket,
		QuoteAmount: d("100"), FrozenAsset: "USDT", FrozenAmount: d("100"), AccountType: domain.AccountMarginCross,
		SideEffect: domain.SideEffectNone,
	})
	if market.GetOrderSide() != orderv1.Side_SIDE_BUY || market.GetPrice() != "" || market.GetQuantity() != "" ||
		market.GetFreezeAmount() != "100" {
		t.Fatalf("market buy: %v", market)
	}
}

// answer is a MarginService client whose ReserveOrder fails with err, or
// answers resp (12.5 borrowed as b1 when nil).
type answer struct {
	marginv1.MarginServiceClient
	err  error
	resp *marginv1.ReserveOrderResponse
}

func (a answer) ReserveOrder(context.Context, *marginv1.ReserveOrderRequest, ...grpc.CallOption) (*marginv1.ReserveOrderResponse, error) {
	if a.err != nil {
		return nil, a.err
	}
	if a.resp != nil {
		return a.resp, nil
	}
	return &marginv1.ReserveOrderResponse{Borrowed: "12.5", BorrowId: "b1", MarginLevel: "2.4"}, nil
}

func TestAnswersOutsideTheContractAreNotTakenAsBorrows(t *testing.T) {
	ctx := context.Background()
	for _, resp := range []*marginv1.ReserveOrderResponse{
		{Borrowed: "-1", BorrowId: "b1"},
		{Borrowed: "1.5"},
		{Borrowed: "lots", BorrowId: "b1"},
	} {
		if _, err := New(answer{resp: resp}).ReserveOrder(ctx, domain.Order{ID: "o1"}); err == nil || apperr.Is(err, "MARGIN_DISABLED") {
			t.Fatalf("%v: got %v, want an unknown outcome", resp, err)
		}
	}
	// Nothing borrowed: no borrow ID either.
	r, err := New(answer{resp: &marginv1.ReserveOrderResponse{Borrowed: "0", BorrowId: "b1"}}).ReserveOrder(ctx, domain.Order{})
	if err != nil || !r.Borrowed.IsZero() || r.BorrowID != "" {
		t.Fatalf("nothing borrowed: %+v %v", r, err)
	}
}

func TestAMarginServiceWithoutTheCallRefusesTheOrder(t *testing.T) {
	ctx := context.Background()
	unimplemented := status.Error(codes.Unimplemented, "unknown method ReserveOrder")
	// As the call returns it, and as grpcx's client interceptor wraps it.
	for _, err := range []error{unimplemented, apperr.Internal(unimplemented)} {
		if _, got := New(answer{err: err}).ReserveOrder(ctx, domain.Order{}); !apperr.Is(got, "MARGIN_DISABLED") {
			t.Fatalf("%v: got %v", err, got)
		}
	}
	unavailable := apperr.Unavailable(status.Error(codes.Unavailable, "connection refused"))
	if _, got := New(answer{err: unavailable}).ReserveOrder(ctx, domain.Order{}); got != unavailable { //nolint:errorlint // passed through as is
		t.Fatalf("an outage: got %v", got)
	}
	r, err := New(answer{}).ReserveOrder(ctx, domain.Order{})
	if err != nil || !r.Borrowed.Equal(decimal.RequireFromString("12.5")) || r.BorrowID != "b1" {
		t.Fatalf("a reservation: %+v %v", r, err)
	}
}
