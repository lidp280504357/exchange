// Package application runs the spot order use cases (requirements §5.6,
// §11.1): place, cancel and read orders.
package application

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/trading/domain"
	"github.com/lidp280504357/exchange/internal/trading/ports"
)

// FeatureSpotTrade is the eligibility feature of spot orders (§5.4).
const FeatureSpotTrade = "SPOT_TRADE"

// Service places and cancels orders.
type Service struct {
	Store       ports.Store
	Ledger      ports.Ledger
	Instruments ports.Instruments
	Eligibility ports.Eligibility
	Prices      ports.Prices
	Log         *slog.Logger
	Now         func() time.Time
}

// Place checks, stores and funds a new order (§11.1 steps 3–5): it answers
// with the order NEW and funded, handed to the engine. A client_order_id
// repeated with the same order returns that order.
func (s *Service) Place(ctx context.Context, req domain.Request) (domain.Order, error) {
	if req.ClientOrderID != "" {
		if prev, err := s.Store.Read().Orders().ByClientID(ctx, req.UserID, req.ClientOrderID); err == nil {
			return s.repeat(ctx, prev, req)
		} else if !errors.Is(err, domain.ErrOrderNotFound) {
			return domain.Order{}, err
		}
	}
	pair, err := s.Instruments.Pair(ctx, req.Symbol)
	if err != nil {
		return domain.Order{}, err
	}
	allowed, reason, err := s.Eligibility.Check(ctx, req.UserID, FeatureSpotTrade, pair.Symbol)
	if err != nil {
		return domain.Order{}, err
	}
	if !allowed {
		return domain.Order{}, apperr.New(apperr.KindForbidden, reason, "spot trading is not available to this account now")
	}
	anchor, err := s.Prices.Anchor(ctx, pair.Symbol)
	if err != nil {
		return domain.Order{}, err
	}
	o, err := domain.NewOrder(uuid.Must(uuid.NewV7()).String(), req, pair, anchor, s.Now())
	if err != nil {
		return domain.Order{}, err
	}
	var prev *domain.Order
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Orders().LockUser(ctx, o.UserID); err != nil {
			return err
		}
		// Checked again under the lock: a concurrent retry may have won.
		if p, err := r.Orders().ByClientID(ctx, o.UserID, o.ClientOrderID); err == nil {
			prev = &p
			return nil
		} else if !errors.Is(err, domain.ErrOrderNotFound) {
			return err
		}
		onSymbol, total, err := r.Orders().CountActive(ctx, o.UserID, o.Symbol)
		if err != nil {
			return err
		}
		if onSymbol >= domain.MaxActivePerSymbol || total >= domain.MaxActive {
			return domain.ErrTooManyOpen.WithDetail("max_per_symbol", domain.MaxActivePerSymbol).WithDetail("max_total", domain.MaxActive)
		}
		return r.Orders().Insert(ctx, o)
	})
	if err != nil {
		return domain.Order{}, err
	}
	if prev != nil {
		return s.repeat(ctx, *prev, req)
	}
	return s.fund(ctx, o)
}

// repeat answers a request whose client_order_id names an existing order.
func (s *Service) repeat(_ context.Context, prev domain.Order, req domain.Request) (domain.Order, error) {
	if !prev.SameAs(req) {
		return domain.Order{}, domain.ErrClientIDReused.WithDetail("order_id", prev.ID)
	}
	if prev.Status == domain.StatusRejected {
		return prev, apperr.New(apperr.KindUnprocessable, prev.RejectReason, "the order was rejected").WithDetail("order_id", prev.ID)
	}
	return prev, nil
}

// fund freezes the order's funds (idempotent by order ID) and, in one
// transaction, records the freeze with OrderAccepted and the PlaceOrder
// command. A refused freeze rejects the order; when the ledger cannot be
// reached the outcome is unknown, so the order stays pending for Recover.
func (s *Service) fund(ctx context.Context, o domain.Order) (domain.Order, error) {
	err := s.Ledger.Freeze(ctx, "order:"+o.ID, o.UserID, o.FrozenAsset, o.FrozenAmount, o.ID)
	if err != nil {
		e := apperr.From(err)
		switch e.Kind {
		case apperr.KindInvalid, apperr.KindUnprocessable, apperr.KindConflict, apperr.KindForbidden:
			return s.reject(ctx, o, e)
		}
		s.Log.WarnContext(ctx, "order freeze did not complete; recovery retries it", "order_id", o.ID, "error", err)
		return o, nil
	}
	var out domain.Order
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Orders().GetForUpdate(ctx, o.ID)
		if err != nil {
			return err
		}
		out = cur
		if cur.FreezeState != domain.FreezePending {
			return nil // recovery got here first
		}
		cur.FreezeState, cur.UpdatedAt = domain.FreezeDone, s.Now()
		if err := r.Orders().Update(ctx, cur); err != nil {
			return err
		}
		out = cur
		msg := toProto(cur)
		if err := r.Emit(ctx, event.TopicOrder, &orderv1.OrderAccepted{
			Order: msg, FrozenAsset: cur.FrozenAsset, FrozenAmount: cur.FrozenAmount.String(),
		}, "symbol", cur.Symbol); err != nil {
			return err
		}
		if err := r.Emit(ctx, event.TopicOrderCommands, &orderv1.PlaceOrder{Order: msg}, "symbol", cur.Symbol); err != nil {
			return err
		}
		if cur.CancelRequested { // canceled while its freeze was pending
			return r.Emit(ctx, event.TopicOrderCommands, cancelCommand(cur), "symbol", cur.Symbol)
		}
		return nil
	})
	return out, err
}

