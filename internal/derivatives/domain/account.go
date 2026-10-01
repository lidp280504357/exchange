package domain

import (
	"github.com/shopspring/decimal"
)

// remaining is what an active order may still fill.
func remaining(o Order) decimal.Decimal { return o.Qty.Sub(o.Filled) }

// CheckClosing checks that an order that only closes (reduce-only, or a
// hedge-mode order against its side) fits in what its position has left
// to close: the position's size less what the other active closing orders
// on it would close.
func CheckClosing(o Order, held map[PositionSide]Position, active []Order) error {
	pos := held[o.PositionSide]
	closable := decimal.Zero
	switch {
	case o.PositionSide != SideBoth:
		closable = pos.Qty.Abs()
	case o.Side == Buy && pos.Qty.IsNegative(), o.Side == Sell && pos.Qty.IsPositive():
		closable = pos.Qty.Abs()
	}
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
	if notional := exposure.Add(o.Qty).Mul(mark); notional.GreaterThan(limit) {
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
