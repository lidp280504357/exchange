package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// Consumer names derivatives-service's consumer group on the engine's
// events.
const Consumer = "derivatives-service"

// OnUpdate records what the engine reports about an order: its status
// and fill totals, in sequence order. A finished order then releases the
// reservation of what it did not fill (domain.Order.ToRelease), whether or
// not its fills were applied yet.
func (s *Service) OnUpdate(ctx context.Context, u domain.Update) error {
	var o domain.Order
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		o, err = r.Orders().GetForUpdate(ctx, u.OrderID)
		if errors.Is(err, domain.ErrOrderNotFound) {
			s.Log.WarnContext(ctx, "engine update for an unknown contract order", "order_id", u.OrderID, "sequence", u.Seq)
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

// release unfreezes a finished order's unused reservation (the key makes
// a retry harmless) and marks the order released.
func (s *Service) release(ctx context.Context, o domain.Order) error {
	if o.FreezeState != domain.FreezeDone {
		return nil // nothing was frozen
	}
	if amount := o.ToRelease(); amount.IsPositive() {
		c, err := s.Instruments.Contract(ctx, o.Symbol)
		if err != nil {
			return err
		}
		if err := s.Ledger.Unfreeze(ctx, "release:"+o.ID, o.UserID, c.Settle(), amount, o.ID); err != nil {
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

// RecoverReleases releases finished orders whose release did not
// complete (a ledger outage while the update was handled).
func (s *Service) RecoverReleases(ctx context.Context) (int, error) {
	orders, err := s.Store.Read().Orders().ToRelease(ctx, s.Now().Add(-10*time.Second), 100)
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

// Trade is a contract trade of the engine.
type Trade struct {
	ID            string
	Symbol        string
	Seq           int64
	Price         decimal.Decimal
	Qty           decimal.Decimal
	BuyerOrderID  string
	BuyerUserID   string
	SellerOrderID string
	SellerUserID  string
	BuyerIsMaker  bool
	// HouseSide is the side HOUSE took against its reference liquidity
	// (ADR-0015), "" between users: that side has no order.
	HouseSide domain.Side
	At        time.Time
}

// OnTrade applies both sides of a trade, the buyer's first. Trades come in
// the engine's order per contract and are applied one at a time.
func (s *Service) OnTrade(ctx context.Context, t Trade) error {
	s.fills.Lock()
	defer s.fills.Unlock()
	c, err := s.Instruments.Contract(ctx, t.Symbol)
	if err != nil {
		return err
	}
	if err := s.applyFill(ctx, c, t, domain.Buy, t.BuyerOrderID, t.BuyerUserID, t.BuyerIsMaker); err != nil {
		return err
	}
	if err := s.applyFill(ctx, c, t, domain.Sell, t.SellerOrderID, t.SellerUserID, !t.BuyerIsMaker); err != nil {
		return err
	}
	s.last.set(t.Symbol, t.Price)
	return nil
}

// applyFill works out one side of a trade (domain.PlanFill) from the
// stored positions and order, settles it in the ledger and stores the
// result, all under the owner's lock: a redelivery finds the side
// recorded, or, when the store failed after the ledger booked, works out
// the same plan and gets the ledger's first outcome back. A settlement
// the ledger refuses (its insurance fund is short) is parked: the fill
// and the positions are stored as if it went through, the money follows
// once RetryPending books it.
func (s *Service) applyFill(ctx context.Context, c domain.Contract, t Trade, side domain.Side, orderID, userID string, maker bool) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		if done, err := r.Fills().Has(ctx, t.ID, side); err != nil || done {
			return err
		}
		house := t.HouseSide == side
		var o domain.Order
		if house {
			o = domain.HouseOrder(c, userID, side, t.Qty)
		} else {
			var err error
			o, err = r.Orders().GetForUpdate(ctx, orderID)
			if errors.Is(err, domain.ErrOrderNotFound) {
				s.Log.ErrorContext(ctx, "contract trade of an unknown order", "trade_id", t.ID, "order_id", orderID)
				return nil
			}
			if err != nil {
				return err
			}
		}
		held, err := r.Positions().OfUser(ctx, userID, t.Symbol)
		if err != nil {
			return err
		}
		positions := byside(held)
		liquidated := positions[o.PositionSide].Liquidating
		plan, err := domain.PlanFill(c, o, positions, domain.FillInput{
			TradeID: t.ID, Price: t.Price, Qty: t.Qty, Maker: maker, Seq: t.Seq, ExecutedAt: t.At,
			Liquidation: o.Kind == domain.KindLiquidation, ADL: o.Kind == domain.KindADL,
		})
		if err != nil {
			return err
		}
		req := ports.SettleRequest{
			IdemKey: "fill:" + t.ID + ":" + string(side), UserID: userID, Asset: c.Settle(),
			Reference: fmt.Sprintf("%s trade %s", t.Symbol, t.ID), Moves: plan.Moves,
		}
		outcomes, parked, err := s.settle(ctx, req)
		if err != nil {
			return err
		}
		if parked != nil {
			outcomes = plan.Assumed()
			plan.Fill.Settled = false
		}
		if err := plan.Apply(outcomes); err != nil {
			return err
		}
		for i, ch := range plan.Positions {
			saved, err := r.Positions().Save(ctx, ch.Position)
			if err != nil {
				return err
			}
			plan.Positions[i].Position = saved
			if err := emitPosition(ctx, r, c, plan.Positions[i], t.ID); err != nil {
				return err
			}
		}
		if !house {
			if err := r.Orders().Update(ctx, plan.Order); err != nil {
				return err
			}
		}
		if err := r.Fills().Insert(ctx, plan.Fill); err != nil {
			return err
		}
		if err := r.Emit(ctx, event.TopicDerivPosition, fillProto(c, plan.Fill), "user", userID); err != nil {
			return err
		}
		if msg := liquidationEvent(c, o, plan.Fill, liquidated); msg != nil {
			if err := r.Emit(ctx, event.TopicDerivLiquidation, msg, "user", userID); err != nil {
				return err
			}
		}
		if parked != nil {
			move, target := plan.FreezeMove()
			parked.FreezeMove, parked.TradeID, parked.Side = move, t.ID, side
			if target >= 0 {
				parked.PositionID = plan.Positions[target].Position.ID
			}
			if err := r.Pending().Insert(ctx, *parked); err != nil {
				return err
			}
			s.Metrics.Parked.Inc()
			s.Log.ErrorContext(ctx, "contract settlement refused by the ledger; parked", "trade_id", t.ID, "side", side,
				"user_id", userID, "error", parked.LastError)
		}
		s.Metrics.Fills.Inc()
		return nil
	})
}

// settle books a request, or returns it to park when the ledger refuses
// it; other errors (the ledger unreachable) fail for a retry.
func (s *Service) settle(ctx context.Context, req ports.SettleRequest) ([]domain.Outcome, *ports.PendingSettlement, error) {
	if len(req.Moves) == 0 {
		return nil, nil, nil
	}
	outcomes, err := s.Ledger.Settle(ctx, req)
	if err == nil {
		return outcomes, nil, nil
	}
	if !refused(err) {
		return nil, nil, err
	}
	return nil, &ports.PendingSettlement{IdemKey: req.IdemKey, UserID: req.UserID, Request: req, FreezeMove: -1, LastError: err.Error()}, nil
}

// refused tells a business refusal from a failure worth retrying.
func refused(err error) bool {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Kind {
	case apperr.KindInvalid, apperr.KindNotFound, apperr.KindConflict, apperr.KindUnprocessable:
		return true
	}
	return false
}

// RetryPending books parked settlements again, oldest first; the ones the
// ledger still refuses stay. A partial freeze's frozen amount then joins
// its position's margin.
func (s *Service) RetryPending(ctx context.Context) (int, error) {
	due, err := s.Store.Read().Pending().Due(ctx, 50)
	if err != nil {
		return 0, err
	}
	booked := 0
	for _, p := range due {
		err := s.Store.Tx(ctx, func(r ports.Repos) error {
			if err := r.LockUser(ctx, p.UserID); err != nil {
				return err
			}
			outcomes, err := s.Ledger.Settle(ctx, p.Request)
			if err != nil {
				if refused(err) {
					return r.Pending().Failed(ctx, p.IdemKey, err.Error())
				}
				return err
			}
			if p.FreezeMove >= 0 && p.FreezeMove < len(outcomes) && p.PositionID != "" {
				frozen := p.Request.Moves[p.FreezeMove].Amount.Sub(outcomes[p.FreezeMove].Waived)
				if err := s.addMargin(ctx, r, p.UserID, p.PositionID, frozen); err != nil {
					return err
				}
			}
			if p.TradeID != "" {
				if err := r.Fills().SetSettled(ctx, p.TradeID, p.Side); err != nil {
					return err
				}
			}
			booked++
			return r.Pending().Delete(ctx, p.IdemKey)
		})
		if err != nil {
			return booked, err
		}
	}
	return booked, nil
}

// addMargin adds frozen margin to a position found by ID.
func (s *Service) addMargin(ctx context.Context, r ports.Repos, userID, positionID string, amount decimal.Decimal) error {
	if !amount.IsPositive() {
		return nil
	}
	list, err := r.Positions().OfUser(ctx, userID, "")
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.ID == positionID {
			p.Margin, p.UpdatedAt = p.Margin.Add(amount), s.Now()
			_, err := r.Positions().Save(ctx, p)
			return err
		}
	}
	return nil
}

func fillProto(c domain.Contract, f domain.Fill) *derivativesv1.FillSettled {
	return &derivativesv1.FillSettled{
		TradeId: f.TradeID, OrderId: f.OrderID, UserId: f.UserID, Symbol: f.Symbol, Side: string(f.Side),
		PositionSide: string(f.PositionSide), Maker: f.Maker, Price: f.Price.String(), Quantity: f.Qty.String(),
		ClosedQuantity: f.ClosedQty.String(), Fee: f.Fee.String(), RealizedPnl: f.RealizedPnL.String(),
		Liquidation: f.Liquidation, ExecutedAt: timestamppb.New(f.ExecutedAt), SettleAsset: c.Settle(), ContractSize: sizeOf(c),
	}
}

// Fills returns a page of the user's fills, newest first, and the cursor
// of the next page ("" on the last).
func (s *Service) Fills(ctx context.Context, userID, symbol, cursor string, limit int) ([]domain.Fill, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if cursor != "" {
		trade, side, ok := strings.Cut(cursor, ":")
		if _, err := uuid.Parse(trade); err != nil || !ok || (side != string(domain.Buy) && side != string(domain.Sell)) {
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
	last := list[limit-1]
	return list, last.TradeID + ":" + string(last.Side), nil
}
