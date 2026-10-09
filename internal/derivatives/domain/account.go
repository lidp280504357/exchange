package domain

import (
	"github.com/shopspring/decimal"
)

// remaining is what an active order may still fill.
func remaining(o Order) decimal.Decimal { return o.Qty.Sub(o.Filled) }

// closableBy is what of its position an order that only closes may close:
// the position's size, or nothing when the position is the other way.
func closableBy(o Order, held map[PositionSide]Position) decimal.Decimal {
	pos := held[o.PositionSide]
	switch {
	case o.PositionSide != SideBoth:
		return pos.Qty.Abs()
	case o.Side == Buy && pos.Qty.IsNegative(), o.Side == Sell && pos.Qty.IsPositive():
		return pos.Qty.Abs()
	}
	return decimal.Zero
}

// CheckClosing checks that an order that only closes (reduce-only, or a
// hedge-mode order against its side) fits in what its position has left
// to close: the position's size less what the other active closing orders
// on it would close.
func CheckClosing(o Order, held map[PositionSide]Position, active []Order) error {
	closable := closableBy(o, held)
	for _, a := range active {
		if a.ID != o.ID && a.Closing() && a.PositionSide == o.PositionSide && a.Side == o.Side {
			closable = closable.Sub(remaining(a))
		}
	}
	if o.Qty.GreaterThan(closable) {
		return ErrReduceOnlyRejected.WithDetail("closable", decimal.Max(closable, decimal.Zero).String())
	}
	return nil
}

// BeyondPosition returns the user's closing orders their positions have no
// room for any more (review C62): a closing order fits its position as it
// is placed (CheckClosing), but the position may shrink while it rests -
// auto-deleveraging against it, a liquidation, an order the other way
// filling first - and the engine, which knows no positions, fills it
// whole: PlanFill then opens the rest the other way. active are the
// user's active orders on one contract, oldest first: the older ones keep
// the room, the ones it no longer holds are returned. The liquidation
// engine's, ADL's and the console's orders, and those on their way out,
// are neither counted nor returned.
func BeyondPosition(held map[PositionSide]Position, active []Order) []Order {
	type way struct {
		side PositionSide
		dir  Side
	}
	room := map[way]decimal.Decimal{}
	var out []Order
	for _, o := range active {
		switch o.Kind {
		case KindUser, KindTakeProfit, KindStopLoss:
		default:
			continue
		}
		if !o.Closing() || o.CancelRequested {
			continue
		}
		w := way{o.PositionSide, o.Side}
		left, seen := room[w]
		if !seen {
			left = closableBy(o, held)
		}
		if rest := remaining(o); rest.GreaterThan(left) {
			out = append(out, o)
		} else {
			left = left.Sub(rest)
		}
		room[w] = left
	}
	return out
}

// CheckRiskLimit checks that an opening order keeps the side it adds to
// within the notional its leverage allows (§11.7 risk limit tiers), at the
// mark price: the position on that side, the active opening orders on it
// and this order.
func CheckRiskLimit(c Contract, o Order, held map[PositionSide]Position, active []Order, mark decimal.Decimal) error {
	var exposure decimal.Decimal
	if o.PositionSide == SideBoth {
		pos := held[SideBoth].Qty
		if o.Side == Sell {
			pos = pos.Neg()
		}
		exposure = decimal.Max(pos, decimal.Zero)
		for _, a := range active {
			if a.ID != o.ID && !a.Closing() && a.PositionSide == SideBoth && a.Side == o.Side {
				exposure = exposure.Add(remaining(a))
			}
		}
	} else {
		exposure = held[o.PositionSide].Qty.Abs()
		for _, a := range active {
			if a.ID != o.ID && a.Opening() && a.PositionSide == o.PositionSide {
				exposure = exposure.Add(remaining(a))
			}
		}
	}
	limit := c.MaxNotional(o.Leverage)
	if notional := c.Value(exposure.Add(o.Qty), mark); notional.GreaterThan(limit) {
		return riskLimitExceeded(limit, o.Leverage, notional)
	}
	return nil
}

// riskLimitExceeded names the cap of the leverage and the notional that
// went past it (up to the cent), so that the client can say by how much.
func riskLimitExceeded(limit decimal.Decimal, leverage int32, notional decimal.Decimal) error {
	return ErrRiskLimitExceeded.WithDetail("max_notional", limit.String()).WithDetail("leverage", leverage).
		WithDetail("notional", ceil(notional, 2).String())
}

// Summary is a user's FUTURES account at the mark prices.
type Summary struct {
	Asset     string
	Available decimal.Decimal
	Frozen    decimal.Decimal
	// OrderMargin is what open orders reserve; PositionMargin what
	// positions hold.
	OrderMargin    decimal.Decimal
	PositionMargin decimal.Decimal
	UnrealizedPnL  decimal.Decimal
	// CrossUnrealizedPnL counts the cross positions only.
	CrossUnrealizedPnL decimal.Decimal
	// Liquidating: the account's cross positions are being liquidated
	// (review C74 ④); until what that leaves has gone to the insurance
	// fund nothing may leave the account.
	Liquidating bool
}

// WalletBalance is available + frozen.
func (s Summary) WalletBalance() decimal.Decimal { return s.Available.Add(s.Frozen) }

// MarginBalance is the wallet balance with the unrealized profit and loss.
func (s Summary) MarginBalance() decimal.Decimal { return s.WalletBalance().Add(s.UnrealizedPnL) }

// Transferable is what may leave the FUTURES account now:
// min(available, available + cross unrealized PnL), at least 0. A cross
// loss the wallet covers stays; unrealized profit cannot leave.
func Transferable(available, crossUnrealized decimal.Decimal) decimal.Decimal {
	return decimal.Max(decimal.Min(available, available.Add(crossUnrealized)), decimal.Zero)
}

// FreeMargin is what new orders may reserve: the available balance less
// the cross positions' unrealized loss (profit does not count).
func FreeMargin(available, crossUnrealized decimal.Decimal) decimal.Decimal {
	return available.Add(decimal.Min(crossUnrealized, decimal.Zero))
}
