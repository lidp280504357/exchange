package domain

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// PositionMode is one-way (one net position per contract) or hedge (a
// long and a short side, each on its own).
type PositionMode string

// Position modes.
const (
	OneWay PositionMode = "ONE_WAY"
	Hedge  PositionMode = "HEDGE"
)

// MarginMode is cross (the whole FUTURES balance backs the positions) or
// isolated (each position has its own margin, the most it can lose).
type MarginMode string

// Margin modes.
const (
	Cross    MarginMode = "CROSS"
	Isolated MarginMode = "ISOLATED"
)

// PositionSide is BOTH in one-way mode, LONG or SHORT in hedge mode.
type PositionSide string

// Position sides.
const (
	SideBoth  PositionSide = "BOTH"
	SideLong  PositionSide = "LONG"
	SideShort PositionSide = "SHORT"
)

// DefaultLeverage applies until a user picks one, capped by the contract.
const DefaultLeverage = 20

// Settings are a user's choices on a contract.
type Settings struct {
	UserID       string
	Symbol       string
	PositionMode PositionMode
	MarginMode   MarginMode
	Leverage     int32
	UpdatedAt    time.Time
}

// DefaultSettings are one-way, cross and 20x (or the contract's most).
func DefaultSettings(userID string, c Contract) Settings {
	return Settings{
		UserID: userID, Symbol: c.Symbol, PositionMode: OneWay, MarginMode: Cross,
		Leverage: min(DefaultLeverage, c.MaxLeverage()),
	}
}

// CheckLeverage checks a leverage against the contract.
func CheckLeverage(leverage int32, c Contract) error {
	if leverage < 1 || leverage > c.MaxLeverage() {
		return ErrLeverageExceeded.WithDetail("max_leverage", c.MaxLeverage())
	}
	return nil
}

// Position is a user's position on a contract, booked at its entry cost:
// EntryCost is Σ quantity x price of the open quantity, and closing takes
// its share of it, so realized profit and loss are exact and PNL_CLEARING
// + Σ long cost − Σ short cost stays 0 (invariant 6).
type Position struct {
	ID     string
	UserID string
	Symbol string
	Side   PositionSide
	// Qty is signed: positive long, negative short.
	Qty       decimal.Decimal
	EntryCost decimal.Decimal
	// Margin is what the position has frozen in the FUTURES account: its
	// isolated margin, or its initial margin in cross mode.
	Margin      decimal.Decimal
	MarginMode  MarginMode
	Leverage    int32
	RealizedPnL decimal.Decimal
	Funding     decimal.Decimal
	Fees        decimal.Decimal
	// Liquidating is set once the liquidation engine took the position
	// over, until it is closed; LiquidationAttempts counts its orders.
	Liquidating         bool
	LiquidationAttempts int
	LiquidationAt       time.Time
	// WarnedAt is set while the margin is close to the maintenance margin.
	WarnedAt  time.Time
	Version   int64
	OpenedAt  time.Time
	UpdatedAt time.Time
}

// Flat reports whether the position is closed.
func (p Position) Flat() bool { return p.Qty.IsZero() }

// Long reports whether the open quantity is long.
func (p Position) Long() bool { return p.Qty.IsPositive() }

// EntryPrice is the average price of the open quantity, 8 decimals.
func (p Position) EntryPrice() decimal.Decimal {
	if p.Qty.IsZero() {
		return decimal.Zero
	}
	return p.EntryCost.DivRound(p.Qty.Abs(), 8)
}

// Notional is |qty| x mark.
func (p Position) Notional(mark decimal.Decimal) decimal.Decimal { return p.Qty.Abs().Mul(mark) }

// UnrealizedPnL at mark (§11.7): qty x mark − cost for a long, cost − |qty|
// x mark for a short.
func (p Position) UnrealizedPnL(mark decimal.Decimal) decimal.Decimal {
	if p.Long() {
		return p.Qty.Mul(mark).Sub(p.EntryCost)
	}
	return p.EntryCost.Sub(p.Qty.Abs().Mul(mark))
}

// MaintenanceMargin at mark: notional x the tier's rate (§11.7).
func (p Position) MaintenanceMargin(c Contract, mark decimal.Decimal) decimal.Decimal {
	n := p.Notional(mark)
	return n.Mul(c.MMR(n))
}

