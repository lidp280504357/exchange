package application

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	derivativesv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

// PositionView is an open position at the mark price.
type PositionView struct {
	domain.Position
	// Mark is the latest mark price, zero before the first; the figures
	// below are zero without it.
	Mark              decimal.Decimal
	UnrealizedPnL     decimal.Decimal
	MaintenanceMargin decimal.Decimal
	// LiquidationPrice is the estimate of an isolated position (§11.7);
	// zero for cross positions, whose price depends on the whole account.
	LiquidationPrice decimal.Decimal
}

// Positions returns the user's open positions, of one contract when
// symbol is set.
func (s *Service) Positions(ctx context.Context, userID, symbol string) ([]PositionView, error) {
	list, err := s.Store.Read().Positions().OfUser(ctx, userID, symbol)
	if err != nil {
		return nil, err
	}
	out := []PositionView{}
	for _, p := range list {
		if p.Flat() {
			continue
		}
		c, err := s.Instruments.Contract(ctx, p.Symbol)
		if err != nil {
			return nil, err
		}
		v := PositionView{Position: p}
		if m, _ := s.Marks.Mark(p.Symbol); m.Price.IsPositive() {
			v.Mark, v.UnrealizedPnL, v.MaintenanceMargin = m.Price, p.UnrealizedPnL(m.Price), p.MaintenanceMargin(c, m.Price)
		}
		if p.MarginMode == domain.Isolated {
			v.LiquidationPrice = p.LiquidationPrice(c)
		}
		out = append(out, v)
	}
	return out, nil
}

// Settings returns the user's settings on a contract.
func (s *Service) Settings(ctx context.Context, userID, symbol string) (domain.Settings, error) {
	c, err := s.Instruments.Contract(ctx, symbol)
	if err != nil {
		return domain.Settings{}, err
	}
	return settings(ctx, s.Store.Read(), userID, c)
}

// SettingsChange asks for new settings; nil fields stay.
type SettingsChange struct {
	PositionMode *domain.PositionMode
	MarginMode   *domain.MarginMode
	Leverage     *int32
}

// UpdateSettings changes the user's settings on a contract. The position
// and margin modes change only without positions or active orders on it
// (DERIV_SETTINGS_LOCKED); a new leverage is checked against the open
// positions (domain.ChangeLeverage), and cross positions' margin follows
// it in the ledger.
func (s *Service) UpdateSettings(ctx context.Context, userID, symbol string, ch SettingsChange) (domain.Settings, error) {
	c, err := s.Instruments.Contract(ctx, symbol)
	if err != nil {
		return domain.Settings{}, err
	}
	if ch.PositionMode != nil && *ch.PositionMode != domain.OneWay && *ch.PositionMode != domain.Hedge {
		return domain.Settings{}, apperr.Invalid("position_mode must be ONE_WAY or HEDGE")
	}
	if ch.MarginMode != nil && *ch.MarginMode != domain.Cross && *ch.MarginMode != domain.Isolated {
		return domain.Settings{}, apperr.Invalid("margin_mode must be CROSS or ISOLATED")
	}
	var out domain.Settings
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		set, err := settings(ctx, r, userID, c)
		if err != nil {
			return err
		}
		positions, err := r.Positions().OfUser(ctx, userID, symbol)
		if err != nil {
			return err
		}
		active, err := r.Orders().Active(ctx, userID, symbol)
		if err != nil {
			return err
		}
		open := len(active) > 0
		for _, p := range positions {
			open = open || !p.Flat()
			if p.Liquidating {
				return domain.ErrLiquidating
			}
		}
		modeChange := (ch.PositionMode != nil && *ch.PositionMode != set.PositionMode) ||
			(ch.MarginMode != nil && *ch.MarginMode != set.MarginMode)
		if modeChange && open {
			return domain.ErrSettingsLocked
		}
		if ch.PositionMode != nil {
			set.PositionMode = *ch.PositionMode
		}
		if ch.MarginMode != nil {
			set.MarginMode = *ch.MarginMode
		}
		if ch.Leverage != nil && *ch.Leverage != set.Leverage {
			if err := s.changeLeverage(ctx, r, c, set, *ch.Leverage, positions); err != nil {
				return err
			}
			set.Leverage = *ch.Leverage
			if err := r.Emit(ctx, event.TopicDerivPosition, &derivativesv1.LeverageChanged{
				UserId: userID, Symbol: symbol, Leverage: set.Leverage,
			}, "user", userID); err != nil {
				return err
			}
		}
		set.UpdatedAt = s.Now()
		out = set
		return r.Settings().Save(ctx, set)
	})
	return out, err
}

func (s *Service) changeLeverage(ctx context.Context, r ports.Repos, c domain.Contract, set domain.Settings, leverage int32,
	positions []domain.Position,
) error {
	mark := decimal.Zero
	for _, p := range positions {
		if !p.Flat() {
			m, err := s.mark(c.Symbol)
			if err != nil {
				return err
			}
			mark = m
			break
		}
	}
	changes, err := domain.ChangeLeverage(c, leverage, positions, mark)
	if err != nil {
		return err
	}
	for _, ch := range changes {
		key := fmt.Sprintf("leverage:%s:%d", ch.Position.ID, ch.Position.Version)
		if err := s.moveMargin(ctx, set.UserID, c.Quote, key, ch.Delta); err != nil {
			return err
		}
		ch.Position.UpdatedAt = s.Now()
		saved, err := r.Positions().Save(ctx, ch.Position)
		if err != nil {
			return err
		}
		if ch.Delta.IsZero() {
			continue
		}
		if err := r.Emit(ctx, event.TopicDerivPosition, &derivativesv1.MarginAdjusted{
			Position: positionProto(saved), Amount: ch.Delta.String(),
		}, "user", set.UserID); err != nil {
			return err
		}
	}
	return nil
}

