package gateway

import (
	"context"
	"strings"
	"time"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
)

// WSTopics are the topics the hub follows from their end (requirements
// §7.3: every gateway instance reads every partition).
var WSTopics = []string{
	"ledger.events", "notification.events", "order.events", "trade.events", "market.depth", "market.candle.events",
}

type balanceData struct {
	AccountType string `json:"account_type"`
	Asset       string `json:"asset"`
	Available   string `json:"available"`
	Frozen      string `json:"frozen"`
	EntryType   string `json:"entry_type"`
}

type notificationData struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// orderData is an order change on "orders": the fields the event carries.
// The engine's events have the fill totals; the order service's
// acceptance has the order itself.
type orderData struct {
	OrderID        string `json:"order_id"`
	ClientOrderID  string `json:"client_order_id,omitempty"`
	Symbol         string `json:"symbol"`
	Status         string `json:"status"`
	Side           string `json:"side,omitempty"`
	Type           string `json:"type,omitempty"`
	Price          string `json:"price,omitempty"`
	Quantity       string `json:"quantity,omitempty"`
	QuoteAmount    string `json:"quote_amount,omitempty"`
	FilledQuantity string `json:"filled_quantity,omitempty"`
	FilledQuote    string `json:"filled_quote,omitempty"`
	CancelReason   string `json:"cancel_reason,omitempty"`
	RejectReason   string `json:"reject_reason,omitempty"`
	Sequence       int64  `json:"sequence,omitempty"`
}

// fillData is one side of a trade on "fills".
type fillData struct {
	TradeID       string `json:"trade_id"`
	OrderID       string `json:"order_id"`
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	Role          string `json:"role"`
	Price         string `json:"price"`
	Quantity      string `json:"quantity"`
	QuoteQuantity string `json:"quote_quantity"`
	FeeAsset      string `json:"fee_asset"`
	Fee           string `json:"fee"`
	ExecutedAt    string `json:"executed_at"`
}

// tradeData is a public trade on "trades:{symbol}".
type tradeData struct {
	TradeID       string `json:"trade_id"`
	TradeNumber   uint64 `json:"trade_number"`
	Price         string `json:"price"`
	Quantity      string `json:"quantity"`
	QuoteQuantity string `json:"quote_quantity"`
	TakerSide     string `json:"taker_side"`
	ExecutedAt    string `json:"executed_at"`
}

type candleData struct {
	OpenTime    string `json:"open_time"`
	Open        string `json:"open"`
	High        string `json:"high"`
	Low         string `json:"low"`
	Close       string `json:"close"`
	Volume      string `json:"volume"`
	QuoteVolume string `json:"quote_volume"`
	TradeCount  int64  `json:"trade_count"`
	Closed      bool   `json:"closed"`
}

