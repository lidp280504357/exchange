package domain

import (
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// InitialMargin is cost / leverage, rounded up.
func InitialMargin(cost decimal.Decimal, leverage int32, decimals int32) decimal.Decimal {
	return ceil(cost.Div(decimal.NewFromInt32(max(leverage, 1))), decimals)
}

// MarginChange is a position whose margin a settings change moves: Delta
// is frozen (positive) or freed.
type MarginChange struct {
	Position Position
	Delta    decimal.Decimal
}

// ChangeLeverage checks a new leverage against the contract and the
// user's open positions on it at the mark price (§5.8: re-checked with
// positions) and returns what it does to them: each must stay within the
// risk limit of the new leverage; a cross position's margin becomes its
// initial margin at the new leverage (more is frozen, or some freed); an
// isolated position keeps its margin, which must still cover that initial
// margin.
func ChangeLeverage(c Contract, leverage int32, positions []Position, mark decimal.Decimal) ([]MarginChange, error) {
	if err := CheckLeverage(leverage, c); err != nil {
		return nil, err
	}
	limit := c.MaxNotional(leverage)
	var out []MarginChange
	for _, p := range positions {
		if p.Flat() {
			continue
		}
		if !mark.IsPositive() {
			return nil, ErrMarkUnavailable
		}
		if n := p.Notional(mark); n.GreaterThan(limit) {
			return nil, riskLimitExceeded(limit, leverage, n)
		}
		im := InitialMargin(p.EntryCost, leverage, c.QuoteDecimals)
		next := p
		next.Leverage = leverage
		delta := decimal.Zero
		if p.MarginMode == Cross {
			delta = im.Sub(p.Margin)
			next.Margin = im
		} else if p.Margin.LessThan(im) {
			return nil, ErrInsufficientMargin.WithDetail("required_margin", im.String())
		}
		out = append(out, MarginChange{Position: next, Delta: delta})
	}
	return out, nil
}

// AdjustMargin adds margin to an isolated position (a positive amount) or
// takes some away: what stays must cover the initial margin at the entry
// cost, and with the unrealized result the initial margin at the mark.
func AdjustMargin(c Contract, p Position, amount, mark decimal.Decimal) (Position, error) {
	switch {
	case p.Flat():
		return Position{}, ErrNoPosition
	case p.MarginMode != Isolated:
		return Position{}, apperr.Invalid("only an isolated position has its own margin")
	case amount.IsZero() || !amount.Equal(amount.Truncate(c.QuoteDecimals)):
		return Position{}, apperr.Invalid("amount must be non-zero with at most the settlement asset's decimals")
	}
	p.Margin = p.Margin.Add(amount)
	if amount.IsNegative() {
		if !mark.IsPositive() {
			return Position{}, ErrMarkUnavailable
		}
		atEntry := InitialMargin(p.EntryCost, p.Leverage, c.QuoteDecimals)
		atMark := InitialMargin(p.Notional(mark), p.Leverage, c.QuoteDecimals)
		if p.Margin.LessThan(atEntry) || p.Margin.Add(p.UnrealizedPnL(mark)).LessThan(atMark) {
			return Position{}, ErrMarginTooLarge
		}
	}
	return p, nil
}
