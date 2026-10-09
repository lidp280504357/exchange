package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

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

// repayGrace is how long after an order that borrowed ends its repayment
// waits (B160): its trades' settlement gives back what a limit price held
// beyond the trade prices, and the ledger settles them on its own.
// RecoverReleases repays it, past this (its cutoff is as long); the ledger
// still refuses while the order's settled trades fall short of what it
// filled (B163), and the next pass tries again. repayWait is how long that
// goes on: past it the repayment takes what the account holds then (a
// trade the ledger parked as FAILED settles only by an operator's hand).
const (
	repayGrace = 10 * time.Second
	repayWait  = time.Hour
)

// release unfreezes a finished order's unused funds (the key makes a
// retry harmless) and marks the order released. An order on a margin
// account that borrowed for its freeze first repays, once its trades had
// repayGrace to settle, what came back of the freeze, up to what it
// borrowed (B160; the ledger's key makes that once per order).
func (s *Service) release(ctx context.Context, o domain.Order) error {
	if o.FreezeState != domain.FreezeDone {
		return nil // nothing was frozen
	}
	if unused := o.Unused(); unused.IsPositive() {
		call, cancel := s.bounded(ctx)
		err := s.Ledger.Unfreeze(call, "release:"+o.ID, o.Account(), o.FrozenAsset, unused, o.ID)
		cancel()
		if err != nil {
			return err
		}
	}
	if repay := o.BorrowToRepay(); repay.IsPositive() {
		if s.Now().Sub(o.UpdatedAt) < repayGrace {
			return nil // RecoverReleases repays it, and marks it released
		}
		filled := o.FilledQuantity
		if s.Now().Sub(o.UpdatedAt) > repayWait {
			filled = decimal.Zero // no longer waits for the settlement
		}
		call, cancel := s.bounded(ctx)
		repaid, err := s.Ledger.RepayReleased(call, o.Account(), o.FrozenAsset, repay, filled, o.ID)
		cancel()
		if err != nil {
			return err
		}
		if repaid.IsPositive() {
			s.Log.InfoContext(ctx, "an order's borrow repaid as it ended", "order_id", o.ID, "asset", o.FrozenAsset,
				"repaid", repaid.String(), "up_to", repay.String())
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
// (a ledger outage while the update was handled, a repayment waiting for
// its trades' settlement). One that fails is logged and left for the next
// pass while the others go on (B163); it returns how many it released and
// the first failure.
func (s *Service) RecoverReleases(ctx context.Context) (int, error) {
	orders, err := s.Store.Read().Orders().Unreleased(ctx, s.Now().Add(-10*time.Second), 100)
	if err != nil {
		return 0, err
	}
	n, failed := 0, 0
	var first error
	for _, o := range orders {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if err := s.release(ctx, o); err != nil {
			s.Log.WarnContext(ctx, "an order's release did not complete; the next pass tries again", "order_id", o.ID, "error", err)
			failed++
			if first == nil {
				first = err
			}
			continue
		}
		n++
	}
	if first != nil {
		return n, fmt.Errorf("%d of %d releases did not complete: %w", failed, len(orders), first)
	}
	return n, nil
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
