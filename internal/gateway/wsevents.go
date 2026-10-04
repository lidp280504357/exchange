package gateway

import (
	"context"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	notificationv1 "github.com/skill/exchange/api/gen/go/exchange/notification/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/skill/exchange/api/gen/go/exchange/trade/v1"
	walletv1 "github.com/skill/exchange/api/gen/go/exchange/wallet/v1"
)

// WSTopics are the topics the hub follows from their end (requirements
// §7.3: every gateway instance reads every partition).
// The public books and trades are market-data-service's (market.depth,
// derivatives.market.depth, market.trades: the reference market's or the
// platform's, ADR-0015); trade.events only feeds the users' "fills".
var WSTopics = []string{
	"ledger.events", "notification.events", "order.events", "trade.events", "market.depth", "market.trades", "market.candle.events",
	"wallet.deposit.events", "wallet.withdrawal.events", "derivatives.market.depth",
	"derivatives.order.events", "derivatives.position.events", "derivatives.liquidation.events",
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

// fillData is one side of a trade on "fills". A contract's fill has no
// quote quantity but its position side, the part that closed a position
// and the realized profit; its fee is in the settlement asset.
type fillData struct {
	TradeID        string `json:"trade_id"`
	OrderID        string `json:"order_id"`
	Symbol         string `json:"symbol"`
	Side           string `json:"side"`
	Role           string `json:"role"`
	Price          string `json:"price"`
	Quantity       string `json:"quantity"`
	QuoteQuantity  string `json:"quote_quantity,omitempty"`
	FeeAsset       string `json:"fee_asset"`
	Fee            string `json:"fee"`
	ExecutedAt     string `json:"executed_at"`
	PositionSide   string `json:"position_side,omitempty"`
	ClosedQuantity string `json:"closed_quantity,omitempty"`
	RealizedPnL    string `json:"realized_pnl,omitempty"`
	Liquidation    bool   `json:"liquidation,omitempty"`
}

// positionData is a contract position change on "positions": event is
// OPEN, INCREASE, REDUCE, CLOSE, FLIP, MARGIN, FUNDING or LEVERAGE.
type positionData struct {
	Event        string `json:"event"`
	PositionID   string `json:"position_id,omitempty"`
	Symbol       string `json:"symbol"`
	PositionSide string `json:"position_side,omitempty"`
	Quantity     string `json:"quantity,omitempty"`
	EntryPrice   string `json:"entry_price,omitempty"`
	Margin       string `json:"margin,omitempty"`
	MarginMode   string `json:"margin_mode,omitempty"`
	Leverage     int32  `json:"leverage"`
	RealizedPnL  string `json:"realized_pnl,omitempty"`
	Funding      string `json:"funding,omitempty"`
	TradeID      string `json:"trade_id,omitempty"`
	// Amount is the margin added (MARGIN) or the funding received (FUNDING,
	// negative when paid).
	Amount string `json:"amount,omitempty"`
}

// riskData is a liquidation step on "risk": event is WARNING, STARTED,
// LIQUIDATED (a liquidation order or deleveraging closed some of it) or
// ADL (a position of the user was deleveraged against a liquidation).
type riskData struct {
	Event             string `json:"event"`
	Symbol            string `json:"symbol,omitempty"`
	PositionSide      string `json:"position_side,omitempty"`
	Cross             bool   `json:"cross,omitempty"`
	MarginBalance     string `json:"margin_balance,omitempty"`
	MaintenanceMargin string `json:"maintenance_margin,omitempty"`
	MarkPrice         string `json:"mark_price,omitempty"`
	TradeID           string `json:"trade_id,omitempty"`
	Price             string `json:"price,omitempty"`
	Quantity          string `json:"quantity,omitempty"`
	RealizedPnL       string `json:"realized_pnl,omitempty"`
}

func positionOf(p *derivativesv1.Position, ev string) positionData {
	return positionData{
		Event: ev, PositionID: p.GetPositionId(), Symbol: p.GetSymbol(), PositionSide: p.GetPositionSide(), Quantity: p.GetQuantity(),
		EntryPrice: p.GetEntryPrice(), Margin: p.GetMargin(), MarginMode: p.GetMarginMode(), Leverage: p.GetLeverage(),
		RealizedPnL: p.GetRealizedPnl(), Funding: p.GetFunding(),
	}
}

// depositData is a deposit change on "deposits"; clients reload the
// deposit list for the details.
type depositData struct {
	DepositID             string  `json:"deposit_id"`
	Asset                 *string `json:"asset"`
	Network               string  `json:"network"`
	TxHash                string  `json:"tx_hash"`
	Amount                string  `json:"amount"`
	Status                string  `json:"status"`
	Confirmations         uint32  `json:"confirmations"`
	RequiredConfirmations uint32  `json:"required_confirmations"`
	Unclaimed             bool    `json:"unclaimed"`
	Reason                *string `json:"reason"`
}

// withdrawalData is a withdrawal change on "withdrawals".
type withdrawalData struct {
	WithdrawalID          string  `json:"withdrawal_id"`
	Asset                 string  `json:"asset"`
	Amount                string  `json:"amount"`
	Status                string  `json:"status"`
	TxHash                *string `json:"tx_hash"`
	Confirmations         uint32  `json:"confirmations"`
	RequiredConfirmations uint32  `json:"required_confirmations"`
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

// markData is a contract's prices on "mark-price:{symbol}", every second.
type markData struct {
	Symbol          string `json:"symbol"`
	MarkPrice       string `json:"mark_price"`
	IndexPrice      string `json:"index_price"`
	FundingRate     string `json:"funding_rate"`
	NextFundingTime string `json:"next_funding_time"`
	UpdatedAt       string `json:"updated_at"`
}

// fundingData is on "funding:{symbol}": the running estimate (type
// "estimate") when it changes, and the rate a period settled at (type
// "settled", with the mark price) when it ends.
type fundingData struct {
	Symbol       string  `json:"symbol"`
	FundingRate  string  `json:"funding_rate"`
	InterestRate string  `json:"interest_rate"`
	FundingTime  string  `json:"funding_time"`
	MarkPrice    *string `json:"mark_price"`
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
// "fills", deposit changes on "deposits", and the public trades, depth,
// candles and tickers.
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
			depthUp   marketv1.DepthUpdate
			printed   marketv1.TradesPrinted
			candleUp  marketv1.CandleUpdated
			candleEnd marketv1.CandleClosed
			ticker    marketv1.TickerUpdated
			mark      marketv1.MarkPriceUpdated
			funding   marketv1.FundingRateUpdated
		)
		p := env.GetPayload()
		if ok, err := derivativesOf(h, p); ok {
			return err
		}
		if wd, ok := withdrawalOf(p); ok {
			h.Publish(wd.GetUserId(), "withdrawals", withdrawalData{
				WithdrawalID: wd.GetWithdrawalId(), Asset: wd.GetAsset(), Amount: wd.GetAmount(), Status: wd.GetStatus(),
				TxHash: optional(wd.GetTxHash()), Confirmations: wd.GetConfirmations(), RequiredConfirmations: wd.GetRequiredConfirmations(),
			})
			return nil
		}
		if d, ok := depositOf(p); ok {
			h.Publish(d.GetUserId(), "deposits", depositData{
				DepositID: d.GetDepositId(), Asset: optional(d.GetAsset()), Network: d.GetNetwork(), TxHash: d.GetTxHash(),
				Amount: d.GetAmount(), Status: d.GetStatus(), Confirmations: d.GetConfirmations(),
				RequiredConfirmations: d.GetRequiredConfirmations(), Unclaimed: d.GetUnclaimed(), Reason: optional(d.GetReason()),
			})
			return nil
		}
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
		case p.MessageIs(&printed):
			if err := p.UnmarshalTo(&printed); err != nil {
				return err
			}
			onPrinted(h, &printed)
		case p.MessageIs(&depth):
			if err := p.UnmarshalTo(&depth); err != nil {
				return err
			}
			h.OnDepth(&depth)
		case p.MessageIs(&depthUp):
			if err := p.UnmarshalTo(&depthUp); err != nil {
				return err
			}
			h.OnDepthUpdate(&depthUp)
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
			h.OnTicker(tickerData{
				Symbol: t.GetSymbol(), Last: optional(t.GetLast()), Open: optional(t.GetOpen()), High: optional(t.GetHigh()),
				Low: optional(t.GetLow()), Volume: t.GetVolume(), QuoteVolume: t.GetQuoteVolume(), TradeCount: t.GetTradeCount(),
				Change: optional(t.GetChange()), Bid: optional(t.GetBid()), Ask: optional(t.GetAsk()),
				UpdatedAt: t.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339Nano),
			})
		case p.MessageIs(&mark):
			if err := p.UnmarshalTo(&mark); err != nil {
				return err
			}
			h.OnMarket(wsMarket{Channel: "mark-price:" + mark.GetSymbol(), Data: markData{
				Symbol: mark.GetSymbol(), MarkPrice: mark.GetMarkPrice(), IndexPrice: mark.GetIndexPrice(),
				FundingRate: mark.GetFundingRate(), NextFundingTime: mark.GetNextFundingTime().AsTime().UTC().Format(time.RFC3339),
				UpdatedAt: mark.GetComputedAt().AsTime().UTC().Format(time.RFC3339Nano),
			}}, true)
		case p.MessageIs(&funding):
			if err := p.UnmarshalTo(&funding); err != nil {
				return err
			}
			kind := "estimate"
			if funding.GetFinal() {
				kind = "settled"
			}
			// New subscribers start from the running estimate.
			h.OnMarket(wsMarket{Channel: "funding:" + funding.GetSymbol(), Type: kind, Data: fundingData{
				Symbol: funding.GetSymbol(), FundingRate: funding.GetFundingRate(), InterestRate: funding.GetInterestRate(),
				FundingTime: funding.GetFundingTime().AsTime().UTC().Format(time.RFC3339), MarkPrice: optional(funding.GetMarkPrice()),
			}}, !funding.GetFinal())
		}
		return nil
	}
}

