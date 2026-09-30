// Package consumer feeds market-data-service from Kafka: trades in batches
// (a consumer group, committed after they are stored) and the engines'
// depth snapshots from the tail of their internal depth topics (only the
// latest matter). Both are relayed as the public book and trades of the
// symbols that do not show the reference market's (ADR-0015).
package consumer

import (
	"context"

	"github.com/shopspring/decimal"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/application"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Group is the consumer group on trade.events.
const Group = "market-data"

// Trades is the kafka.BatchHandler for trade.events; relay (nil for none)
// republishes the public trades.
func Trades(svc *application.Service, relay *application.Books) kafka.BatchHandler {
	return func(ctx context.Context, batch []kafka.Delivery) error {
		trades := make([]domain.Trade, 0, len(batch))
		for _, d := range batch {
			var m tradev1.TradeExecuted
			if !d.Envelope.GetPayload().MessageIs(&m) {
				continue
			}
			if err := d.Envelope.GetPayload().UnmarshalTo(&m); err != nil {
				return err
			}
			t, ok := fromProto(&m, d.Envelope)
			if !ok {
				continue // amounts that do not parse; settlement parks such trades
			}
			trades = append(trades, t)
		}
		if len(trades) == 0 {
			return nil
		}
		if err := svc.OnTrades(ctx, trades); err != nil {
			return err
		}
		if relay != nil {
			relay.RelayTrades(ctx, trades) // after storing: a redelivery would count them twice
		}
		return nil
	}
}

func fromProto(m *tradev1.TradeExecuted, env *eventv1.Envelope) (domain.Trade, bool) {
	price, err1 := decimal.NewFromString(m.GetPrice())
	qty, err2 := decimal.NewFromString(m.GetQuantity())
	quote, err3 := decimal.NewFromString(m.GetQuoteQuantity())
	if err1 != nil || err2 != nil || err3 != nil || m.GetSymbol() == "" {
		return domain.Trade{}, false
	}
	side := "BUY"
	if m.GetTakerSide() == orderv1.Side_SIDE_SELL {
		side = "SELL"
	}
	return domain.Trade{
		Symbol: m.GetSymbol(), Sequence: m.GetSequence(), ID: m.GetTradeId(), Number: m.GetTradeNumber(),
		Price: price, Quantity: qty, Quote: quote, TakerSide: side, At: env.GetOccurredAt().AsTime().UTC(),
	}, true
}

// Depth is the kafka.Handler for the tail of the engines' internal depth
// topics; relay (nil for none) republishes the public books.
func Depth(svc *application.Service, relay *application.Books) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		var d marketv1.DepthSnapshot
		if !env.GetPayload().MessageIs(&d) {
			return nil
		}
		if err := env.GetPayload().UnmarshalTo(&d); err != nil {
			return err
		}
		svc.OnDepth(&d)
		if relay != nil {
			relay.RelayDepth(ctx, &d)
		}
		return nil
	}
}
