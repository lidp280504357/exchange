package application

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
)

// Place checks, stores and funds a new order: it answers with the order
// NEW, its reservation frozen and handed to the engine. A client_order_id
// repeated with the same order returns that order.
//
// Opening orders need the user to be eligible (DERIVATIVES_TRADE, which
// includes the derivatives.trading flag), stay within the risk limit of
// their leverage and fit in the free margin (the available balance less
// the cross positions' unrealized loss); orders that only close need
// neither, but must fit in what their position has left to close. A
// contract under reduce-only takes only those.
func (s *Service) Place(ctx context.Context, req domain.Request) (domain.Order, error) {
	if req.ClientOrderID != "" {
		if prev, err := s.Store.Read().Orders().ByClientID(ctx, req.UserID, req.ClientOrderID); err == nil {
			return repeat(prev, req)
		} else if !errors.Is(err, domain.ErrOrderNotFound) {
			return domain.Order{}, err
		}
	}
	c, err := s.Instruments.Contract(ctx, req.Symbol)
	if err != nil {
		return domain.Order{}, err
	}
	if slices.Contains(s.FeeFree, req.UserID) {
		c.MakerFeeRate, c.TakerFeeRate = decimal.Zero, decimal.Zero
	}
	mark, err := s.mark(c.Symbol)
	if err != nil {
		return domain.Order{}, err
	}
	var o domain.Order
	var prev *domain.Order
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, req.UserID); err != nil {
			return err
		}
		// Checked again under the lock: a concurrent retry may have won.
		if p, err := r.Orders().ByClientID(ctx, req.UserID, req.ClientOrderID); err == nil {
			prev = &p
			return nil
		} else if !errors.Is(err, domain.ErrOrderNotFound) {
			return err
		}
		set, err := settings(ctx, r, req.UserID, c)
		if err != nil {
			return err
		}
		o, err = domain.NewOrder(uuid.Must(uuid.NewV7()).String(), req, c, set, mark, s.Now())
		if err != nil {
			return err
		}
		if state, err := r.Contracts().Get(ctx, c.Symbol); err != nil {
			return err
		} else if state != nil && state.ReduceOnly && !o.Closing() {
			return domain.ErrReduceOnlyMode.WithDetail("reason", state.Reason)
		}
		onSymbol, total, err := r.Orders().CountActive(ctx, o.UserID, o.Symbol)
		if err != nil {
			return err
		}
		if onSymbol >= domain.MaxActivePerSymbol || total >= domain.MaxActive {
			return domain.ErrTooManyOpen.WithDetail("max_per_symbol", domain.MaxActivePerSymbol).WithDetail("max_total", domain.MaxActive)
		}
		held, err := r.Positions().OfUser(ctx, o.UserID, o.Symbol)
		if err != nil {
			return err
		}
		if byside(held)[o.PositionSide].Liquidating {
			return domain.ErrLiquidating
		}
		active, err := r.Orders().Active(ctx, o.UserID, o.Symbol)
		if err != nil {
			return err
		}
		if o.Closing() {
			if err := domain.CheckClosing(o, byside(held), active); err != nil {
				return err
			}
		} else if err := s.checkOpening(ctx, r, c, o, byside(held), active, mark); err != nil {
			return err
		}
		if err := r.Orders().Insert(ctx, o); err != nil {
			return err
		}
		if o.FreezeState == domain.FreezeDone { // nothing to freeze
			return s.accept(ctx, r, o, c)
		}
		return nil
	})
	if err != nil {
		return domain.Order{}, err
	}
	if prev != nil {
		return repeat(*prev, req)
	}
	if o.FreezeState == domain.FreezePending {
		return s.fund(ctx, o)
	}
	return o, nil
}

// checkOpening checks what an opening order needs.
func (s *Service) checkOpening(ctx context.Context, r ports.Repos, c domain.Contract, o domain.Order,
	held map[domain.PositionSide]domain.Position, active []domain.Order, mark decimal.Decimal,
) error {
	allowed, reason, err := s.Eligibility.Check(ctx, o.UserID, FeatureDerivatives, o.Symbol)
	if err != nil {
		return err
	}
	if !allowed {
		return apperr.New(apperr.KindForbidden, reason, "contract trading is not available to this account now")
	}
	if err := domain.CheckRiskLimit(c, o, held, active, mark); err != nil {
		return err
	}
	bal, err := s.Ledger.Balance(ctx, o.UserID, c.Quote)
	if err != nil {
		return err
	}
	cross, err := s.crossUnrealized(ctx, r, o.UserID)
	if err != nil {
		return err
	}
	need := o.Unreleased()
	if free := domain.FreeMargin(bal.Available, cross); free.LessThan(need) {
		return domain.ErrInsufficientMargin.WithDetail("required", need.String()).WithDetail("free_margin", free.String())
	}
	return nil
}

func repeat(prev domain.Order, req domain.Request) (domain.Order, error) {
	if !prev.SameAs(req) {
		return domain.Order{}, domain.ErrClientIDReused.WithDetail("order_id", prev.ID)
	}
	if prev.Status == domain.StatusRejected {
		return prev, apperr.New(apperr.KindUnprocessable, prev.RejectReason, "the order was rejected").WithDetail("order_id", prev.ID)
	}
	return prev, nil
}