// reject stores the order as REJECTED with OrderRejected and returns the
// ledger's refusal with the order's ID.
func (s *Service) reject(ctx context.Context, o domain.Order, cause *apperr.Error) (domain.Order, error) {
	var out domain.Order
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Orders().GetForUpdate(ctx, o.ID)
		if err != nil {
			return err
		}
		out = cur
		if cur.FreezeState != domain.FreezePending {
			return nil
		}
		cur.Status, cur.RejectReason, cur.FreezeState, cur.UpdatedAt = domain.StatusRejected, cause.Code, domain.FreezeNone, s.Now()
		if err := r.Orders().Update(ctx, cur); err != nil {
			return err
		}
		out = cur
		return r.Emit(ctx, event.TopicOrder, &orderv1.OrderRejected{
			OrderId: cur.ID, ClientOrderId: cur.ClientOrderID, UserId: cur.UserID, Symbol: cur.Symbol, ReasonCode: cause.Code,
		}, "symbol", cur.Symbol)
	})
	if err != nil {
		return domain.Order{}, err
	}
	if out.Status != domain.StatusRejected {
		return out, nil
	}
	return out, apperr.New(cause.Kind, cause.Code, cause.Message).WithDetail("order_id", out.ID)
}

// Recover finishes orders whose freeze outcome was not recorded (§11.1
// step 5): a crash or a ledger outage between storing an order and
// recording its freeze. The freeze is retried with the same key, so an
// order the ledger already froze is accepted, never frozen twice.
func (s *Service) Recover(ctx context.Context) (int, error) {
	pending, err := s.Store.Read().Orders().PendingFreeze(ctx, s.Now().Add(-10*time.Second), 100)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, o := range pending {
		if _, err := s.fund(ctx, o); err != nil && !isRejection(err) {
			return n, err
		}
		n++
	}
	return n, nil
}

func isRejection(err error) bool {
	e := apperr.From(err)
	return e.Kind != apperr.KindInternal && e.Kind != apperr.KindUnavailable
}

// Cancel asks the engine to cancel one of the user's orders (§11.2: the
// answer is asynchronous). Canceling twice is harmless.
func (s *Service) Cancel(ctx context.Context, userID, orderID string) (domain.Order, error) {
	var out domain.Order
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		o, err := r.Orders().GetForUpdate(ctx, orderID)
		if err != nil || o.UserID != userID {
			return domain.ErrOrderNotFound
		}
		switch {
		case o.Status == domain.StatusFilled:
			return domain.ErrAlreadyFilled
		case !o.Status.Active():
			return domain.ErrOrderFinished.WithDetail("status", string(o.Status))
		}
		out = o
		if o.CancelRequested {
			return nil
		}
		out, err = s.requestCancel(ctx, r, o)
		return err
	})
	return out, err
}

// CancelAll asks the engine to cancel the user's active orders, of one
// symbol when symbol is set, and returns how many it asked for.
func (s *Service) CancelAll(ctx context.Context, userID, symbol string) (int, error) {
	n := 0
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		n = 0
		active, err := r.Orders().Active(ctx, userID, symbol)
		if err != nil {
			return err
		}
		for _, o := range active {
			if o.CancelRequested {
				continue
			}
			if _, err := s.requestCancel(ctx, r, o); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// requestCancel marks the order and, once it is funded (so the engine has
// or will have it), queues CancelOrder behind its PlaceOrder.
func (s *Service) requestCancel(ctx context.Context, r ports.Repos, o domain.Order) (domain.Order, error) {
	o.CancelRequested, o.UpdatedAt = true, s.Now()
	if err := r.Orders().Update(ctx, o); err != nil {
		return o, err
	}
	if o.FreezeState != domain.FreezeDone {
		return o, nil // fund sends the cancel after the order
	}
	return o, r.Emit(ctx, event.TopicOrderCommands, cancelCommand(o), "symbol", o.Symbol)
}

// Get returns one of the user's orders.
func (s *Service) Get(ctx context.Context, userID, orderID string) (domain.Order, error) {
	o, err := s.Store.Read().Orders().Get(ctx, orderID)
	if err != nil || o.UserID != userID {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return o, nil
}

// List returns a page of the user's orders, newest first, and the cursor
// of the next page ("" on the last). status is one status, ACTIVE, or ""
// for all.
func (s *Service) List(ctx context.Context, userID, symbol, status, cursor string, limit int) ([]domain.Order, string, error) {
	f := ports.ListFilter{Symbol: symbol, Before: cursor, Limit: limit}
	switch st := domain.Status(status); {
	case status == "":
	case status == "ACTIVE":
		f.Statuses = domain.ActiveStatuses
	case st.Valid():
		f.Statuses = []domain.Status{st}
	default:
		return nil, "", apperr.Invalid("status must be ACTIVE or an order status")
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", apperr.Invalid("bad cursor")
		}
	}
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	f.Limit++ // one more tells whether a next page exists
	list, err := s.Store.Read().Orders().List(ctx, userID, f)
	if err != nil {
		return nil, "", err
	}
	if len(list) < f.Limit {
		return list, "", nil
	}
	list = list[:f.Limit-1]
	return list, list[len(list)-1].ID, nil
}

func cancelCommand(o domain.Order) *orderv1.CancelOrder {
	return &orderv1.CancelOrder{OrderId: o.ID, UserId: o.UserID, Symbol: o.Symbol}
}
