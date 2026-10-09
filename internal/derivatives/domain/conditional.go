package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Conditional order kinds, trigger prices and statuses (§5.8).
const (
	TakeProfit = "TAKE_PROFIT"
	StopLoss   = "STOP_LOSS"

	TriggerMark = "MARK"
	TriggerLast = "LAST"

	ConditionalActive    = "ACTIVE"
	ConditionalTriggered = "TRIGGERED"
	ConditionalCanceled  = "CANCELED"
	ConditionalFailed    = "FAILED"
)

// ErrTriggerImmediate is a trigger the price already reached.
var ErrTriggerImmediate = apperr.New(apperr.KindUnprocessable, "DERIV_TRIGGER_IMMEDIATE",
	"the trigger price is already reached; place an order instead")

// ErrConditionalEnded is a conditional order that is no longer active when
// something would end it or place its order: canceled by its user, ended
// with its position, triggered (review C69).
var ErrConditionalEnded = errors.New("the conditional order is no longer active")

// Conditional is a take-profit or stop-loss: when its trigger price is
// reached it places an order that only closes the position.
type Conditional struct {
	ID           string
	UserID       string
	Symbol       string
	PositionSide PositionSide
	// Side is the side of the order it places.
	Side         Side
	Kind         string
	TriggerPrice decimal.Decimal
	TriggerBy    string
	OrderType    Type
	// Price is a LIMIT order's price.
	Price decimal.Decimal
	// Qty is zero to close the whole position.
	Qty       decimal.Decimal
	Status    string
	Reason    string
	OrderID   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ConditionalRequest asks for a take-profit or stop-loss on a position.
type ConditionalRequest struct {
	UserID       string
	Symbol       string
	PositionSide PositionSide
	Kind         string
	TriggerPrice decimal.Decimal
	TriggerBy    string
	OrderType    Type
	Price        decimal.Decimal
	Qty          decimal.Decimal
}

// NewConditional checks a request against the contract and the position
// it closes, at the price its trigger follows now, and returns it ACTIVE.
// A long closes with a sell: its take-profit triggers at or above the
// trigger price, its stop-loss at or below; a short the other way round.
func NewConditional(id string, req ConditionalRequest, c Contract, pos Position, current decimal.Decimal, now time.Time) (Conditional, error) {
	if req.TriggerBy == "" {
		req.TriggerBy = TriggerMark
	}
	if req.OrderType == "" {
		req.OrderType = Market
	}
	switch {
	case req.Kind != TakeProfit && req.Kind != StopLoss:
		return Conditional{}, apperr.Invalid("kind must be TAKE_PROFIT or STOP_LOSS")
	case req.TriggerBy != TriggerMark && req.TriggerBy != TriggerLast:
		return Conditional{}, apperr.Invalid("trigger_by must be MARK or LAST")
	case req.OrderType != Market && req.OrderType != Limit:
		return Conditional{}, apperr.Invalid("order_type must be MARKET or LIMIT")
	case !req.TriggerPrice.IsPositive() || !req.TriggerPrice.Mod(c.TickSize).IsZero():
		return Conditional{}, apperr.New(apperr.KindInvalid, "INSTRUMENT_PRECISION",
			fmt.Sprintf("trigger_price must be a positive multiple of the tick size %s", c.TickSize))
	case req.OrderType == Limit && (!req.Price.IsPositive() || !req.Price.Mod(c.TickSize).IsZero()):
		return Conditional{}, apperr.New(apperr.KindInvalid, "INSTRUMENT_PRECISION",
			fmt.Sprintf("a limit order needs a price, a multiple of the tick size %s", c.TickSize))
	case req.OrderType == Market && !req.Price.IsZero():
		return Conditional{}, apperr.Invalid("a market order has no price")
	case c.Inverse() && !req.Qty.IsInteger():
		return Conditional{}, ErrContractsNotInteger
	case req.Qty.IsNegative() || (!req.Qty.IsZero() && !req.Qty.Mod(c.LotSize).IsZero()):
		return Conditional{}, apperr.New(apperr.KindInvalid, "INSTRUMENT_PRECISION",
			fmt.Sprintf("quantity must be a multiple of the lot size %s, or empty to close the whole position", c.LotSize))
	case pos.Flat():
		return Conditional{}, ErrNoPosition
	case !req.Qty.IsZero() && req.Qty.GreaterThan(pos.Qty.Abs()):
		return Conditional{}, ErrReduceOnlyRejected.WithDetail("closable", pos.Qty.Abs().String())
	}
	cd := Conditional{
		ID: id, UserID: req.UserID, Symbol: c.Symbol, PositionSide: pos.Side, Side: Sell, Kind: req.Kind,
		TriggerPrice: req.TriggerPrice, TriggerBy: req.TriggerBy, OrderType: req.OrderType, Price: req.Price, Qty: req.Qty,
		Status: ConditionalActive, CreatedAt: now, UpdatedAt: now,
	}
	if pos.Qty.IsNegative() {
		cd.Side = Buy
	}
	if current.IsPositive() && cd.Triggered(current) {
		return Conditional{}, ErrTriggerImmediate.WithDetail("price", current.String())
	}
	return cd, nil
}

// Triggered reports whether price reaches the trigger: a sell (closing a
// long) takes profit at or above it and stops the loss at or below it; a
// buy (closing a short) the other way round.
func (cd Conditional) Triggered(price decimal.Decimal) bool {
	up := (cd.Side == Sell) == (cd.Kind == TakeProfit)
	if up {
		return price.GreaterThanOrEqual(cd.TriggerPrice)
	}
	return price.LessThanOrEqual(cd.TriggerPrice)
}

// OrderRequest is the order a triggered conditional places for what is
// left of the position (the whole position, or the quantity asked for if
// less): reduce-only in one-way mode, against its side in hedge mode.
func (cd Conditional) OrderRequest(pos Position) Request {
	qty := pos.Qty.Abs()
	if !cd.Qty.IsZero() {
		qty = decimal.Min(qty, cd.Qty)
	}
	r := Request{
		UserID: cd.UserID, Symbol: cd.Symbol, Side: cd.Side, PositionSide: cd.PositionSide, Type: cd.OrderType, Qty: qty,
		ReduceOnly: cd.PositionSide == SideBoth,
	}
	if cd.OrderType == Limit {
		r.Price = cd.Price
	}
	return r
}
