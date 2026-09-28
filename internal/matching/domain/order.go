// Package domain is the matching engine's core (requirements §5.7, §11.2,
// ADR-0002): one order book per symbol, price-time priority, and a
// deterministic stream of events: the same commands always produce the
// same events. It holds no balances.
package domain

import (
	"github.com/shopspring/decimal"
)

// Side of an order.
type Side string

// Sides.
const (
	Buy  Side = "BUY"
	Sell Side = "SELL"
)

// Type of an order.
type Type string

// Types.
const (
	Limit  Type = "LIMIT"
	Market Type = "MARKET"
)

// TimeInForce of an order.
type TimeInForce string

// Times in force.
const (
	GTC      TimeInForce = "GTC"
	IOC      TimeInForce = "IOC"
	FOK      TimeInForce = "FOK"
	PostOnly TimeInForce = "POST_ONLY"
)

// STP is the self-trade prevention mode of the incoming order.
type STP string

// Self-trade prevention modes.
const (
	CancelNewest STP = "CANCEL_NEWEST"
	CancelOldest STP = "CANCEL_OLDEST"
	CancelBoth   STP = "CANCEL_BOTH"
)

// Order is an order as the engine keeps it: what was asked for, and what
// is left.
type Order struct {
	ID            string          `json:"id"`
	ClientOrderID string          `json:"client_order_id"`
	UserID        string          `json:"user_id"`
	Symbol        string          `json:"symbol"`
	Side          Side            `json:"side"`
	Type          Type            `json:"type"`
	TimeInForce   TimeInForce     `json:"time_in_force"`
	STP           STP             `json:"stp"`
	Price         decimal.Decimal `json:"price"`        // LIMIT
	Quantity      decimal.Decimal `json:"quantity"`     // LIMIT, MARKET sells
	QuoteAmount   decimal.Decimal `json:"quote_amount"` // MARKET buys
	MakerFeeRate  decimal.Decimal `json:"maker_fee_rate"`
	TakerFeeRate  decimal.Decimal `json:"taker_fee_rate"`
	BaseDecimals  int32           `json:"base_decimals"`
	QuoteDecimals int32           `json:"quote_decimals"`
	// Protection bounds a MARKET order's fills; zero is unbounded.
	Protection decimal.Decimal `json:"protection"`
	LotSize    decimal.Decimal `json:"lot_size"`
	BaseAsset  string          `json:"base_asset"`
	QuoteAsset string          `json:"quote_asset"`

	Filled      decimal.Decimal `json:"filled"`       // base
	FilledQuote decimal.Decimal `json:"filled_quote"` // quote exchanged
}

// Remaining is the base quantity still open (not for market buys, which
// are bounded by quote).
func (o *Order) Remaining() decimal.Decimal { return o.Quantity.Sub(o.Filled) }

// RemainingQuote is what a market buy may still spend.
func (o *Order) RemainingQuote() decimal.Decimal { return o.QuoteAmount.Sub(o.FilledQuote) }

// lot is the order's quantity step; orders placed before steps traveled
// with the command fall back to the base asset's smallest unit.
func (o *Order) lot() decimal.Decimal {
	if o.LotSize.IsPositive() {
		return o.LotSize
	}
	return decimal.New(1, -o.BaseDecimals)
}

// crosses reports whether a resting price is acceptable to an incoming
// order: within its limit, or within a market order's protection.
func (o *Order) crosses(resting decimal.Decimal) bool {
	switch {
	case o.Type == Limit && o.Side == Buy:
		return resting.LessThanOrEqual(o.Price)
	case o.Type == Limit:
		return resting.GreaterThanOrEqual(o.Price)
	case o.Protection.IsZero():
		return true
	case o.Side == Buy:
		return resting.LessThanOrEqual(o.Protection)
	default:
		return resting.GreaterThanOrEqual(o.Protection)
	}
}
