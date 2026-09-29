package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Funding round statuses.
const (
	FundingSnapshot = "SNAPSHOT"
	FundingSettled  = "SETTLED"
	FundingSkipped  = "SKIPPED"
)

// FundingRound is a contract's funding at one funding time (§11.7): the
// positions held then, and once market-data-service settled the period,
// its rate and mark price.
type FundingRound struct {
	Symbol      string
	FundingTime time.Time
	Status      string
	Rate        decimal.Decimal
	Mark        decimal.Decimal
	Positions   int
}

// FundingPayment is what a position held at a funding time pays or
// receives.
type FundingPayment struct {
	Symbol      string
	FundingTime time.Time
	PositionID  string
	UserID      string
	Side        PositionSide
	// Qty is the signed quantity at the funding time.
	Qty        decimal.Decimal
	MarginMode MarginMode
	// Amount is received (positive) or paid; Insurance is what the
	// insurance fund paid of a payment the user could not cover.
	Amount    decimal.Decimal
	Insurance decimal.Decimal
	Settled   bool
	SettledAt time.Time
	// Rate and Mark are the round's.
	Rate decimal.Decimal
	Mark decimal.Decimal
}

// FundingAmount is what a position of qty pays (negative) or receives at
// mark and rate: |qty| x mark x |rate|; longs pay shorts when the rate is
// positive, shorts pay longs when it is negative. A payer rounds up, a
// receiver down, so FUNDING_CLEARING keeps only the rounding.
func FundingAmount(qty, mark, rate decimal.Decimal, decimals int32) decimal.Decimal {
	v := qty.Abs().Mul(mark).Mul(rate.Abs())
	if (qty.IsPositive() && rate.IsPositive()) || (qty.IsNegative() && rate.IsNegative()) {
		return ceil(v, decimals).Neg()
	}
	return floor(v, decimals)
}

// FundingMove is the move of a payment against the position as it is now:
// an isolated position still open pays out of (at most) its margin and
// receives into it; a cross or closed one uses the available balance.
func FundingMove(p Position, amount decimal.Decimal) Move {
	isolated := p.MarginMode == Isolated && !p.Flat()
	if amount.IsNegative() {
		m := Move{Type: MoveFundingPay, Amount: amount.Neg()}
		if isolated {
			m.Frozen, m.Limit = true, limit(p.Margin)
		}
		return m
	}
	return Move{Type: MoveFundingReceive, Amount: amount, Frozen: isolated}
}

// ApplyFunding books a settled payment on the position: its funding total,
// and an isolated position's margin when the money came from or went to
// it. paid is what the user's balance gave or got.
func ApplyFunding(p Position, m Move, user decimal.Decimal) Position {
	p.Funding = p.Funding.Add(user)
	if m.Frozen {
		p.Margin = decimal.Max(p.Margin.Add(user), decimal.Zero)
	}
	return p
}