// depositOf returns the deposit a wallet event carries.
func depositOf(p interface {
	MessageIs(proto.Message) bool
	UnmarshalTo(proto.Message) error
},
) (*walletv1.Deposit, bool) {
	for _, m := range []interface {
		proto.Message
		GetDeposit() *walletv1.Deposit
	}{
		&walletv1.DepositDetected{}, &walletv1.DepositConfirmed{}, &walletv1.DepositCredited{},
		&walletv1.DepositOrphaned{}, &walletv1.DepositRejected{},
	} {
		if p.MessageIs(m) {
			if err := p.UnmarshalTo(m); err != nil {
				return nil, false
			}
			return m.GetDeposit(), true
		}
	}
	return nil, false
}

// derivativesOf pushes a derivatives-service event: position changes on
// "positions", settled fills on "fills". It reports whether p was one.
func derivativesOf(h *Hub, p interface {
	MessageIs(proto.Message) bool
	UnmarshalTo(proto.Message) error
},
) (bool, error) {
	var (
		opened   derivativesv1.PositionOpened
		changed  derivativesv1.PositionChanged
		closed   derivativesv1.PositionClosed
		margin   derivativesv1.MarginAdjusted
		leverage derivativesv1.LeverageChanged
		funding  derivativesv1.FundingPaid
		fill     derivativesv1.FillSettled
		warning  derivativesv1.LiquidationWarning
		started  derivativesv1.LiquidationStarted
		liquid   derivativesv1.LiquidationFilled
		adl      derivativesv1.AdlExecuted
	)
	switch {
	case p.MessageIs(&warning):
		if err := p.UnmarshalTo(&warning); err != nil {
			return true, err
		}
		h.Publish(warning.GetUserId(), "risk", riskData{
			Event: "WARNING", Symbol: warning.GetSymbol(), PositionSide: warning.GetPositionSide(), Cross: warning.GetCross(),
			MarginBalance: warning.GetMarginBalance(), MaintenanceMargin: warning.GetMaintenanceMargin(),
		})
	case p.MessageIs(&started):
		if err := p.UnmarshalTo(&started); err != nil {
			return true, err
		}
		pos := started.GetPosition()
		h.Publish(pos.GetUserId(), "risk", riskData{
			Event: "STARTED", Symbol: pos.GetSymbol(), PositionSide: pos.GetPositionSide(), Cross: started.GetCross(),
			MarginBalance: started.GetMarginBalance(), MaintenanceMargin: started.GetMaintenanceMargin(), MarkPrice: started.GetMarkPrice(),
			Quantity: pos.GetQuantity(),
		})
	case p.MessageIs(&liquid):
		if err := p.UnmarshalTo(&liquid); err != nil {
			return true, err
		}
		h.Publish(liquid.GetUserId(), "risk", riskData{
			Event: "LIQUIDATED", Symbol: liquid.GetSymbol(), PositionSide: liquid.GetPositionSide(), TradeID: liquid.GetTradeId(),
			Price: liquid.GetPrice(), Quantity: liquid.GetQuantity(), RealizedPnL: liquid.GetRealizedPnl(),
		})
	case p.MessageIs(&adl):
		if err := p.UnmarshalTo(&adl); err != nil {
			return true, err
		}
		h.Publish(adl.GetUserId(), "risk", riskData{
			Event: "ADL", Symbol: adl.GetSymbol(), PositionSide: adl.GetPositionSide(), TradeID: adl.GetTradeId(), Price: adl.GetPrice(),
			Quantity: adl.GetQuantity(), RealizedPnL: adl.GetRealizedPnl(),
		})
	case p.MessageIs(&opened):
		if err := p.UnmarshalTo(&opened); err != nil {
			return true, err
		}
		d := positionOf(opened.GetPosition(), "OPEN")
		d.TradeID = opened.GetTradeId()
		h.Publish(opened.GetPosition().GetUserId(), "positions", d)
	case p.MessageIs(&changed):
		if err := p.UnmarshalTo(&changed); err != nil {
			return true, err
		}
		d := positionOf(changed.GetPosition(), changed.GetReason())
		d.TradeID = changed.GetTradeId()
		h.Publish(changed.GetPosition().GetUserId(), "positions", d)
	case p.MessageIs(&closed):
		if err := p.UnmarshalTo(&closed); err != nil {
			return true, err
		}
		d := positionOf(closed.GetPosition(), "CLOSE")
		d.TradeID = closed.GetTradeId()
		h.Publish(closed.GetPosition().GetUserId(), "positions", d)
	case p.MessageIs(&margin):
		if err := p.UnmarshalTo(&margin); err != nil {
			return true, err
		}
		d := positionOf(margin.GetPosition(), "MARGIN")
		d.Amount = margin.GetAmount()
		h.Publish(margin.GetPosition().GetUserId(), "positions", d)
	case p.MessageIs(&funding):
		if err := p.UnmarshalTo(&funding); err != nil {
			return true, err
		}
		d := positionOf(funding.GetPosition(), "FUNDING")
		d.Amount = funding.GetAmount()
		h.Publish(funding.GetPosition().GetUserId(), "positions", d)
	case p.MessageIs(&leverage):
		if err := p.UnmarshalTo(&leverage); err != nil {
			return true, err
		}
		h.Publish(leverage.GetUserId(), "positions", positionData{Event: "LEVERAGE", Symbol: leverage.GetSymbol(), Leverage: leverage.GetLeverage()})
	case p.MessageIs(&fill):
		if err := p.UnmarshalTo(&fill); err != nil {
			return true, err
		}
		role := "TAKER"
		if fill.GetMaker() {
			role = "MAKER"
		}
		h.Publish(fill.GetUserId(), "fills", fillData{
			TradeID: fill.GetTradeId(), OrderID: fill.GetOrderId(), Symbol: fill.GetSymbol(), Side: fill.GetSide(), Role: role,
			Price: fill.GetPrice(), Quantity: fill.GetQuantity(), FeeAsset: "USDT", Fee: fill.GetFee(),
			ExecutedAt: fill.GetExecutedAt().AsTime().UTC().Format(time.RFC3339Nano), PositionSide: fill.GetPositionSide(),
			ClosedQuantity: fill.GetClosedQuantity(), RealizedPnL: fill.GetRealizedPnl(), Liquidation: fill.GetLiquidation(),
		})
	default:
		return false, nil
	}
	return true, nil
}

