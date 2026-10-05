// Package grpcapi serves margin-service's gRPC API (margin.proto
// MarginService) to spot-trading-service: the checks of orders on margin
// accounts and their borrows.
package grpcapi

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Server implements marginv1.MarginServiceServer.
type Server struct {
	marginv1.UnimplementedMarginServiceServer
	svc *application.Service
}

// NewServer returns the gRPC API over svc.
func NewServer(svc *application.Service) *Server { return &Server{svc: svc} }

func decimalOf(name, s string, required bool) (decimal.Decimal, error) {
	if s == "" && !required {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, apperr.Invalid(name + " must be a decimal string")
	}
	return d, nil
}

// orderInput reads an OrderCheck: the side from order_side, else from the
// deprecated side.
func orderInput(o *marginv1.OrderCheck) (application.OrderInput, error) {
	if _, err := uuid.Parse(o.GetUserId()); err != nil {
		return application.OrderInput{}, apperr.Invalid("user_id must be a UUID")
	}
	if _, err := uuid.Parse(o.GetOrderId()); err != nil {
		return application.OrderInput{}, apperr.Invalid("order_id must be a UUID")
	}
	symbol := ""
	if o.GetAccountType() == string(domain.AccountIsolated) {
		symbol = o.GetSymbol()
	}
	a, err := domain.ParseAccount(o.GetAccountType(), symbol)
	if err != nil {
		return application.OrderInput{}, err
	}
	in := application.OrderInput{
		UserID: o.GetUserId(), OrderID: o.GetOrderId(), Account: a, Symbol: o.GetSymbol(), FreezeAsset: o.GetFreezeAsset(),
		SideEffect: domain.SideEffect(o.GetSideEffect()),
	}
	switch o.GetOrderSide() {
	case orderv1.Side_SIDE_BUY:
		in.Side = application.SideBuy
	case orderv1.Side_SIDE_SELL:
		in.Side = application.SideSell
	default:
		in.Side = o.GetSide() //nolint:staticcheck // the deprecated field, read when order_side is unset
	}
	switch in.SideEffect {
	case "":
		in.SideEffect = domain.SideEffectNone
	case domain.SideEffectNone, domain.SideEffectAutoBorrow, domain.SideEffectAutoRepay:
	default:
		return application.OrderInput{}, apperr.Invalid("side_effect must be NONE, AUTO_BORROW or AUTO_REPAY")
	}
	if in.FreezeAmount, err = decimalOf("freeze_amount", o.GetFreezeAmount(), true); err != nil {
		return application.OrderInput{}, err
	}
	if in.Price, err = decimalOf("price", o.GetPrice(), false); err != nil {
		return application.OrderInput{}, err
	}
	if in.Quantity, err = decimalOf("quantity", o.GetQuantity(), false); err != nil {
		return application.OrderInput{}, err
	}
	return in, nil
}

// answer turns an error into the answers spot-trading-service takes as
// final or unknown (review CM, C8): a refusal is InvalidArgument,
// FailedPrecondition, AlreadyExists, PermissionDenied or Unavailable with
// MARGIN_PRICE_UNAVAILABLE, and the order is refused; Internal and any
// other Unavailable are unknown, and the trading service asks again. A
// dependency's NotFound (a pair the instruments do not know) is an
// invalid order; its rate limits and credentials are no fault of the
// order, so they are unknown.
func answer(err error) error {
	e := apperr.From(err)
	switch e.Kind {
	case apperr.KindNotFound:
		c := *e
		c.Kind = apperr.KindInvalid
		return &c
	case apperr.KindRateLimited, apperr.KindUnauthenticated:
		return apperr.Unavailable(err)
	}
	return err
}

func levelOf(l *decimal.Decimal) string {
	if l == nil {
		return ""
	}
	return l.String()
}

// CheckOrder answers whether an order may go on a margin account,
// changing nothing.
func (s *Server) CheckOrder(ctx context.Context, req *marginv1.CheckOrderRequest) (*marginv1.CheckOrderResponse, error) {
	in, err := orderInput(req.GetOrder())
	if err != nil {
		return nil, answer(err)
	}
	out, err := s.svc.CheckOrder(ctx, in)
	if err != nil {
		return nil, answer(err)
	}
	return &marginv1.CheckOrderResponse{Borrow: out.Borrow.String(), MarginLevel: levelOf(out.MarginLevel)}, nil
}

// ReserveOrder checks an order and, with AUTO_BORROW, borrows what it
// lacks; a repeat returns the first answer.
func (s *Server) ReserveOrder(ctx context.Context, req *marginv1.ReserveOrderRequest) (*marginv1.ReserveOrderResponse, error) {
	in, err := orderInput(req.GetOrder())
	if err != nil {
		return nil, answer(err)
	}
	out, err := s.svc.ReserveOrder(ctx, in)
	if err != nil {
		return nil, answer(err)
	}
	return &marginv1.ReserveOrderResponse{Borrowed: out.Borrow.String(), BorrowId: out.BorrowID, MarginLevel: levelOf(out.MarginLevel)}, nil
}
