package application

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// MaxConditionals bounds a user's active take-profit and stop-loss orders
// per contract.
const MaxConditionals = 20

// lastPrices keeps the latest trade price of each contract, the trigger
// price of conditional orders that follow the last price.
type lastPrices struct {
	mu     sync.Mutex
	prices map[string]decimal.Decimal
}

func (l *lastPrices) set(symbol string, p decimal.Decimal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.prices == nil {
		l.prices = map[string]decimal.Decimal{}
	}
	l.prices[symbol] = p
}

func (l *lastPrices) get(symbol string) (decimal.Decimal, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.prices[symbol]
	return p, ok
}

// triggerPrice is the price a conditional order follows now: the fresh
// mark price, or the last trade price (read from the fills after a
// restart); zero when there is none.
func (s *Service) triggerPrice(ctx context.Context, symbol, by string) decimal.Decimal {
	if by == domain.TriggerMark {
		if m, fresh := s.Marks.Mark(symbol); fresh {
			return m.Price
		}
		return decimal.Zero
	}
	if p, ok := s.last.get(symbol); ok {
		return p
	}
	p, err := s.Store.Read().Fills().LastPrice(ctx, symbol)
	if err != nil || !p.IsPositive() {
		return decimal.Zero
	}
	s.last.set(symbol, p)
	return p
}

// CreateConditional places a take-profit or stop-loss on one of the
// user's open positions (§5.8): it waits until its trigger price is
// reached, then places an order that only closes the position.
func (s *Service) CreateConditional(ctx context.Context, req domain.ConditionalRequest) (domain.Conditional, error) {
	c, err := s.Instruments.Contract(ctx, req.Symbol)
	if err != nil {
		return domain.Conditional{}, err
	}
	if req.TriggerBy == "" {
		req.TriggerBy = domain.TriggerMark
	}
	current := s.triggerPrice(ctx, c.Symbol, req.TriggerBy)
	if req.PositionSide == "" {
		req.PositionSide = domain.SideBoth
	}
	var out domain.Conditional
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, req.UserID); err != nil {
			return err
		}
		held, err := r.Positions().OfUser(ctx, req.UserID, c.Symbol)
		if err != nil {
			return err
		}
		pos := byside(held)[req.PositionSide]
		if pos.Liquidating {
			return domain.ErrLiquidating
		}
		active, err := r.Conditionals().OfUser(ctx, req.UserID, c.Symbol, domain.ConditionalActive, "", MaxConditionals+1)
		if err != nil {
			return err
		}
		if len(active) >= MaxConditionals {
			return domain.ErrTooManyOpen.WithDetail("max_per_symbol", MaxConditionals)
		}
		out, err = domain.NewConditional(uuid.Must(uuid.NewV7()).String(), req, c, pos, current, s.Now())
		if err != nil {
			return err
		}
		return r.Conditionals().Insert(ctx, out)
	})
	return out, err
}

// CancelConditional cancels one of the user's active conditional orders.
func (s *Service) CancelConditional(ctx context.Context, userID, id string) (domain.Conditional, error) {
	var out domain.Conditional
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cd, err := r.Conditionals().Get(ctx, id)
		if err != nil || cd.UserID != userID {
			return domain.ErrOrderNotFound
		}
		if cd.Status != domain.ConditionalActive {
			return domain.ErrOrderFinished.WithDetail("status", cd.Status)
		}
		cd.Status, cd.Reason, cd.UpdatedAt = domain.ConditionalCanceled, "USER", s.Now()
		out = cd
		return r.Conditionals().Update(ctx, cd)
	})
	return out, err
}

// Conditionals returns a page of the user's conditional orders, newest
// first, and the cursor of the next page ("" on the last).
func (s *Service) Conditionals(ctx context.Context, userID, symbol, status, cursor string, limit int) ([]domain.Conditional, string, error) {
	switch status {
	case "", domain.ConditionalActive, domain.ConditionalTriggered, domain.ConditionalCanceled, domain.ConditionalFailed:
	default:
		return nil, "", apperr.Invalid("status must be ACTIVE, TRIGGERED, CANCELED or FAILED")
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", apperr.Invalid("bad cursor")
		}
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	list, err := s.Store.Read().Conditionals().OfUser(ctx, userID, symbol, status, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	if len(list) <= limit {
		return list, "", nil
	}
	list = list[:limit]
	return list, list[limit-1].ID, nil
}

// Trigger checks the active conditional orders against their trigger
// prices, once per call (every second). A triggered one places its order
// through Place (client_order_id = its ID, so a retry after a crash finds
// the same order): TRIGGERED with the order, or FAILED with the refusal
// (e.g. the position is being liquidated). One whose position is gone,
// or turned around, is CANCELED (NO_POSITION).
func (s *Service) Trigger(ctx context.Context) (int, error) {
	active, err := s.Store.Read().Conditionals().Active(ctx, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, cd := range active {
		price := s.triggerPrice(ctx, cd.Symbol, cd.TriggerBy)
		if !price.IsPositive() || !cd.Triggered(price) {
			continue
		}
		held, err := s.Store.Read().Positions().OfUser(ctx, cd.UserID, cd.Symbol)
		if err != nil {
			return n, err
		}
		pos := byside(held)[cd.PositionSide]
		closes := (cd.Side == domain.Sell && pos.Qty.IsPositive()) || (cd.Side == domain.Buy && pos.Qty.IsNegative())
		status, reason, orderID := domain.ConditionalTriggered, "", ""
		if !closes {
			status, reason = domain.ConditionalCanceled, "NO_POSITION"
		} else {
			req := cd.OrderRequest(pos)
			req.ClientOrderID, req.Kind = cd.ID, domain.Kind(cd.Kind)
			o, err := s.Place(ctx, req)
			switch e := apperr.From(err); {
			case err == nil:
				orderID = o.ID
			case e.Kind == apperr.KindInternal || e.Kind == apperr.KindUnavailable:
				s.Log.WarnContext(ctx, "conditional order not placed; retried", "conditional_id", cd.ID, "error", err)
				continue
			default:
				status, reason = domain.ConditionalFailed, e.Code
			}
		}
		cd.Status, cd.Reason, cd.OrderID, cd.UpdatedAt = status, reason, orderID, s.Now()
		if err := s.Store.Tx(ctx, func(r ports.Repos) error { return r.Conditionals().Update(ctx, cd) }); err != nil {
			return n, err
		}
		s.Log.InfoContext(ctx, "conditional order triggered", "conditional_id", cd.ID, "kind", cd.Kind, "status", status,
			"reason", reason, "price", price.String(), "at", time.Now().UTC())
		n++
	}
	return n, nil
}
