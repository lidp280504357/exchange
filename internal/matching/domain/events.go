package domain

import (
	"strconv"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Kind of an engine event.
type Kind string

// Event kinds: a trade, or a change of one order.
const (
	KindTrade           Kind = "TRADE"
	KindOpened          Kind = "OPENED"
	KindPartiallyFilled Kind = "PARTIALLY_FILLED"
	KindFilled          Kind = "FILLED"
	KindCanceled        Kind = "CANCELED"
	KindRejected        Kind = "REJECTED"
)

// Cancel reasons and reject codes (appendix C).
const (
	ReasonUser        = "USER"
	ReasonIOC         = "IOC"
	ReasonFOK         = "FOK"
	ReasonSelfTrade   = "SELF_TRADE"
	ReasonNoLiquidity = "NO_LIQUIDITY"
	RejectWouldTake   = "ORDER_WOULD_TAKE"
	RejectNoLiquidity = "ORDER_NO_LIQUIDITY"
	RejectSelfTrade   = "ORDER_SELF_TRADE"
)

// tradeNamespace seeds the trade IDs derived from symbol and sequence.
var tradeNamespace = uuid.MustParse("8b6c5c1e-5a38-4a4e-9d42-4c3f3a0e7a51")

// Event is one output of the engine, with its sequence in the symbol.
type Event struct {
	Kind   Kind
	Seq    int64
	Symbol string
	// Order events: the order and its fill state after the event.
	OrderID       string
	ClientOrderID string
	UserID        string
	Filled        decimal.Decimal
	FilledQuote   decimal.Decimal
	TradeID       string // the trade that changed the order, if any
	Reason        string // cancel reason or reject code
	// Trade events.
	Trade *Trade
}

// Trade is one fill at the maker's price.
type Trade struct {
	ID string
	// Number counts the symbol's trades from 1: a gap downstream means a
	// missing trade.
	Number       uint64
	Symbol       string
	BaseAsset    string
	QuoteAsset   string
	Seq          int64
	Price        decimal.Decimal
	Quantity     decimal.Decimal
	Quote        decimal.Decimal // Price x Quantity
	TakerSide    Side
	BuyOrderID   string
	BuyUserID    string
	SellOrderID  string
	SellUserID   string
	BuyerIsMaker bool
	BuyerFee     decimal.Decimal // base
	SellerFee    decimal.Decimal // quote
	// BuyerLimit is the buy order's limit price, zero for a market buy.
	BuyerLimit decimal.Decimal
}

// tradeID derives a trade's ID from its symbol and sequence, so a replay
// yields the same IDs.
func tradeID(symbol string, seq int64) string {
	return uuid.NewSHA1(tradeNamespace, []byte(symbol+":"+strconv.FormatInt(seq, 10))).String()
}
