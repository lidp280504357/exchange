package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	"github.com/lidp280504357/exchange/internal/matching/domain"
	"github.com/lidp280504357/exchange/internal/matching/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

// Topics names what an engine shard consumes and publishes.
type Topics struct {
	Commands   string // input, e.g. order.commands
	References string // input, the reference books (ADR-0015)
	Orders     string // order events
	Trades     string // trade events
	Depth      string // the engine's own depth snapshots
}

// SpotTopics are the spot shard's topics; DerivativesTopics the perpetual
// contracts' (implementation plan §7.3 task 2).
var (
	SpotTopics = Topics{
		Commands: event.TopicOrderCommands, References: event.TopicOrderReferences, Orders: event.TopicOrder, Trades: event.TopicTrade,
		Depth: event.TopicMarketDepthInternal,
	}
	DerivativesTopics = Topics{
		Commands: event.TopicDerivOrderCommands, References: event.TopicDerivOrderReferences, Orders: event.TopicDerivOrder,
		Trades: event.TopicDerivTrade, Depth: event.TopicDerivMarketDepthInternal,
	}
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

// referenceFromProto reads a ReferenceBookUpdate published at: the levels
// best first (bids falling, asks rising, positive amounts), the rooms
// (empty is none) and HOUSE's user when there is anything to trade.
func referenceFromProto(m *orderv1.ReferenceBookUpdate, at time.Time) (domain.Reference, error) {
	r := domain.Reference{HouseUser: m.GetHouseUserId(), At: at, BuyRoom: decimal.Zero, SellRoom: decimal.Zero}
	if m.GetHoldingsAt() != nil {
		r.HoldingsAt = m.GetHoldingsAt().AsTime()
	}
	if m.GetSymbol() == "" {
		return r, fmt.Errorf("reference book without a symbol")
	}
	var err error
	for _, f := range []struct {
		dst  *decimal.Decimal
		src  string
		name string
	}{{&r.BuyRoom, m.GetBuyRoom(), "buy_room"}, {&r.SellRoom, m.GetSellRoom(), "sell_room"}} {
		if f.src == "" {
			continue
		}
		if *f.dst, err = decimal.NewFromString(f.src); err != nil || f.dst.IsNegative() {
			return r, fmt.Errorf("reference book %s: bad %s %q", m.GetSymbol(), f.name, f.src)
		}
	}
	read := func(in []*orderv1.ReferenceLevel, falling bool) ([]domain.RefLevel, error) {
		out := make([]domain.RefLevel, 0, len(in))
		for _, l := range in {
			p, err1 := decimal.NewFromString(l.GetPrice())
			q, err2 := decimal.NewFromString(l.GetQuantity())
			if err1 != nil || err2 != nil || !p.IsPositive() || !q.IsPositive() {
				return nil, fmt.Errorf("reference book %s: bad level %q x %q", m.GetSymbol(), l.GetPrice(), l.GetQuantity())
			}
			if n := len(out); n > 0 && (falling && !p.LessThan(out[n-1].Price) || !falling && !p.GreaterThan(out[n-1].Price)) {
				return nil, fmt.Errorf("reference book %s: level %s out of order", m.GetSymbol(), p)
			}
			out = append(out, domain.RefLevel{Price: p, Quantity: q})
		}
		return out, nil
	}
	if r.Bids, err = read(m.GetBids(), true); err != nil {
		return r, err
	}
	if r.Asks, err = read(m.GetAsks(), false); err != nil {
		return r, err
	}
	if len(r.Bids)+len(r.Asks) > 0 && r.HouseUser == "" {
		return r, fmt.Errorf("reference book %s without HOUSE's user", m.GetSymbol())
	}
	return r, nil
}

// output turns an engine event into the envelope to publish.
func (e *Engine) output(ctx context.Context, ev domain.Event) (ports.Output, error) {
	topic := e.Topics.Orders
	var msg proto.Message
	filled, quote := ev.Filled.String(), ev.FilledQuote.String()
	switch ev.Kind {
	case domain.KindTrade:
		t := ev.Trade
		topic = e.Topics.Trades
		side := orderv1.Side_SIDE_BUY
		if t.TakerSide == domain.Sell {
			side = orderv1.Side_SIDE_SELL
		}
		m := &tradev1.TradeExecuted{
			TradeId: t.ID, Symbol: t.Symbol, BaseAsset: t.BaseAsset, QuoteAsset: t.QuoteAsset, Sequence: t.Seq,
			Price: t.Price.String(), Quantity: t.Quantity.String(), QuoteQuantity: t.Quote.String(), TakerSide: side,
			BuyerOrderId: t.BuyOrderID, BuyerUserId: t.BuyUserID, SellerOrderId: t.SellOrderID, SellerUserId: t.SellUserID,
			BuyerIsMaker: t.BuyerIsMaker, BuyerFee: t.BuyerFee.String(), SellerFee: t.SellerFee.String(),
			TradeNumber: t.Number,
		}
		if !t.BuyerLimit.IsZero() {
			m.BuyerLimitPrice = t.BuyerLimit.String()
		}
		switch t.HouseSide {
		case domain.Buy:
			m.HouseSide = orderv1.Side_SIDE_BUY
		case domain.Sell:
			m.HouseSide = orderv1.Side_SIDE_SELL
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
