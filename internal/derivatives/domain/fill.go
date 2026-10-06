package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Moves of a settlement on the user's FUTURES account, as the ledger's
// SettleFutures takes them (plan §7.3 task 4).
const (
	MoveUnfreeze       = "UNFREEZE"
	MoveFreeze         = "FREEZE"
	MoveFee            = "FEE"
	MoveProfit         = "PROFIT"
	MoveLoss           = "LOSS"
	MoveFundingPay     = "FUNDING_PAY"
	MoveFundingReceive = "FUNDING_RECEIVE"
	MoveInsurance      = "INSURANCE"
)

// Entry types a PROFIT or LOSS may be booked as.
const (
	EntryRealizedPnL       = "REALIZED_PNL"
	EntryLiquidationSettle = "LIQUIDATION_SETTLE"
	EntryADLSettle         = "ADL_SETTLE"
)

// Move is one step of a settlement.
type Move struct {
	Type   string          `json:"type"`
	Amount decimal.Decimal `json:"amount"`
	// Frozen takes from or gives to the frozen balance, else the available
	// one.
	Frozen bool `json:"frozen,omitempty"`
	// Limit bounds what a FEE, LOSS or FUNDING_PAY takes from the user.
	Limit *decimal.Decimal `json:"limit,omitempty"`
	// Partial freezes what the available balance allows.
	Partial   bool   `json:"partial,omitempty"`
	EntryType string `json:"entry_type,omitempty"`
}

// Outcome is what the ledger did with a move: what the user's balance
// got (positive) or gave, what the insurance fund paid, what was waived.
type Outcome struct {
	User      decimal.Decimal
	Insurance decimal.Decimal
	Waived    decimal.Decimal
}

func limit(d decimal.Decimal) *decimal.Decimal { return &d }

// FillInput is one side of an engine trade: what the order's owner
// bought or sold.
type FillInput struct {
	TradeID    string
	Price      decimal.Decimal
	Qty        decimal.Decimal
	Maker      bool
	Seq        int64
	ExecutedAt time.Time
	// Liquidation books the fill as a liquidation: an isolated position's
	// margin left after the loss and fee goes to the insurance fund.
	Liquidation bool
	// ADL books it as an auto-deleveraging (ADL_SETTLE).
	ADL bool
}

// Fill is one side of a trade as its owner sees it.
type Fill struct {
	TradeID      string
	OrderID      string
	UserID       string
	Symbol       string
	Side         Side
	PositionSide PositionSide
	Maker        bool
	Price        decimal.Decimal
	Qty          decimal.Decimal
	// ClosedQty is the part of Qty that closed a position.
	ClosedQty   decimal.Decimal
	Fee         decimal.Decimal
	FeeWaived   decimal.Decimal
	RealizedPnL decimal.Decimal
	// Insurance is what the insurance fund paid of the loss.
	Insurance   decimal.Decimal
	Liquidation bool
	Seq         int64
	ExecutedAt  time.Time
	// Settled is false while the ledger refused the settlement.
	Settled bool
}

// Position change reasons (derivatives.position.events).
const (
	ChangeOpen     = "OPEN"
	ChangeIncrease = "INCREASE"
	ChangeReduce   = "REDUCE"
	ChangeClose    = "CLOSE"
	ChangeFlip     = "FLIP"
)

// PositionChange is a position a fill changed.
type PositionChange struct {
	Position Position
	Reason   string
}

// FillPlan is what a fill does: the moves on the user's FUTURES account,
// in order, and the new state of the order, the positions and the fill.
// Apply the ledger's outcomes before storing it.
type FillPlan struct {
	Moves     []Move
	Order     Order
	Positions []PositionChange
	Fill      Fill

	fees, losses []int
	// freeze is a partial FREEZE whose outcome becomes the margin of
	// Positions[freezeTo]; -1 when there is none.
	freeze, freezeTo int
}

