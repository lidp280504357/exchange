package domain

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
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

// EntryPrice is the average price of the open quantity, 8 decimals: the
// cost / quantity of a linear contract, the harmonic mean quantity x size
// / cost of an inverse one (coin-M design §2.2).
func (p Position) EntryPrice(c Contract) decimal.Decimal {
	if p.Qty.IsZero() {
		return decimal.Zero
	}
	if c.Inverse() {
		if !p.EntryCost.IsPositive() {
			return decimal.Zero
		}
		return p.Qty.Abs().Mul(c.ContractSize).DivRound(p.EntryCost, 8)
	}
	return p.EntryCost.DivRound(p.Qty.Abs(), 8)
}

// Notional is what the open quantity is worth at mark in the settlement
// asset (Contract.Value).
func (p Position) Notional(c Contract, mark decimal.Decimal) decimal.Decimal {
	return c.Value(p.Qty.Abs(), mark)
}

// UnrealizedPnL at mark (§11.7, coin-M §2.2): for a linear contract value
// − cost for a long, cost − value for a short; an inverse contract's coin
// value falls as the price rises, so the other way round.
func (p Position) UnrealizedPnL(c Contract, mark decimal.Decimal) decimal.Decimal {
	gain := p.Notional(c, mark).Sub(p.EntryCost) // a linear long's, an inverse short's
	if p.Long() == c.Inverse() {
		return gain.Neg()
	}
	return gain
}

// MaintenanceMargin at mark (§11.7, Contract.Maintenance).
func (p Position) MaintenanceMargin(c Contract, mark decimal.Decimal) decimal.Decimal {
	return c.Maintenance(p.Notional(c, mark))
}

// BankruptcyPrice is where an isolated position's margin is used up:
// (cost − margin) / qty for a linear long, (cost + margin) / qty for a
// short; for an inverse contract quantity x size / (cost + margin) for a
// long, quantity x size / (cost − margin) for a short, which has none
// (zero) when its margin covers its whole cost: its loss in coin is bounded.
func (p Position) BankruptcyPrice(c Contract) decimal.Decimal {
	if p.Qty.IsZero() {
		return decimal.Zero
	}
	q := p.Qty.Abs()
	if c.Inverse() {
		qs := q.Mul(c.ContractSize)
		left := p.EntryCost.Add(p.Margin)
		if !p.Long() {
			left = p.EntryCost.Sub(p.Margin)
		}
		if !left.IsPositive() {
			return decimal.Zero
		}
		return qs.DivRound(left, 8)
	}
	if p.Long() {
		return decimal.Max(p.EntryCost.Sub(p.Margin), decimal.Zero).DivRound(q, 8)
	}
	return p.EntryCost.Add(p.Margin).DivRound(q, 8)
}

// LiquidationPrice estimates where an isolated position reaches its
// maintenance margin (§11.7): its margin with the result at the price
// meets the maintenance margin there. Zero when flat or when there is no
// such price (a linear long whose margin covers a fall to zero, an
// inverse short whose margin covers its whole cost).
func (p Position) LiquidationPrice(c Contract) decimal.Decimal {
	return liquidationPrice(c, p, p.Margin, decimal.Zero, c.tierOf(p.EntryCost))
}

// CrossLiquidationPrice estimates the mark price of p's contract at which
// its account's cross equity falls to the cross maintenance margin
// (§11.7), the other cross positions staying at their marks. equity is
// the cross equity at mark (CrossEquity) and others the maintenance margin
// of the other cross positions. Zero when flat, without a mark, or when
// there is no such price.
func CrossLiquidationPrice(c Contract, p Position, mark, equity, others decimal.Decimal) decimal.Decimal {
	if !mark.IsPositive() {
		return decimal.Zero
	}
	rest := equity.Sub(p.UnrealizedPnL(c, mark)) // the equity without p's result
	return liquidationPrice(c, p, rest, others, c.tierOf(p.Notional(c, mark)))
}

// liquidationPrice solves for the price x at which rest plus p's result at
// x meets others plus p's maintenance margin at x (notional x rate less
// the tier's amount a). Linear: for a long rest + q·x − cost = others +
// q·x·m − a, for a short rest + cost − q·x = others + q·x·m − a. Inverse
// (coin-M §2.2, qs = quantity x size): for a long rest + cost − qs/x =
// others + qs/x·m − a, so x = qs(1 + m) / (rest + cost − others + a); for
// a short rest + qs/x − cost = others + qs/x·m − a, so x = qs(1 − m) /
// (cost + others − a − rest). It starts in tier and moves to the tier the
// price falls into until they agree; the maintenance margin being
// continuous, they do within the ladder's length.
func liquidationPrice(c Contract, p Position, rest, others decimal.Decimal, tier int) decimal.Decimal {
	if p.Qty.IsZero() {
		return decimal.Zero
	}
	q := p.Qty.Abs()
	one := decimal.NewFromInt(1)
	price := decimal.Zero
	for range len(c.Tiers) + 1 {
		m, a := decimal.Zero, decimal.Zero
		if len(c.Tiers) > 0 {
			m, a = c.Tiers[tier].MMR, c.maintenanceAmount(tier)
		}
		var v, per decimal.Decimal
		switch {
		case c.Inverse() && p.Long():
			v, per = q.Mul(c.ContractSize).Mul(one.Add(m)), rest.Add(p.EntryCost).Sub(others).Add(a)
		case c.Inverse():
			v, per = q.Mul(c.ContractSize).Mul(one.Sub(m)), p.EntryCost.Add(others).Sub(a).Sub(rest)
		case p.Long():
			v, per = p.EntryCost.Add(others).Sub(a).Sub(rest), q.Mul(one.Sub(m))
		default:
			v, per = rest.Add(p.EntryCost).Sub(others).Add(a), q.Mul(one.Add(m))
		}
		if !v.IsPositive() || !per.IsPositive() {
			return decimal.Zero
		}
		price = v.DivRound(per, 8)
		next := c.tierOf(c.Value(q, price))
		if next == tier {
			break
		}
		tier = next
	}
	return price
}

// CrossEquity is a user's cross margin account at the marks (§11.7), as
// the margin monitor measures it: the equity is the available balance,
// what cross orders still reserve, and each cross position's margin and
// unrealized result; the maintenance margin is the cross positions'. The
// orders and positions are of contracts settled in one asset, the
// account's.
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
		c, mark := contracts[p.Symbol], marks[p.Symbol]
		equity = equity.Add(p.Margin).Add(p.UnrealizedPnL(c, mark))
		maintenance = maintenance.Add(p.MaintenanceMargin(c, mark))
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