// moveMargin freezes (positive) or frees margin in the user's FUTURES
// account; a freeze the balance cannot cover fails DERIV_INSUFFICIENT_MARGIN.
func (s *Service) moveMargin(ctx context.Context, userID, asset, key string, delta decimal.Decimal) error {
	if delta.IsZero() {
		return nil
	}
	move := domain.Move{Type: domain.MoveFreeze, Amount: delta}
	if delta.IsNegative() {
		move = domain.Move{Type: domain.MoveUnfreeze, Amount: delta.Neg()}
	}
	_, err := s.Ledger.Settle(ctx, ports.SettleRequest{IdemKey: key, UserID: userID, Asset: asset, Reference: key, Moves: []domain.Move{move}})
	if err != nil && apperr.From(err).Code == "LEDGER_INSUFFICIENT_BALANCE" {
		return domain.ErrInsufficientMargin.WithDetail("required", delta.String())
	}
	return err
}

// AdjustMargin adds margin to one of the user's isolated positions
// (amount positive) or takes some away (domain.AdjustMargin).
func (s *Service) AdjustMargin(ctx context.Context, userID, symbol string, side domain.PositionSide, amount decimal.Decimal) (domain.Position, error) {
	c, err := s.Instruments.Contract(ctx, symbol)
	if err != nil {
		return domain.Position{}, err
	}
	mark := decimal.Zero
	if m, fresh := s.Marks.Mark(symbol); fresh {
		mark = m.Price
	}
	if side == "" {
		side = domain.SideBoth
	}
	var out domain.Position
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		held, err := r.Positions().OfUser(ctx, userID, symbol)
		if err != nil {
			return err
		}
		pos, ok := byside(held)[side]
		if !ok {
			return domain.ErrNoPosition
		}
		if pos.Liquidating {
			return domain.ErrLiquidating
		}
		next, err := domain.AdjustMargin(c, pos, amount, mark)
		if err != nil {
			return err
		}
		if err := s.moveMargin(ctx, userID, c.Quote, fmt.Sprintf("margin:%s:%d", pos.ID, pos.Version), amount); err != nil {
			return err
		}
		next.UpdatedAt = s.Now()
		if out, err = r.Positions().Save(ctx, next); err != nil {
			return err
		}
		return r.Emit(ctx, event.TopicDerivPosition, &derivativesv1.MarginAdjusted{
			Position: positionProto(out), Amount: amount.String(),
		}, "user", userID)
	})
	return out, err
}

// Account sums up the user's FUTURES account at the latest mark prices.
func (s *Service) Account(ctx context.Context, userID, asset string) (domain.Summary, error) {
	bal, err := s.Ledger.Balance(ctx, userID, asset)
	if err != nil {
		return domain.Summary{}, err
	}
	sum := domain.Summary{
		Asset: asset, Available: bal.Available, Frozen: bal.Frozen, OrderMargin: decimal.Zero, PositionMargin: decimal.Zero,
		UnrealizedPnL: decimal.Zero, CrossUnrealizedPnL: decimal.Zero,
	}
	r := s.Store.Read()
	positions, err := r.Positions().OfUser(ctx, userID, "")
	if err != nil {
		return domain.Summary{}, err
	}
	for _, p := range positions {
		sum.PositionMargin = sum.PositionMargin.Add(p.Margin)
		if p.Flat() {
			continue
		}
		m, _ := s.Marks.Mark(p.Symbol)
		if !m.Price.IsPositive() {
			continue
		}
		u := p.UnrealizedPnL(m.Price)
		sum.UnrealizedPnL = sum.UnrealizedPnL.Add(u)
		if p.MarginMode == domain.Cross {
			sum.CrossUnrealizedPnL = sum.CrossUnrealizedPnL.Add(u)
		}
	}
	orders, err := r.Orders().Unreleased(ctx, userID)
	if err != nil {
		return domain.Summary{}, err
	}
	for _, o := range orders {
		sum.OrderMargin = sum.OrderMargin.Add(o.Unreleased())
	}
	return sum, nil
}

// CrossUnrealizedPnL is the unrealized result of the user's cross
// positions at fresh mark prices, for the ledger's transfers out of
// FUTURES; unavailable while a cross position has no fresh mark.
func (s *Service) CrossUnrealizedPnL(ctx context.Context, userID string) (decimal.Decimal, error) {
	return s.crossUnrealized(ctx, s.Store.Read(), userID)
}

// OnDegraded puts a contract under reduce-only (risk.events
// SystemDegraded, §11.7); a person lifts it.
func (s *Service) OnDegraded(ctx context.Context, symbol, reason string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		changed, err := r.Contracts().Degrade(ctx, symbol, reason, s.Now())
		if err == nil && changed {
			s.Log.ErrorContext(ctx, "contract under reduce-only", "symbol", symbol, "reason", reason)
		}
		return err
	})
}

// LiftReduceOnly ends a contract's reduce-only; by names the person.
func (s *Service) LiftReduceOnly(ctx context.Context, symbol, by string) (bool, error) {
	var changed bool
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		changed, err = r.Contracts().Lift(ctx, symbol, by, s.Now())
		return err
	})
	if changed {
		s.Log.InfoContext(ctx, "contract reduce-only lifted", "symbol", symbol, "by", by)
	}
	return changed, err
}

// ContractStates lists the contracts' reduce-only states.
func (s *Service) ContractStates(ctx context.Context) ([]ports.ContractState, error) {
	return s.Store.Read().Contracts().All(ctx)
}
