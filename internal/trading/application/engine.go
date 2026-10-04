package application

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/trading/domain"
	"github.com/skill/exchange/internal/trading/ports"
)

// Consumer names spot-trading-service's consumer group.
const Consumer = "spot-trading-service"

// OnUpdate records what the engine reports about an order (§11.1 step 8):
// its status and fills, in sequence order. A finished order then releases
// what it no longer needs.
func (s *Service) OnUpdate(ctx context.Context, u domain.Update) error {
	var o domain.Order
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		o, err = r.Orders().GetForUpdate(ctx, u.OrderID)
		if errors.Is(err, domain.ErrOrderNotFound) {
			s.Log.WarnContext(ctx, "engine update for an unknown order", "order_id", u.OrderID, "sequence", u.Seq)
			return nil
		}
		if err != nil || !o.Apply(u, s.Now()) {
			return err
		}
		return r.Orders().Update(ctx, o)
	})
	if err != nil || o.ID == "" || !o.Status.Terminal() || o.Released {
		return err
	}
	return s.release(ctx, o)
}

// release unfreezes a finished order's unused funds (the key makes a
// retry harmless) and marks the order released.
func (s *Service) release(ctx context.Context, o domain.Order) error {
	if o.FreezeState != domain.FreezeDone {
		return nil // nothing was frozen
	}
	if unused := o.Unused(); unused.IsPositive() {
		if err := s.Ledger.Unfreeze(ctx, "release:"+o.ID, o.UserID, o.FrozenAsset, unused, o.ID); err != nil {
			return err
		}
	}
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Orders().GetForUpdate(ctx, o.ID)
		if err != nil || cur.Released {
			return err
		}
		cur.Released, cur.UpdatedAt = true, s.Now()
		return r.Orders().Update(ctx, cur)
	})
}

// RecoverReleases releases finished orders whose release did not complete
// (a ledger outage while the update was handled).
func (s *Service) RecoverReleases(ctx context.Context) (int, error) {
	orders, err := s.Store.Read().Orders().Unreleased(ctx, s.Now().Add(-10*time.Second), 100)
	if err != nil {
		return 0, err
	}
	for i, o := range orders {
		if err := s.release(ctx, o); err != nil {
			return i, err
		}
	}
	return len(orders), nil
}

// OnFill records one side of a trade.
func (s *Service) OnFill(ctx context.Context, f domain.Fill) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error { return r.Fills().Insert(ctx, f) })
}

// Fills returns the fills of one of the user's orders.
func (s *Service) Fills(ctx context.Context, userID, orderID string) ([]domain.Fill, error) {
	if _, err := s.Get(ctx, userID, orderID); err != nil {
		return nil, err
	}
	return s.Store.Read().Fills().OfOrder(ctx, orderID)
}

// UserFills returns a page of the user's fills, newest first, and the
// cursor of the next page ("" on the last).
func (s *Service) UserFills(ctx context.Context, userID, symbol, cursor string, limit int) ([]domain.Fill, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", apperr.Invalid("bad cursor")
		}
	}
	list, err := s.Store.Read().Fills().OfUser(ctx, userID, symbol, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	if len(list) <= limit {
		return list, "", nil
	}
	list = list[:limit]
	return list, list[limit-1].TradeID, nil
}
