package application

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// AdminClose closes a user's position at the market for the admin console
// (design 2026-10-02 §4.1, force close; the console checks the role and
// audits). The closing orders resting on the position come off first:
// while any is active its cancel is requested and the call fails with
// DERIV_CLOSE_PENDING, to be repeated once the engine confirmed (the
// console does). Then a market order of kind ADMIN takes the whole
// position: reduce-only in one-way mode, against its side in hedge mode.
// clientOrderID makes the call idempotent: repeating it returns the order
// it placed. A position under liquidation is left to the liquidation
// engine (DERIV_POSITION_LIQUIDATING).
func (s *Service) AdminClose(ctx context.Context, userID, symbol string, side domain.PositionSide, clientOrderID string) (domain.Order, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return domain.Order{}, apperr.Invalid("user_id must be a UUID")
	}
	switch side {
	case domain.SideBoth, domain.SideLong, domain.SideShort:
	default:
		return domain.Order{}, apperr.Invalid("position_side must be BOTH, LONG or SHORT")
	}
	if clientOrderID != "" {
		if prev, err := s.Store.Read().Orders().ByClientID(ctx, userID, clientOrderID); err == nil {
			return prev, nil
		} else if !errors.Is(err, domain.ErrOrderNotFound) {
			return domain.Order{}, err
		}
	}
	var (
		pos     domain.Position
		pending int
	)
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		held, err := r.Positions().OfUser(ctx, userID, symbol)
		if err != nil {
			return err
		}
		pos = byside(held)[side]
		if pos.Qty.IsZero() {
			return domain.ErrNoPosition
		}
		if pos.Liquidating {
			return domain.ErrLiquidating
		}
		active, err := r.Orders().Active(ctx, userID, symbol)
		if err != nil {
			return err
		}
		for _, o := range active {
			if !o.Closing() || o.PositionSide != side {
				continue
			}
			pending++
			if o.CancelRequested {
				continue
			}
			if _, err := s.requestCancel(ctx, r, o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Order{}, err
	}
	if pending > 0 {
		return domain.Order{}, domain.ErrClosePending.WithDetail("orders", pending)
	}
	req := domain.Request{
		UserID: userID, ClientOrderID: clientOrderID, Symbol: symbol, Side: domain.Sell, PositionSide: side, Type: domain.Market,
		Qty: pos.Qty.Abs(), ReduceOnly: side == domain.SideBoth, Kind: domain.KindAdmin,
	}
	if pos.Qty.IsNegative() {
		req.Side = domain.Buy
	}
	o, err := s.Place(ctx, req)
	if err != nil {
		return domain.Order{}, err
	}
	s.Log.InfoContext(ctx, "position closed for the admin console", "user_id", userID, "symbol", symbol, "position_side", side,
		"quantity", pos.Qty.String(), "order_id", o.ID)
	return o, nil
}