// PlanFill works out a fill of order o (§11.7). held are the owner's
// positions on the contract by side (a missing side is flat). The fill
// closes what it can of the position it reduces and opens the rest:
//
//   - The fee is the fill's value (quantity x price; quantity x size /
//     price for an inverse contract) x the role's rate, rounded up. An opening order pays it out of its fee reservation for the
//     fill's lots; the unused reservation, and the margin reserved for a
//     quantity that closes rather than opens, are unfrozen.
//   - Closing takes the position's share of entry cost and margin;
//     realized profit is proceeds − cost for a long, cost − proceeds for a
//     short (the other way round for an inverse contract, whose coin value
//     falls as the price rises), booked against PNL_CLEARING. A cross position frees its
//     margin and settles profit, loss and the rest of the fee on the
//     available balance (a loss beyond it falls on the insurance fund); an
//     isolated position loses at most its margin, pays the fee out of what
//     is left and frees the rest (a liquidation gives the rest to the
//     insurance fund).
//   - Opening adds the quantity's value at its price to the entry cost:
//     the fill's value less what its closing part took, so that its two
//     sides book the same value however they split it (invariant 6); the
//     order's margin reservation for it becomes the position's margin
//     (less any fee beyond the fee reservation, as a sell may fill above
//     its limit). A closing order's rest beyond a position that shrank
//     meanwhile (it had reserved nothing) opens one with the margin the
//     available balance can give.
func PlanFill(c Contract, o Order, held map[PositionSide]Position, in FillInput) (FillPlan, error) {
	if !in.Qty.IsPositive() || !in.Price.IsPositive() || !in.Qty.Mod(o.LotSize).IsZero() {
		return FillPlan{}, fmt.Errorf("fill %s of order %s: bad quantity %s or price %s", in.TradeID, o.ID, in.Qty, in.Price)
	}
	qd := c.QuoteDecimals
	q, p := in.Qty, in.Price
	rate := o.TakerFee
	if in.Maker {
		rate = o.MakerFee
	}
	fee := ceil(c.exact(q, p).Mul(rate), qd)
	value := c.Value(q, p) // what the fill books, on both of its sides

	// Which position the fill reduces, and which one its rest opens.
	reduces, opens := o.PositionSide, o.PositionSide
	if o.PositionSide != SideBoth {
		if o.Opening() {
			reduces = ""
		} else {
			opens = opposite(o.PositionSide)
		}
	}
	qClose := decimal.Zero
	if reduces != "" {
		pos := held[reduces]
		if (o.Side == Buy && pos.Qty.IsNegative()) || (o.Side == Sell && pos.Qty.IsPositive()) {
			qClose = decimal.Min(q, pos.Qty.Abs())
		}
	}
	qOpen := q.Sub(qClose)

	marginOpen, marginClose, feeRes := decimal.Zero, decimal.Zero, decimal.Zero
	if o.Reserving() {
		marginOpen, _ = o.Reservation(qOpen)
		marginClose, _ = o.Reservation(qClose)
		_, feeRes = o.Reservation(q)
	}
	feeFromRes := decimal.Min(fee, feeRes)
	feeRest := fee.Sub(feeFromRes)
	release := feeRes.Sub(feeFromRes).Add(marginClose)

	plan := FillPlan{Order: o, freeze: -1, freezeTo: -1, Fill: Fill{
		TradeID: in.TradeID, OrderID: o.ID, UserID: o.UserID, Symbol: o.Symbol, Side: o.Side, PositionSide: o.PositionSide,
		Maker: in.Maker, Price: p, Qty: q, ClosedQty: qClose, Fee: fee, FeeWaived: decimal.Zero, RealizedPnL: decimal.Zero,
		Insurance: decimal.Zero, Liquidation: in.Liquidation, Seq: in.Seq, ExecutedAt: in.ExecutedAt, Settled: true,
	}}
	add := func(m Move) int {
		plan.Moves = append(plan.Moves, m)
		return len(plan.Moves) - 1
	}
	if feeFromRes.IsPositive() {
		plan.fees = append(plan.fees, add(Move{Type: MoveFee, Amount: feeFromRes, Frozen: true}))
	}

	changed := map[PositionSide]Position{}
	if qClose.IsPositive() {
		pos := held[reduces]
		cost := pos.share(pos.EntryCost, qClose, qd, true)
		margin := pos.share(pos.Margin, qClose, qd, false)
		proceeds := c.Value(qClose, p)
		value = value.Sub(proceeds) // what is left for the quantity that opens
		pnl := proceeds.Sub(cost)
		if pos.Qty.IsNegative() != c.Inverse() {
			pnl = cost.Sub(proceeds)
		}
		entry := EntryRealizedPnL
		switch {
		case in.ADL:
			entry = EntryADLSettle
		case in.Liquidation:
			entry = EntryLiquidationSettle
		}
		feeHere := decimal.Zero
		if qOpen.IsZero() { // with a flip the rest of the fee comes out of the new margin
			feeHere = feeRest
		}
		if pos.MarginMode == Isolated {
			if release.IsPositive() {
				add(Move{Type: MoveUnfreeze, Amount: release})
			}
			lossPaid := decimal.Zero
			if pnl.IsNegative() {
				plan.losses = append(plan.losses, add(Move{Type: MoveLoss, Amount: pnl.Neg(), Frozen: true, Limit: limit(margin), EntryType: entry}))
				lossPaid = decimal.Min(pnl.Neg(), margin)
			}
			feePaid := decimal.Zero
			if feeHere.IsPositive() {
				left := margin.Sub(lossPaid)
				plan.fees = append(plan.fees, add(Move{Type: MoveFee, Amount: feeHere, Frozen: true, Limit: limit(left)}))
				feePaid = decimal.Min(feeHere, left)
			}
			if rest := margin.Sub(lossPaid).Sub(feePaid); rest.IsPositive() {
				if in.Liquidation {
					add(Move{Type: MoveInsurance, Amount: rest, Frozen: true})
				} else {
					add(Move{Type: MoveUnfreeze, Amount: rest})
				}
			}
			if pnl.IsPositive() {
				add(Move{Type: MoveProfit, Amount: pnl, EntryType: entry})
			}
		} else {
			if r := release.Add(margin); r.IsPositive() {
				add(Move{Type: MoveUnfreeze, Amount: r})
			}
			switch {
			case pnl.IsPositive():
				add(Move{Type: MoveProfit, Amount: pnl, EntryType: entry})
			case pnl.IsNegative():
				plan.losses = append(plan.losses, add(Move{Type: MoveLoss, Amount: pnl.Neg(), EntryType: entry}))
			}
			if feeHere.IsPositive() {
				plan.fees = append(plan.fees, add(Move{Type: MoveFee, Amount: feeHere}))
			}
		}
		if pos.Qty.IsPositive() {
			pos.Qty = pos.Qty.Sub(qClose)
		} else {
			pos.Qty = pos.Qty.Add(qClose)
		}
		pos.EntryCost, pos.Margin = pos.EntryCost.Sub(cost), pos.Margin.Sub(margin)
		pos.RealizedPnL = pos.RealizedPnL.Add(pnl)
		if pos.Qty.IsZero() { // closed: the liquidation and warning are over
			pos.Liquidating, pos.LiquidationAttempts, pos.LiquidationAt, pos.WarnedAt = false, 0, time.Time{}, time.Time{}
		}
		changed[reduces] = pos
		plan.Fill.RealizedPnL = pnl
	} else if release.IsPositive() {
		add(Move{Type: MoveUnfreeze, Amount: release})
	}

	if qOpen.IsPositive() {
		pos, ok := changed[opens]
		if !ok {
			pos = held[opens]
		}
		if pos.Qty.IsZero() {
			pos.MarginMode, pos.OpenedAt = o.MarginMode, in.ExecutedAt
		}
		pos.Leverage = o.Leverage
		margin := marginOpen
		if o.Reserving() {
			if feeRest.IsPositive() {
				plan.fees = append(plan.fees, add(Move{Type: MoveFee, Amount: feeRest, Frozen: true, Limit: limit(margin)}))
				margin = margin.Sub(decimal.Min(feeRest, margin))
			}
		} else {
			if feeRest.IsPositive() {
				plan.fees = append(plan.fees, add(Move{Type: MoveFee, Amount: feeRest}))
			}
			need := ceil(c.exact(qOpen, p).Div(decimal.NewFromInt32(max(o.Leverage, 1))), qd)
			plan.freeze = add(Move{Type: MoveFreeze, Amount: need, Partial: true})
		}
		if o.Side == Buy {
			pos.Qty = pos.Qty.Add(qOpen)
		} else {
			pos.Qty = pos.Qty.Sub(qOpen)
		}
		pos.EntryCost, pos.Margin = pos.EntryCost.Add(value), pos.Margin.Add(margin)
		changed[opens] = pos
	}

	for _, side := range []PositionSide{SideBoth, SideLong, SideShort} {
		after, ok := changed[side]
		if !ok {
			continue
		}
		before := held[side]
		after.Symbol, after.UserID, after.Side = o.Symbol, o.UserID, side
		after.Fees = after.Fees.Add(fee)
		after.UpdatedAt = in.ExecutedAt
		if plan.freeze >= 0 && side == opens {
			plan.freezeTo = len(plan.Positions)
		}
		plan.Positions = append(plan.Positions, PositionChange{Position: after, Reason: changeReason(before.Qty, after.Qty)})
	}
	// The fee counts once, on the position the fill reduced if any.
	if len(plan.Positions) == 2 {
		plan.Positions[1].Position.Fees = plan.Positions[1].Position.Fees.Sub(fee)
	}
	plan.Order.Consumed = plan.Order.Consumed.Add(q)
	return plan, nil
}

