package domain

import (
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

// Liquidation (requirements §11.7): a position (isolated) or an account's
// cross positions whose margin balance falls to the maintenance margin
// are taken over and closed; a warning goes out at 1.2 times it.
var (
	// WarnRatio: a margin balance at most this times the maintenance margin
	// gets a warning; ClearRatio: above this the warning is over.
	WarnRatio  = decimal.RequireFromString("1.2")
	ClearRatio = decimal.RequireFromString("1.3")
	// Slippage bounds the liquidation orders around the bankruptcy price
	// (isolated) or the mark price (cross).
	Slippage = decimal.RequireFromString("0.005")
)

// MaxLiquidationAttempts are the liquidation orders tried before the rest
// of a position is auto-deleveraged.
const MaxLiquidationAttempts = 3

// MarginState is where a margin balance stands against its maintenance
// margin.
type MarginState int

// Margin states.
const (
	MarginHealthy MarginState = iota
	MarginWarning
	MarginLiquidate
)

// State compares a margin balance with the maintenance margin.
func State(balance, maintenance decimal.Decimal) MarginState {
	switch {
	case !maintenance.IsPositive():
		return MarginHealthy
	case balance.LessThanOrEqual(maintenance):
		return MarginLiquidate
	case balance.LessThanOrEqual(maintenance.Mul(WarnRatio)):
		return MarginWarning
	}
	return MarginHealthy
}

// Recovered reports whether a warned margin balance is back above
// ClearRatio times the maintenance margin.
func Recovered(balance, maintenance decimal.Decimal) bool {
	return balance.GreaterThan(maintenance.Mul(ClearRatio))
}

// MarginBalance of an isolated position: its margin with the unrealized
// result at mark (§11.7).
func (p Position) MarginBalance(c Contract, mark decimal.Decimal) decimal.Decimal {
	return p.Margin.Add(p.UnrealizedPnL(c, mark))
}

// LiquidationAnchor is the price a liquidation closes a position around:
// an isolated position's bankruptcy price, else (cross, or an inverse
// short with no bankruptcy price) the mark price.
func LiquidationAnchor(c Contract, p Position, mark decimal.Decimal) decimal.Decimal {
	if p.MarginMode == Isolated {
		if b := p.BankruptcyPrice(c); b.IsPositive() {
			return b
		}
	}
	return mark
}

// LiquidationOrder is the liquidation engine's order for what is left of
// a position: IOC, closing it at the anchor (the bankruptcy price of an
// isolated position, the mark price for cross) moved by Slippage against
// the position, on the tick grid.
func LiquidationOrder(id string, c Contract, p Position, anchor decimal.Decimal, now time.Time) Order {
	one := decimal.NewFromInt(1)
	side, price := Sell, floorTo(anchor.Mul(one.Sub(Slippage)), c.TickSize)
	if p.Qty.IsNegative() {
		side, price = Buy, ceilTo(anchor.Mul(one.Add(Slippage)), c.TickSize)
	}
	if !price.IsPositive() {
		price = c.TickSize
	}
	return Order{
		ID: id, ClientOrderID: id, UserID: p.UserID, Symbol: p.Symbol, Side: side, PositionSide: p.Side, Type: Limit,
		TimeInForce: IOC, Price: price, Qty: p.Qty.Abs(), ReduceOnly: p.Side == SideBoth, Kind: KindLiquidation,
		Leverage: p.Leverage, MarginMode: p.MarginMode, MakerFee: c.MakerFeeRate, TakerFee: c.TakerFeeRate, LotSize: c.LotSize,
		MarginPerLot: decimal.Zero, FeePerLot: decimal.Zero, Consumed: decimal.Zero, Status: StatusNew, FreezeState: FreezeDone,
		Filled: decimal.Zero, FilledQuote: decimal.Zero, Fee: decimal.Zero, RealizedPnL: decimal.Zero, CreatedAt: now, UpdatedAt: now,
	}
}

// ADLPrice is where an auto-deleveraging closes the position: its
// liquidation anchor (the bankruptcy price when isolated, the mark price
// when cross), on the tick grid.
func ADLPrice(c Contract, p Position, mark decimal.Decimal) decimal.Decimal {
	price := LiquidationAnchor(c, p, mark)
	price = price.Div(c.TickSize).Round(0).Mul(c.TickSize)
	if !price.IsPositive() {
		return c.TickSize
	}
	return price
}

// ADLScore ranks a counterparty for auto-deleveraging (§11.7): its profit
// ratio (unrealized result / entry cost) times its effective leverage
// (notional / margin balance); the most profitable and most leveraged go
// first.
func ADLScore(c Contract, p Position, mark decimal.Decimal) decimal.Decimal {
	if p.Flat() || !p.EntryCost.IsPositive() {
		return decimal.Zero
	}
	upnl := p.UnrealizedPnL(c, mark)
	ratio := upnl.DivRound(p.EntryCost, 12)
	balance := p.Margin.Add(upnl)
	if !balance.IsPositive() {
		balance = decimal.New(1, -8)
	}
	return ratio.Mul(p.Notional(c, mark).DivRound(balance, 12))
}

// ADLQueue orders the counterparties of a position to deleverage: the
// open positions on the other side of other users that are not being
// liquidated themselves, best score first.
func ADLQueue(c Contract, p Position, positions []Position, mark decimal.Decimal) []Position {
	var out []Position
	for _, cp := range positions {
		if cp.UserID != p.UserID && !cp.Flat() && !cp.Liquidating && cp.Qty.Sign() == -p.Qty.Sign() {
			out = append(out, cp)
		}
	}
	slices.SortStableFunc(out, func(a, b Position) int { return ADLScore(c, b, mark).Cmp(ADLScore(c, a, mark)) })
	return out
}

// ADLOrder is a synthetic order of an auto-deleveraging: it closes qty of
// the position at price outside the book, without fees.
func ADLOrder(id string, c Contract, p Position, qty, price decimal.Decimal, now time.Time) Order {
	side := Sell
	if p.Qty.IsNegative() {
		side = Buy
	}
	return Order{
		ID: id, ClientOrderID: id, UserID: p.UserID, Symbol: p.Symbol, Side: side, PositionSide: p.Side, Type: Limit,
		TimeInForce: IOC, Price: price, Qty: qty, ReduceOnly: p.Side == SideBoth, Kind: KindADL, Leverage: p.Leverage,
		MarginMode: p.MarginMode, MakerFee: decimal.Zero, TakerFee: decimal.Zero, LotSize: c.LotSize, MarginPerLot: decimal.Zero,
		FeePerLot: decimal.Zero, Consumed: decimal.Zero, Released: true, Status: StatusFilled, FreezeState: FreezeDone,
		Filled: qty, FilledQuote: qty.Mul(price), Fee: decimal.Zero, RealizedPnL: decimal.Zero, CreatedAt: now, UpdatedAt: now,
	}
}
