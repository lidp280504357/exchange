package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// A cross account's liquidation (coin-margined design 2026-10-06 §0, C68,
// user decision 2026-10-09): as Binance does, what the liquidation of a
// cross account leaves goes to the insurance fund as a liquidation
// clearance fee, and the account ends at zero; a loss beyond the account
// still falls on the fund, the user never owes. An isolated position's
// liquidation already gives what is left of its margin to the fund (its
// fills' INSURANCE move). One is open per user and settlement asset at a
// time, from the take-over until its fee is booked.

// Cross liquidation statuses.
const (
	CrossLiquidationOpen = "OPEN"
	CrossLiquidationDone = "DONE"
)

// CrossLiquidation is a cross account's liquidation.
type CrossLiquidation struct {
	ID        string
	UserID    string
	Asset     string
	StartedAt time.Time
	// Equity is the account's cross equity at the marks when it was taken
	// over: the most the fee takes.
	Equity decimal.Decimal
	// Balance is what the account held then: the available balance, what
	// its cross orders reserved and its cross positions' margin.
	Balance decimal.Decimal
	// Flows is what the fills (Fill.Flow) and funding of its cross
	// positions added to that balance since, or took: what the liquidation
	// left is Balance plus Flows. Funds that come in meanwhile (a transfer
	// in, an isolated position's margin set free) are not part of it.
	Flows  decimal.Decimal
	Status string
	// Fee is the clearance fee once worked out (Valid), kept so that a
	// booking retried after a restart books the same amount.
	Fee    decimal.NullDecimal
	DoneAt time.Time
}

// Left is what the liquidation left of the account.
func (l CrossLiquidation) Left() decimal.Decimal { return l.Balance.Add(l.Flows) }

// ClearanceFee is what goes to the insurance fund once the account's cross
// positions are closed and settled: what the liquidation left of it, at
// most its equity at the take-over and what is available now, down to the
// asset's decimals; none when nothing is left.
func (l CrossLiquidation) ClearanceFee(available decimal.Decimal, decimals int32) decimal.Decimal {
	fee := floor(decimal.Min(l.Left(), l.Equity, available), decimals)
	if !fee.IsPositive() {
		return decimal.Zero
	}
	return fee
}

// Flow is what a fill added to its owner's balance or took, beyond the
// margin and reservations it moved within it: the realized result, the
// insurance fund's part of a loss, less the fee charged (Fee, once the
// ledger's outcomes are applied).
func (f Fill) Flow() decimal.Decimal { return f.RealizedPnL.Add(f.Insurance).Sub(f.Fee) }

// ParkedFlow is what the ledger's outcomes of a parked fill's moves, once
// booked, add to the flow the fill was stored with (FillPlan.Assumed: the
// insurance fund paying nothing of a loss, a partial fee waived whole): the
// fund's part of its losses, and less of its partial fees waived than
// assumed taken off (review C74 ①).
func ParkedFlow(moves []Move, outcomes []Outcome) decimal.Decimal {
	flow := decimal.Zero
	for i, m := range moves {
		if i >= len(outcomes) {
			break
		}
		switch m.Type {
		case MoveLoss:
			flow = flow.Add(outcomes[i].Insurance)
		case MoveFee:
			assumed := decimal.Zero
			if m.Partial {
				assumed = m.Amount
			}
			flow = flow.Add(outcomes[i].Waived.Sub(assumed))
		}
	}
	return flow
}
