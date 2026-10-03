package application

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// CrossMargin is a user's cross margin account as the margin monitor
// measures it (CrossEquity at the marks), and what a debit of its
// available balance would leave: the admin console shows it before a
// negative adjustment of the FUTURES account, which lowers the equity at
// once (C5.5 ⑧).
type CrossMargin struct {
	Asset string
	// Positions counts the cross positions; with none there is nothing
	// to liquidate.
	Positions   int
	Equity      decimal.Decimal
	Maintenance decimal.Decimal
	State       domain.MarginState
	EquityAfter decimal.Decimal
	StateAfter  domain.MarginState
	// Unmeasured is true when a cross position's contract has no fresh
	// mark price: nothing above is measured.
	Unmeasured bool
}

// CrossMargin measures a user's cross margin account, and a debit of it.
func (s *Service) CrossMargin(ctx context.Context, userID, asset string, debit decimal.Decimal) (CrossMargin, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return CrossMargin{}, apperr.Invalid("user_id must be a UUID")
	}
	if debit.IsNegative() {
		return CrossMargin{}, apperr.Invalid("the debit is not negative")
	}
	out := CrossMargin{Asset: asset}
	held, err := s.Store.Read().Positions().OfUser(ctx, userID, "")
	if err != nil {
		return CrossMargin{}, err
	}
	var cross []domain.Position
	for _, p := range held {
		if p.MarginMode == domain.Cross && !p.Qty.IsZero() {
			cross = append(cross, p)
		}
	}
	out.Positions = len(cross)
	if len(cross) == 0 {
		return out, nil
	}
	contracts, err := s.Instruments.Contracts(ctx)
	if err != nil {
		return CrossMargin{}, err
	}
	byContract := make(map[string]domain.Contract, len(contracts))
	for _, c := range contracts {
		byContract[c.Symbol] = c
	}
	marks := map[string]decimal.Decimal{}
	for _, p := range cross {
		m, fresh := s.Marks.Mark(p.Symbol)
		if !fresh {
			out.Unmeasured = true
			return out, nil
		}
		marks[p.Symbol] = m.Price
	}
	bal, err := s.Ledger.Balance(ctx, userID, asset)
	if err != nil {
		return CrossMargin{}, err
	}
	orders, err := s.Store.Read().Orders().Unreleased(ctx, userID)
	if err != nil {
		return CrossMargin{}, err
	}
	out.Equity, out.Maintenance = domain.CrossEquity(bal.Available, orders, cross, byContract, marks)
	out.State = domain.State(out.Equity, out.Maintenance)
	out.EquityAfter = out.Equity.Sub(debit)
	out.StateAfter = domain.State(out.EquityAfter, out.Maintenance)
	return out, nil
}

// ErrHouseClose refuses closing HOUSE's position from the console: its
// ADMIN order would trade with HOUSE itself (ADR-0015).
var ErrHouseClose = apperr.New(apperr.KindUnprocessable, "DERIV_HOUSE_NOT_CLOSED", "HOUSE's positions are not closed from the console")

// AdminClose closes a user's position at the market for the admin console
// (design 2026-10-02 §4.1, force close; the console checks the role and
// audits). The user's orders resting on the contract come off first, all
// of them (C5.5 ⑧: an opening order filled after the close would open the
// position again): while any is active its cancel is requested and the
// call fails with DERIV_CLOSE_PENDING, to be repeated once the engine
// confirmed (the console does). Then a market order of kind ADMIN takes
// the whole position: reduce-only in one-way mode, against its side in
// hedge mode. clientOrderID makes the call idempotent: repeating it
// returns the order it placed. A position under liquidation is left to
// the liquidation engine (DERIV_POSITION_LIQUIDATING); HOUSE's are not
// closed here (DERIV_HOUSE_NOT_CLOSED).
func (s *Service) AdminClose(ctx context.Context, userID, symbol string, side domain.PositionSide, clientOrderID string) (domain.Order, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return domain.Order{}, apperr.Invalid("user_id must be a UUID")
	}
	if s.HouseUser != "" && userID == s.HouseUser {
		return domain.Order{}, ErrHouseClose
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