// BankruptcyPrice is where an isolated position's margin is used up:
// (cost − margin) / qty for a long, (cost + margin) / qty for a short.
func (p Position) BankruptcyPrice() decimal.Decimal {
	if p.Qty.IsZero() {
		return decimal.Zero
	}
	q := p.Qty.Abs()
	if p.Long() {
		return decimal.Max(p.EntryCost.Sub(p.Margin), decimal.Zero).DivRound(q, 8)
	}
	return p.EntryCost.Add(p.Margin).DivRound(q, 8)
}

// LiquidationPrice estimates where an isolated position reaches its
// maintenance margin (§11.7), at the rate of its current tier: (cost −
// margin) / (qty (1 − mmr)) for a long, (cost + margin) / (qty (1 + mmr))
// for a short. Zero when flat or when a long cannot be liquidated.
func (p Position) LiquidationPrice(c Contract) decimal.Decimal {
	if p.Qty.IsZero() {
		return decimal.Zero
	}
	q := p.Qty.Abs()
	mmr := c.MMR(p.EntryCost)
	one := decimal.NewFromInt(1)
	if p.Long() {
		v := p.EntryCost.Sub(p.Margin)
		if !v.IsPositive() {
			return decimal.Zero
		}
		return v.DivRound(q.Mul(one.Sub(mmr)), 8)
	}
	return p.EntryCost.Add(p.Margin).DivRound(q.Mul(one.Add(mmr)), 8)
}

// CrossLiquidationPrice estimates the mark price of p's contract at which
// its account's cross equity falls to the cross maintenance margin
// (§11.7), the other cross positions staying at their marks. equity is
// the cross equity at mark (CrossEquity) and others the maintenance margin
// of the other cross positions. p's own maintenance rate is its tier's at
// the price found, which a few rounds settle when the price crosses a
// tier. Zero when flat, without a mark, or when there is no such price: a
// long whose equity covers a fall to zero, a short with no equity left.
func CrossLiquidationPrice(c Contract, p Position, mark, equity, others decimal.Decimal) decimal.Decimal {
	if p.Qty.IsZero() || !mark.IsPositive() {
		return decimal.Zero
	}
	q := p.Qty.Abs()
	rest := equity.Sub(p.UnrealizedPnL(mark)) // the equity without p's result
	one := decimal.NewFromInt(1)
	mmr := c.MMR(p.Notional(mark))
	price := decimal.Zero
	for range 4 {
		// A long: rest + q·x − cost = others + q·x·mmr; a short:
		// rest + cost − q·x = others + q·x·mmr.
		v, per := p.EntryCost.Add(others).Sub(rest), q.Mul(one.Sub(mmr))
		if !p.Long() {
			v, per = rest.Add(p.EntryCost).Sub(others), q.Mul(one.Add(mmr))
		}
		if !v.IsPositive() || !per.IsPositive() {
			return decimal.Zero
		}
		price = v.DivRound(per, 8)
		next := c.MMR(q.Mul(price))
		if next.Equal(mmr) {
			break
		}
		mmr = next
	}
	return price
}

// CrossEquity is a user's cross margin account at the marks (§11.7), as
// the margin monitor measures it: the equity is the available balance,
// what cross orders still reserve, and each cross position's margin and
// unrealized result; the maintenance margin is the cross positions'.
func CrossEquity(available decimal.Decimal, orders []Order, positions []Position, contracts map[string]Contract,
	marks map[string]decimal.Decimal,
) (equity, maintenance decimal.Decimal) {
	equity, maintenance = available, decimal.Zero
	for _, o := range orders {
		if o.MarginMode == Cross {
			equity = equity.Add(o.Unreleased())
		}
	}
	for _, p := range positions {
		mark := marks[p.Symbol]
		equity = equity.Add(p.Margin).Add(p.UnrealizedPnL(mark))
		maintenance = maintenance.Add(p.MaintenanceMargin(contracts[p.Symbol], mark))
	}
	return equity, maintenance
}

// share is the part of v that qty of the position's open quantity takes:
// all of it when qty closes the position, else rounded to decimals (half
// up for the cost, down for the margin), the rest staying with the
// position.
func (p Position) share(v, qty decimal.Decimal, decimals int32, roundUp bool) decimal.Decimal {
	open := p.Qty.Abs()
	if qty.GreaterThanOrEqual(open) {
		return v
	}
	s := v.Mul(qty).Div(open)
	if roundUp {
		return decimal.Min(s.Round(decimals), v)
	}
	return floor(s, decimals)
}

// ErrPositionSide is a request with a position side its mode does not
// have.
var ErrPositionSide = apperr.Invalid("position_side must be BOTH in one-way mode and LONG or SHORT in hedge mode")
