// Package consumer feeds derivatives-service from Kafka: the contract
// engine's order updates and trades, the mark prices and the degradations.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/reflect/protoregistry"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	riskv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/risk/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	"github.com/lidp280504357/exchange/internal/derivatives/adapters/marks"
	"github.com/lidp280504357/exchange/internal/derivatives/application"
	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Engine is the batch handler of derivatives.order.events and
// derivatives.trade.events: the events are applied one by one in the
// order delivered, and a failure retries the batch (never skips: a fill
// skipped would leave the positions wrong). Order and trade events may
// arrive in either order; the accounting does not depend on it.
func Engine(svc *application.Service) kafka.BatchHandler {
	return func(ctx context.Context, batch []kafka.Delivery) error {
		for _, d := range batch {
			if err := handle(ctx, svc, d.Envelope); err != nil {
				return err
			}
		}
		return nil
	}
}

func handle(ctx context.Context, svc *application.Service, env *eventv1.Envelope) error {
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
			OrderID: m.GetOrderId(), Seq: m.GetSequence(), Status: domain.StatusOpen, Filled: decimal.Zero, FilledQuote: decimal.Zero,
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
		t, err := trade(m, env)
		if err != nil {
			return err
		}
		return svc.OnTrade(ctx, t)
	}
	return nil
}

func update(ctx context.Context, svc *application.Service, orderID string, seq int64, status domain.Status, filled, quote, reason string) error {
	f, err1 := decimal.NewFromString(filled)
	q, err2 := decimal.NewFromString(quote)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("order %s: bad fill amounts %q, %q", orderID, filled, quote)
	}
	return svc.OnUpdate(ctx, domain.Update{OrderID: orderID, Seq: seq, Status: status, Filled: f, FilledQuote: q, Reason: reason})
}

func trade(m *tradev1.TradeExecuted, env *eventv1.Envelope) (application.Trade, error) {
	price, err1 := decimal.NewFromString(m.GetPrice())
	qty, err2 := decimal.NewFromString(m.GetQuantity())
	if err1 != nil || err2 != nil {
		return application.Trade{}, fmt.Errorf("trade %s: bad price %q or quantity %q", m.GetTradeId(), m.GetPrice(), m.GetQuantity())
	}
	return application.Trade{
		ID: m.GetTradeId(), Symbol: m.GetSymbol(), Seq: m.GetSequence(), Price: price, Qty: qty,
		BuyerOrderID: m.GetBuyerOrderId(), BuyerUserID: m.GetBuyerUserId(), SellerOrderID: m.GetSellerOrderId(),
		SellerUserID: m.GetSellerUserId(), BuyerIsMaker: m.GetBuyerIsMaker(), At: env.GetOccurredAt().AsTime().UTC(),
	}, nil
}

// Marks is the handler of the tail of market.candle.events: it keeps the
// contracts' mark prices.
func Marks(book *marks.Book) kafka.Handler {
	return func(_ context.Context, env *eventv1.Envelope) error {
		var m marketv1.MarkPriceUpdated
		if !env.GetPayload().MessageIs(&m) {
			return nil
		}
		if err := env.GetPayload().UnmarshalTo(&m); err != nil {
			return err
		}
		price, err := decimal.NewFromString(m.GetMarkPrice())
		if err != nil || !price.IsPositive() {
			return nil
		}
		book.Set(m.GetSymbol(), price, m.GetComputedAt().AsTime())
		return nil
	}
}

// Risk is the handler of risk.events: a contract's SystemDegraded puts it
// under reduce-only (§11.7).
func Risk(svc *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		var m riskv1.SystemDegraded
		if !env.GetPayload().MessageIs(&m) {
			return nil
		}
		if err := env.GetPayload().UnmarshalTo(&m); err != nil {
			return err
		}
		if !strings.HasSuffix(m.GetSymbol(), "-PERP") {
			return nil
		}
		return svc.OnDegraded(ctx, m.GetSymbol(), m.GetReason())
	}
}