func opposite(s PositionSide) PositionSide {
	if s == SideLong {
		return SideShort
	}
	return SideLong
}

func changeReason(before, after decimal.Decimal) string {
	switch {
	case before.IsZero():
		return ChangeOpen
	case after.IsZero():
		return ChangeClose
	case before.Sign() != after.Sign():
		return ChangeFlip
	case after.Abs().GreaterThan(before.Abs()):
		return ChangeIncrease
	}
	return ChangeReduce
}

// FreezeMove returns the index of a partial FREEZE in Moves whose frozen
// amount becomes the margin of Positions[target]; -1, -1 when there is
// none.
func (p FillPlan) FreezeMove() (move, target int) { return p.freeze, p.freezeTo }

// Assumed are the outcomes the plan's moves have when booked in full,
// but for a partial freeze, which freezes nothing: what a fill waiting on
// a refused settlement is stored with.
func (p FillPlan) Assumed() []Outcome {
	out := make([]Outcome, len(p.Moves))
	for i, m := range p.Moves {
		out[i] = Outcome{User: decimal.Zero, Insurance: decimal.Zero, Waived: decimal.Zero}
		if m.Partial {
			out[i].Waived = m.Amount
		}
	}
	return out
}

// Apply takes the ledger's outcomes of the plan's moves: fees it waived
// and losses the insurance fund paid go on the fill, and a partial
// freeze's frozen amount becomes the new position's margin. Then the fill
// and the order carry the fee actually charged.
func (p *FillPlan) Apply(outcomes []Outcome) error {
	if len(outcomes) != len(p.Moves) {
		return fmt.Errorf("fill %s: %d outcomes for %d moves", p.Fill.TradeID, len(outcomes), len(p.Moves))
	}
	for _, i := range p.fees {
		p.Fill.FeeWaived = p.Fill.FeeWaived.Add(outcomes[i].Waived)
	}
	for _, i := range p.losses {
		p.Fill.Insurance = p.Fill.Insurance.Add(outcomes[i].Insurance)
	}
	p.Fill.Fee = p.Fill.Fee.Sub(p.Fill.FeeWaived)
	if p.freeze >= 0 && p.freezeTo >= 0 {
		frozen := p.Moves[p.freeze].Amount.Sub(outcomes[p.freeze].Waived)
		pos := &p.Positions[p.freezeTo].Position
		pos.Margin = pos.Margin.Add(frozen)
	}
	if p.Fill.FeeWaived.IsPositive() && len(p.Positions) > 0 {
		pos := &p.Positions[0].Position
		pos.Fees = pos.Fees.Sub(p.Fill.FeeWaived)
	}
	p.Order.Fee = p.Order.Fee.Add(p.Fill.Fee)
	p.Order.RealizedPnL = p.Order.RealizedPnL.Add(p.Fill.RealizedPnL)
	return nil
}