type tickerData struct {
	Symbol      string  `json:"symbol"`
	Last        *string `json:"last"`
	Open        *string `json:"open"`
	High        *string `json:"high"`
	Low         *string `json:"low"`
	Volume      string  `json:"volume"`
	QuoteVolume string  `json:"quote_volume"`
	TradeCount  int64   `json:"trade_count"`
	Change      *string `json:"change"`
	Bid         *string `json:"bid"`
	Ask         *string `json:"ask"`
	UpdatedAt   string  `json:"updated_at"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func sideName(s orderv1.Side) string { return strings.TrimPrefix(s.String(), "SIDE_") }

// WSEvents turns events into pushes: balance changes on "balances", new
// notifications on "notifications", order changes on "orders", fills on
// "fills", and the public trades, depth, candles and tickers.
func WSEvents(h *Hub) func(context.Context, *eventv1.Envelope) error {
	return func(_ context.Context, env *eventv1.Envelope) error {
		var (
			balance   ledgerv1.BalanceChanged
			notice    notificationv1.NotificationCreated
			accepted  orderv1.OrderAccepted
			rejected  orderv1.OrderRejected
			opened    orderv1.OrderOpened
			partial   orderv1.OrderPartiallyFilled
			filled    orderv1.OrderFilled
			canceled  orderv1.OrderCanceled
			trade     tradev1.TradeExecuted
			depth     marketv1.DepthSnapshot
			candleUp  marketv1.CandleUpdated
			candleEnd marketv1.CandleClosed
			ticker    marketv1.TickerUpdated
		)
		p := env.GetPayload()
		switch {
		case p.MessageIs(&balance):
			if err := p.UnmarshalTo(&balance); err != nil {
				return err
			}
			h.Publish(balance.GetUserId(), "balances", balanceData{
				AccountType: balance.GetAccountType(), Asset: balance.GetAsset(), Available: balance.GetAvailable(),
				Frozen: balance.GetFrozen(), EntryType: balance.GetEntryType(),
			})
		case p.MessageIs(&notice):
			if err := p.UnmarshalTo(&notice); err != nil {
				return err
			}
			h.Publish(notice.GetUserId(), "notifications", notificationData{
				ID: notice.GetNotificationId(), Type: notice.GetType(), Title: notice.GetTitle(), Body: notice.GetBody(),
			})
		case p.MessageIs(&accepted):
			if err := p.UnmarshalTo(&accepted); err != nil {
				return err
			}
			o := accepted.GetOrder()
			h.Publish(o.GetUserId(), "orders", orderData{
				OrderID: o.GetOrderId(), ClientOrderID: o.GetClientOrderId(), Symbol: o.GetSymbol(), Status: "NEW",
				Side: sideName(o.GetSide()), Type: strings.TrimPrefix(o.GetType().String(), "ORDER_TYPE_"),
				Price: o.GetPrice(), Quantity: o.GetQuantity(), QuoteAmount: o.GetQuoteAmount(),
			})
		case p.MessageIs(&rejected):
			if err := p.UnmarshalTo(&rejected); err != nil {
				return err
			}
			h.Publish(rejected.GetUserId(), "orders", orderData{
				OrderID: rejected.GetOrderId(), ClientOrderID: rejected.GetClientOrderId(), Symbol: rejected.GetSymbol(),
				Status: "REJECTED", RejectReason: rejected.GetReasonCode(), Sequence: rejected.GetSequence(),
			})
		case p.MessageIs(&opened):
			if err := p.UnmarshalTo(&opened); err != nil {
				return err
			}
			h.Publish(opened.GetUserId(), "orders", orderData{
				OrderID: opened.GetOrderId(), Symbol: opened.GetSymbol(), Status: "OPEN", Sequence: opened.GetSequence(),
			})
		case p.MessageIs(&partial):
			if err := p.UnmarshalTo(&partial); err != nil {
				return err
			}
			h.Publish(partial.GetUserId(), "orders", orderData{
				OrderID: partial.GetOrderId(), Symbol: partial.GetSymbol(), Status: "PARTIALLY_FILLED", Sequence: partial.GetSequence(),
				FilledQuantity: partial.GetFilledQuantity(), FilledQuote: partial.GetFilledQuote(),
			})
		case p.MessageIs(&filled):
			if err := p.UnmarshalTo(&filled); err != nil {
				return err
			}
			h.Publish(filled.GetUserId(), "orders", orderData{
				OrderID: filled.GetOrderId(), Symbol: filled.GetSymbol(), Status: "FILLED", Sequence: filled.GetSequence(),
				FilledQuantity: filled.GetFilledQuantity(), FilledQuote: filled.GetFilledQuote(),
			})
		case p.MessageIs(&canceled):
			if err := p.UnmarshalTo(&canceled); err != nil {
				return err
			}
			h.Publish(canceled.GetUserId(), "orders", orderData{
				OrderID: canceled.GetOrderId(), Symbol: canceled.GetSymbol(), Status: "CANCELED", Sequence: canceled.GetSequence(),
				FilledQuantity: canceled.GetFilledQuantity(), FilledQuote: canceled.GetFilledQuote(), CancelReason: canceled.GetReason(),
			})
		case p.MessageIs(&trade):
			if err := p.UnmarshalTo(&trade); err != nil {
				return err
			}
			onTrade(h, &trade, env.GetOccurredAt().AsTime())
		case p.MessageIs(&depth):
			if err := p.UnmarshalTo(&depth); err != nil {
				return err
			}
			h.OnDepth(&depth)
		case p.MessageIs(&candleUp):
			if err := p.UnmarshalTo(&candleUp); err != nil {
				return err
			}
			onCandle(h, candleUp.GetCandle(), "update")
		case p.MessageIs(&candleEnd):
			if err := p.UnmarshalTo(&candleEnd); err != nil {
				return err
			}
			onCandle(h, candleEnd.GetCandle(), "closed")
		case p.MessageIs(&ticker):
			if err := p.UnmarshalTo(&ticker); err != nil {
				return err
			}
			t := ticker.GetTicker()
			ch := "ticker:" + t.GetSymbol()
			h.OnMarket(wsMarket{Channel: ch, Data: tickerData{
				Symbol: t.GetSymbol(), Last: optional(t.GetLast()), Open: optional(t.GetOpen()), High: optional(t.GetHigh()),
				Low: optional(t.GetLow()), Volume: t.GetVolume(), QuoteVolume: t.GetQuoteVolume(), TradeCount: t.GetTradeCount(),
				Change: optional(t.GetChange()), Bid: optional(t.GetBid()), Ask: optional(t.GetAsk()),
				UpdatedAt: t.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339Nano),
			}}, true)
		}
		return nil
	}
}

// onTrade pushes a trade to both sides' "fills" and to "trades:{symbol}".
func onTrade(h *Hub, t *tradev1.TradeExecuted, at time.Time) {
	executed := at.UTC().Format(time.RFC3339Nano)
	role := func(maker bool) string {
		if maker {
			return "MAKER"
		}
		return "TAKER"
	}
	fill := fillData{
		TradeID: t.GetTradeId(), Symbol: t.GetSymbol(), Price: t.GetPrice(), Quantity: t.GetQuantity(),
		QuoteQuantity: t.GetQuoteQuantity(), ExecutedAt: executed,
	}
	buy, sell := fill, fill
	buy.OrderID, buy.Side, buy.Role, buy.FeeAsset, buy.Fee = t.GetBuyerOrderId(), "BUY", role(t.GetBuyerIsMaker()), t.GetBaseAsset(), t.GetBuyerFee()
	sell.OrderID, sell.Side, sell.Role, sell.FeeAsset, sell.Fee = t.GetSellerOrderId(), "SELL", role(!t.GetBuyerIsMaker()), t.GetQuoteAsset(), t.GetSellerFee()
	h.Publish(t.GetBuyerUserId(), "fills", buy)
	h.Publish(t.GetSellerUserId(), "fills", sell)
	ch := "trades:" + t.GetSymbol()
	h.OnMarket(wsMarket{Channel: ch, Data: tradeData{
		TradeID: t.GetTradeId(), TradeNumber: t.GetTradeNumber(), Price: t.GetPrice(), Quantity: t.GetQuantity(),
		QuoteQuantity: t.GetQuoteQuantity(), TakerSide: sideName(t.GetTakerSide()), ExecutedAt: executed,
	}}, false)
}

func onCandle(h *Hub, c *marketv1.Candle, kind string) {
	ch := "candles:" + c.GetSymbol() + ":" + c.GetInterval()
	h.OnMarket(wsMarket{Channel: ch, Type: kind, Data: candleData{
		OpenTime: c.GetOpenTime().AsTime().UTC().Format(time.RFC3339), Open: c.GetOpen(), High: c.GetHigh(), Low: c.GetLow(),
		Close: c.GetClose(), Volume: c.GetVolume(), QuoteVolume: c.GetQuoteVolume(), TradeCount: c.GetTradeCount(),
		Closed: c.GetClosed(),
	}}, true)
}