// withdrawalOf returns the withdrawal a wallet event carries.
func withdrawalOf(p interface {
	MessageIs(proto.Message) bool
	UnmarshalTo(proto.Message) error
},
) (*walletv1.Withdrawal, bool) {
	for _, m := range []interface {
		proto.Message
		GetWithdrawal() *walletv1.Withdrawal
	}{
		&walletv1.WithdrawalRequested{}, &walletv1.WithdrawalRiskScored{}, &walletv1.WithdrawalApproved{},
		&walletv1.WithdrawalRejected{}, &walletv1.WithdrawalCanceled{}, &walletv1.WithdrawalBroadcast{},
		&walletv1.WithdrawalConfirmed{}, &walletv1.WithdrawalFailed{}, &walletv1.WithdrawalSubmitted{},
	} {
		if p.MessageIs(m) {
			if err := p.UnmarshalTo(m); err != nil {
				return nil, false
			}
			return m.GetWithdrawal(), true
		}
	}
	return nil, false
}

// onTrade pushes a spot trade to both sides' "fills" (HOUSE's side has no
// subscriber). A contract's fills come from derivatives-service, with the
// fees and profit the engine does not know; public trades come from
// market.trades (onPrinted).
func onTrade(h *Hub, t *tradev1.TradeExecuted, at time.Time) {
	executed := at.UTC().Format(time.RFC3339Nano)
	if !contractRE.MatchString(t.GetSymbol()) {
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
	}
}

