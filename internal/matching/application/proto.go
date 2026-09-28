package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	"github.com/lidp280504357/exchange/internal/matching/domain"
	"github.com/lidp280504357/exchange/internal/matching/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

// Topics the engine publishes to.
const (
	TopicOrder = event.TopicOrder
	TopicTrade = event.TopicTrade
)

var (
	sides = map[orderv1.Side]domain.Side{orderv1.Side_SIDE_BUY: domain.Buy, orderv1.Side_SIDE_SELL: domain.Sell}
	types = map[orderv1.OrderType]domain.Type{
		orderv1.OrderType_ORDER_TYPE_LIMIT: domain.Limit, orderv1.OrderType_ORDER_TYPE_MARKET: domain.Market,
	}
	tifs = map[orderv1.TimeInForce]domain.TimeInForce{
		orderv1.TimeInForce_TIME_IN_FORCE_GTC: domain.GTC, orderv1.TimeInForce_TIME_IN_FORCE_IOC: domain.IOC,
		orderv1.TimeInForce_TIME_IN_FORCE_FOK: domain.FOK, orderv1.TimeInForce_TIME_IN_FORCE_POST_ONLY: domain.PostOnly,
	}
	stps = map[orderv1.SelfTradePrevention]domain.STP{
		orderv1.SelfTradePrevention_SELF_TRADE_PREVENTION_CANCEL_NEWEST: domain.CancelNewest,
		orderv1.SelfTradePrevention_SELF_TRADE_PREVENTION_CANCEL_OLDEST: domain.CancelOldest,
		orderv1.SelfTradePrevention_SELF_TRADE_PREVENTION_CANCEL_BOTH:   domain.CancelBoth,
	}
)

// fromProto reads an order of a PlaceOrder command.
func fromProto(p *orderv1.Order) (domain.Order, error) {
	o := domain.Order{
		ID: p.GetOrderId(), ClientOrderID: p.GetClientOrderId(), UserID: p.GetUserId(), Symbol: p.GetSymbol(),
		Side: sides[p.GetSide()], Type: types[p.GetType()], TimeInForce: tifs[p.GetTimeInForce()],
		STP: stps[p.GetSelfTradePrevention()], BaseDecimals: p.GetBaseDecimals(), QuoteDecimals: p.GetQuoteDecimals(),
		BaseAsset: p.GetBaseAsset(), QuoteAsset: p.GetQuoteAsset(),
	}
	if o.ID == "" || o.UserID == "" || o.Symbol == "" || o.Side == "" || o.Type == "" || o.TimeInForce == "" {
		return domain.Order{}, fmt.Errorf("order %q lacks its identity, side, type or time in force", o.ID)
	}
	if o.STP == "" {
		o.STP = domain.CancelNewest
	}
	// Commands from before the engine carry no assets; symbols are
	// BASE-QUOTE (instrument rule).
	if o.BaseAsset == "" || o.QuoteAsset == "" {
		o.BaseAsset, o.QuoteAsset, _ = strings.Cut(o.Symbol, "-")
	}
	for _, f := range []struct {
		dst  *decimal.Decimal
		src  string
		name string
	}{
		{&o.Price, p.GetPrice(), "price"},
		{&o.Quantity, p.GetQuantity(), "quantity"},
		{&o.QuoteAmount, p.GetQuoteAmount(), "quote_amount"},
		{&o.MakerFeeRate, p.GetMakerFeeRate(), "maker_fee_rate"},
		{&o.TakerFeeRate, p.GetTakerFeeRate(), "taker_fee_rate"},
		{&o.Protection, p.GetProtectionPrice(), "protection_price"},
		{&o.LotSize, p.GetLotSize(), "lot_size"},
	} {
		if f.src == "" {
			continue
		}
		v, err := decimal.NewFromString(f.src)
		if err != nil {
			return domain.Order{}, fmt.Errorf("order %s: bad %s %q", o.ID, f.name, f.src)
		}
		*f.dst = v
	}
	return o, nil
}

// output turns an engine event into the envelope to publish.
func (e *Engine) output(ctx context.Context, ev domain.Event) (ports.Output, error) {
	topic := TopicOrder
	var msg proto.Message
	filled, quote := ev.Filled.String(), ev.FilledQuote.String()
	switch ev.Kind {
	case domain.KindTrade:
		t := ev.Trade
		topic = TopicTrade
		side := orderv1.Side_SIDE_BUY
		if t.TakerSide == domain.Sell {
			side = orderv1.Side_SIDE_SELL
		}
		m := &tradev1.TradeExecuted{
			TradeId: t.ID, Symbol: t.Symbol, BaseAsset: t.BaseAsset, QuoteAsset: t.QuoteAsset, Sequence: t.Seq,
			Price: t.Price.String(), Quantity: t.Quantity.String(), QuoteQuantity: t.Quote.String(), TakerSide: side,
			BuyerOrderId: t.BuyOrderID, BuyerUserId: t.BuyUserID, SellerOrderId: t.SellOrderID, SellerUserId: t.SellUserID,
			BuyerIsMaker: t.BuyerIsMaker, BuyerFee: t.BuyerFee.String(), SellerFee: t.SellerFee.String(),
		}
		if !t.BuyerLimit.IsZero() {
			m.BuyerLimitPrice = t.BuyerLimit.String()
		}
		msg = m
	case domain.KindOpened:
		msg = &orderv1.OrderOpened{OrderId: ev.OrderID, UserId: ev.UserID, Symbol: ev.Symbol, Sequence: ev.Seq}
	case domain.KindPartiallyFilled:
		msg = &orderv1.OrderPartiallyFilled{
			OrderId: ev.OrderID, UserId: ev.UserID, Symbol: ev.Symbol, Sequence: ev.Seq,
			TradeId: ev.TradeID, FilledQuantity: filled, FilledQuote: quote,
		}
	case domain.KindFilled:
		msg = &orderv1.OrderFilled{
			OrderId: ev.OrderID, UserId: ev.UserID, Symbol: ev.Symbol, Sequence: ev.Seq,
			TradeId: ev.TradeID, FilledQuantity: filled, FilledQuote: quote,
		}
	case domain.KindCanceled:
		msg = &orderv1.OrderCanceled{
			OrderId: ev.OrderID, UserId: ev.UserID, Symbol: ev.Symbol, Sequence: ev.Seq,
			FilledQuantity: filled, FilledQuote: quote, Reason: ev.Reason,
		}
	case domain.KindRejected:
		msg = &orderv1.OrderRejected{
			OrderId: ev.OrderID, ClientOrderId: ev.ClientOrderID, UserId: ev.UserID, Symbol: ev.Symbol,
			ReasonCode: ev.Reason, Sequence: ev.Seq,
		}
	default:
		return ports.Output{}, fmt.Errorf("unknown event kind %s", ev.Kind)
	}
	env, err := e.events.New(ctx, msg, "symbol", ev.Symbol)
	if err != nil {
		return ports.Output{}, err
	}
	return ports.Output{Topic: topic, Envelope: env}, nil
}
