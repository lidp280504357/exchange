// Package consumer handles the matching engine's events for
// spot-trading-service.
package consumer

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/reflect/protoregistry"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/skill/exchange/api/gen/go/exchange/trade/v1"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/trading/application"
	"github.com/skill/exchange/internal/trading/domain"
)

// Handler applies order updates from order.events and records fills from
// trade.events. The service's own events (OrderAccepted, and
// OrderRejected without a sequence) are skipped; updates are idempotent
// by sequence and fills by trade.
func Handler(svc *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		msg, err := env.GetPayload().UnmarshalNew()
		if err != nil {
			if errors.Is(err, protoregistry.NotFound) {
				return nil // a newer event type this build does not know
			}
			return err
		}
		switch m := msg.(type) {
		case *orderv1.OrderOpened:
			return svc.OnUpdate(ctx, domain.Update{
				OrderID: m.GetOrderId(), Seq: m.GetSequence(), Status: domain.StatusOpen,
				Filled: decimal.Zero, FilledQuote: decimal.Zero,
			})
		case *orderv1.OrderPartiallyFilled:
			return update(ctx, svc, m.GetOrderId(), m.GetSequence(), domain.StatusPartiallyFilled, m.GetFilledQuantity(), m.GetFilledQuote(), "")
		case *orderv1.OrderFilled:
			return update(ctx, svc, m.GetOrderId(), m.GetSequence(), domain.StatusFilled, m.GetFilledQuantity(), m.GetFilledQuote(), "")
		case *orderv1.OrderCanceled:
			return update(ctx, svc, m.GetOrderId(), m.GetSequence(), domain.StatusCanceled, m.GetFilledQuantity(), m.GetFilledQuote(), m.GetReason())
		case *orderv1.OrderRejected:
			if m.GetSequence() == 0 {
				return nil // the service's own rejection
			}
			return update(ctx, svc, m.GetOrderId(), m.GetSequence(), domain.StatusRejected, "0", "0", m.GetReasonCode())
		case *tradev1.TradeExecuted:
			return fills(ctx, svc, env, m)
		}
		return nil
	}
}

func update(ctx context.Context, svc *application.Service, orderID string, seq int64, status domain.Status, filled, quote, reason string) error {
	f, err1 := decimal.NewFromString(filled)
	q, err2 := decimal.NewFromString(quote)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("order %s: bad fill amounts %q, %q", orderID, filled, quote)
	}
	return svc.OnUpdate(ctx, domain.Update{OrderID: orderID, Seq: seq, Status: status, Filled: f, FilledQuote: q, Reason: reason})
}

// fills records both sides of a trade; HOUSE's side (ADR-0015) has no
// order here and is not recorded.
func fills(ctx context.Context, svc *application.Service, env *eventv1.Envelope, t *tradev1.TradeExecuted) error {
	var amounts [5]decimal.Decimal
	for i, s := range []string{t.GetPrice(), t.GetQuantity(), t.GetQuoteQuantity(), t.GetBuyerFee(), t.GetSellerFee()} {
		v, err := decimal.NewFromString(s)
		if err != nil {
			return fmt.Errorf("trade %s: bad amount %q", t.GetTradeId(), s)
		}
		amounts[i] = v
	}
	price, qty, quote, buyerFee, sellerFee := amounts[0], amounts[1], amounts[2], amounts[3], amounts[4]
	at := env.GetOccurredAt().AsTime()
	for _, f := range []domain.Fill{
		{
			OrderID: t.GetBuyerOrderId(), UserID: t.GetBuyerUserId(), Side: domain.SideBuy, Maker: t.GetBuyerIsMaker(),
			FeeAsset: t.GetBaseAsset(), Fee: buyerFee,
		},
		{
			OrderID: t.GetSellerOrderId(), UserID: t.GetSellerUserId(), Side: domain.SideSell, Maker: !t.GetBuyerIsMaker(),
			FeeAsset: t.GetQuoteAsset(), Fee: sellerFee,
		},
	} {
		if (f.Side == domain.SideBuy && t.GetHouseSide() == orderv1.Side_SIDE_BUY) ||
			(f.Side == domain.SideSell && t.GetHouseSide() == orderv1.Side_SIDE_SELL) {
			continue
		}
		f.TradeID, f.Symbol, f.Price, f.Quantity, f.Quote, f.Seq, f.ExecutedAt = t.GetTradeId(), t.GetSymbol(), price, qty, quote, t.GetSequence(), at
		if err := svc.OnFill(ctx, f); err != nil {
			return err
		}
	}
	return nil
}