// onPrinted pushes a batch of public trades to "trades:{symbol}", one
// message a trade, oldest first.
func onPrinted(h *Hub, p *marketv1.TradesPrinted) {
	ch := "trades:" + p.GetSymbol()
	for _, t := range p.GetTrades() {
		h.OnMarket(wsMarket{Channel: ch, Data: tradeData{
			TradeID: t.GetTradeId(), TradeNumber: t.GetTradeNumber(), Price: t.GetPrice(), Quantity: t.GetQuantity(),
			QuoteQuantity: t.GetQuoteQuantity(), TakerSide: sideName(t.GetTakerSide()),
			ExecutedAt: t.GetExecutedAt().AsTime().UTC().Format(time.RFC3339Nano),
		}}, false)
	}
}

func onCandle(h *Hub, c *marketv1.Candle, kind string) {
	ch := "candles:" + c.GetSymbol() + ":" + c.GetInterval()
	h.OnMarket(wsMarket{Channel: ch, Type: kind, Data: candleData{
		OpenTime: c.GetOpenTime().AsTime().UTC().Format(time.RFC3339), Open: c.GetOpen(), High: c.GetHigh(), Low: c.GetLow(),
		Close: c.GetClose(), Volume: c.GetVolume(), QuoteVolume: c.GetQuoteVolume(), TradeCount: c.GetTradeCount(),
		Closed: c.GetClosed(),
	}}, true)
}