// fund freezes the order's reservation (idempotent by order ID) and then
// records it with OrderAccepted and the engine's PlaceOrder. A refused
// freeze rejects the order; when the ledger cannot be reached the outcome
// is unknown and the order stays pending for Recover.
func (s *Service) fund(ctx context.Context, o domain.Order) (domain.Order, error) {
	c, err := s.Instruments.Contract(ctx, o.Symbol)
	if err != nil {
		return o, err
	}
	if err := s.Ledger.Freeze(ctx, "order:"+o.ID, o.UserID, c.Quote, o.Unreleased(), o.ID); err != nil {
		e := apperr.From(err)
		switch e.Kind {
		case apperr.KindInvalid, apperr.KindUnprocessable, apperr.KindConflict, apperr.KindForbidden:
			if e.Code == "LEDGER_INSUFFICIENT_BALANCE" {
				e = domain.ErrInsufficientMargin
			}
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
		return s.accept(ctx, r, cur, c)
	})
	return out, err
}

// accept queues OrderAccepted and the engine's PlaceOrder (and a cancel
// asked for while the freeze was pending).
func (s *Service) accept(ctx context.Context, r ports.Repos, o domain.Order, c domain.Contract) error {
	shown := toProto(o, c)
	shown.MakerFeeRate, shown.TakerFeeRate = o.MakerFee.String(), o.TakerFee.String()
	if o.Type == domain.Market {
		shown.Type = orderv1.OrderType_ORDER_TYPE_MARKET
	}
	if err := r.Emit(ctx, event.TopicDerivOrder, &orderv1.OrderAccepted{
		Order: shown, FrozenAsset: c.Quote, FrozenAmount: o.Unreleased().String(),
	}, "symbol", o.Symbol); err != nil {
		return err
	}
	if err := r.Emit(ctx, event.TopicDerivOrderCommands, &orderv1.PlaceOrder{Order: toProto(o, c), HouseOnly: s.houseOnly(c)}, "symbol", o.Symbol); err != nil {
		return err
	}
	if o.CancelRequested {
		return r.Emit(ctx, event.TopicDerivOrderCommands, cancelCommand(o), "symbol", o.Symbol)
	}
	return nil
}

// houseOnly decides whether an order of the contract trades only with
// HOUSE's reference liquidity (ADR-0015): its index pair follows a
// reference market, market.house_liquidity is on for the contract and
// market.internal_matching off.
func (s *Service) houseOnly(c domain.Contract) bool {
	if s.Features == nil || !c.Followed {
		return false
	}
	subject := flags.Subject{Symbol: c.Symbol}
	return s.Features.Enabled(flags.KeyHouseLiquidity, subject) && !s.Features.Enabled(flags.KeyInternalMatching, subject)
}

// reject stores the order as REJECTED with OrderRejected and returns the
// refusal with the order's ID.
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
		return r.Emit(ctx, event.TopicDerivOrder, &orderv1.OrderRejected{
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

// Recover finishes orders whose freeze outcome was not recorded: the
// freeze is retried with the same key, so an order the ledger already
// froze is accepted, never frozen twice.
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

// Cancel asks the engine to cancel one of the user's orders; the answer
// comes as an order event. Canceling twice is harmless.
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
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
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

// requestCancel marks the order and, once it went to the engine, queues
// CancelOrder behind its PlaceOrder.
func (s *Service) requestCancel(ctx context.Context, r ports.Repos, o domain.Order) (domain.Order, error) {
	o.CancelRequested, o.UpdatedAt = true, s.Now()
	if err := r.Orders().Update(ctx, o); err != nil {
		return o, err
	}
	if o.FreezeState != domain.FreezeDone {
		return o, nil // fund sends the cancel after the order
	}
	return o, r.Emit(ctx, event.TopicDerivOrderCommands, cancelCommand(o), "symbol", o.Symbol)
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

var (
	sides = map[domain.Side]orderv1.Side{domain.Buy: orderv1.Side_SIDE_BUY, domain.Sell: orderv1.Side_SIDE_SELL}
	tifs  = map[domain.TimeInForce]orderv1.TimeInForce{
		domain.GTC: orderv1.TimeInForce_TIME_IN_FORCE_GTC, domain.IOC: orderv1.TimeInForce_TIME_IN_FORCE_IOC,
		domain.FOK: orderv1.TimeInForce_TIME_IN_FORCE_FOK, domain.PostOnly: orderv1.TimeInForce_TIME_IN_FORCE_POST_ONLY,
	}
)

// toProto is the order as the engine's contract shard receives it: a
// limit order (a market order at its protection price) without fees,
// which derivatives-service charges itself.
func toProto(o domain.Order, c domain.Contract) *orderv1.Order {
	return &orderv1.Order{
		OrderId: o.ID, ClientOrderId: o.ClientOrderID, UserId: o.UserID, Symbol: o.Symbol, Side: sides[o.Side],
		Type: orderv1.OrderType_ORDER_TYPE_LIMIT, TimeInForce: tifs[o.TimeInForce], Price: o.Price.String(), Quantity: o.Qty.String(),
		SelfTradePrevention: orderv1.SelfTradePrevention_SELF_TRADE_PREVENTION_CANCEL_NEWEST, MakerFeeRate: "0", TakerFeeRate: "0",
		BaseDecimals: c.BaseDecimals, QuoteDecimals: c.QuoteDecimals, TickSize: c.TickSize.String(), LotSize: c.LotSize.String(),
		BaseAsset: c.Base, QuoteAsset: c.Quote,
	}
}

func cancelCommand(o domain.Order) *orderv1.CancelOrder {
	return &orderv1.CancelOrder{OrderId: o.ID, UserId: o.UserID, Symbol: o.Symbol}
}
