package application

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// Flattening a user's contract accounts (design 2026-10-09 user kinds §1
// #9, batch L4b): a purge of a test account ends what it holds here before
// its balances are moved out. Its orders are canceled, its take-profits
// and stop-losses ended, and its positions closed against HOUSE as the
// liquidation engine closes a cross position: IOC at the mark price moved
// by domain.Slippage against it.

// FlattenRounds are the closing rounds Flatten tries.
const FlattenRounds = 3

// ReasonAdmin ends the take-profits and stop-losses Flatten cancels.
const ReasonAdmin = "ADMIN"

// LeftNotFilled is why a position Flatten tried to close is left open
// when its closing orders found nothing to fill at their price.
const LeftNotFilled = "NOT_FILLED"

// FlattenResult is what Flatten did and what it left.
type FlattenResult struct {
	CanceledOrders       int
	CanceledConditionals int
	Closed               []Closed
	Remaining            []Left
}

// Complete reports whether nothing is left: no position open, no order
// working.
func (r FlattenResult) Complete() bool { return len(r.Remaining) == 0 }

// Closed is what Flatten closed of a position: LONG or SHORT, the
// quantity (contracts on a coin-margined contract) and the average price.
type Closed struct {
	Symbol   string
	Side     string
	Quantity decimal.Decimal
	Price    decimal.Decimal
	quote    decimal.Decimal
}

// Left is a position Flatten left open (LONG or SHORT, its quantity) and
// why: the code its closing order was refused with (the contract not
// trading, no fresh mark price, ...), DERIV_POSITION_LIQUIDATING,
// NOT_FILLED, or DERIV_CLOSE_PENDING when orders were still working as it
// gave up or the position changed meanwhile.
type Left struct {
	Symbol   string
	Side     string
	Quantity decimal.Decimal
	Reason   string
}

// Flatten ends what a user holds on their contract accounts for a purge.
// Under the user's lock their orders are canceled (their own and their
// take-profits' and stop-losses'; the console's run their course) and
// their active take-profits and stop-losses end CANCELED (ADMIN), each
// audited as admin.orders.canceled by actor. Once the engine has
// confirmed the cancels (waited for up to FlattenWait), every open
// position - both sides in hedge mode, every settlement asset - is closed
// with IOC orders of kind ADMIN, reduce-only in one-way mode, at its mark
// price moved by domain.Slippage against it as the liquidation engine
// closes a cross position (domain.CloseAt), at most the contract's
// largest order each: only HOUSE takes them (no user's order rests). Up
// to FlattenRounds rounds; what is still open comes back with the reason.
// A user with a position under liquidation, or whose cross account is
// being liquidated, is refused (DERIV_POSITION_LIQUIDATING) before
// anything changes; HOUSE is not flattened. A user who never traded
// contracts - user-service is not asked whether it knows them (the
// coordinator's 06:14 rule: a purge calls it for every test account) -
// has nothing to flatten. Calling it again cancels and closes what is
// open by then: nothing once all is done. Each call is audited as
// derivatives.user_flattened.
func (s *Service) Flatten(ctx context.Context, userID, actor, reason string) (FlattenResult, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return FlattenResult{}, apperr.Invalid("user_id must be a UUID")
	}
	userID = id.String()
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return FlattenResult{}, apperr.Invalid("an actor and a reason of at least 3 characters are required")
	}
	if s.HouseUser != "" && strings.EqualFold(userID, s.HouseUser) {
		return FlattenResult{}, ErrHouseClose
	}
	var out FlattenResult
	working, err := s.cancelAll(ctx, userID, actor, reason, &out)
	if err != nil {
		return FlattenResult{}, err
	}
	f := &flattening{why: map[string]string{}, ours: map[string]bool{}, closed: map[string]*Closed{}}
	orders, err := s.await(ctx, working)
	if err != nil {
		return FlattenResult{}, err
	}
	f.look(orders)
	for round := range FlattenRounds {
		held, err := s.openOf(ctx, userID)
		if err == nil && len(held) > 0 && round > 0 {
			if err = s.sleep(ctx, s.flattenPause()); err == nil {
				held, err = s.openOf(ctx, userID)
			}
		}
		if err != nil {
			return FlattenResult{}, err
		}
		if len(held) == 0 && len(f.carry) == 0 {
			break
		}
		// A position with an order still working (a cancel the engine has
		// not confirmed, a close of an earlier round) waits for it.
		busy := map[string]bool{}
		ids := make([]string, 0, len(f.carry))
		for _, o := range f.carry {
			busy[positionKey(o.Symbol, o.PositionSide)] = true
			ids = append(ids, o.ID)
		}
		for _, p := range held {
			k := positionKey(p.Symbol, p.Side)
			if busy[k] {
				continue
			}
			placed, code, err := s.closeOut(ctx, userID, p)
			if err != nil {
				return FlattenResult{}, err
			}
			f.why[k] = code
			for _, id := range placed {
				f.ours[id] = true
			}
			ids = append(ids, placed...)
		}
		orders, err := s.await(ctx, ids)
		if err != nil {
			return FlattenResult{}, err
		}
		f.look(orders)
	}
	for _, o := range f.carry { // what they filled so far counts
		if f.ours[o.ID] {
			f.add(o)
		}
	}
	for _, k := range f.order {
		c := f.closed[k]
		c.Price = c.quote.DivRound(c.Quantity, 8)
		out.Closed = append(out.Closed, *c)
	}
	held, err := s.openOf(ctx, userID)
	if err != nil {
		return FlattenResult{}, err
	}
	left := map[string]bool{}
	for _, p := range held {
		k := positionKey(p.Symbol, p.Side)
		l := Left{Symbol: p.Symbol, Side: direction(p), Quantity: p.Qty.Abs(), Reason: f.why[k]}
		if l.Reason == "" { // it changed after its last look
			l.Reason = domain.ErrClosePending.Code
		}
		left[k] = true
		out.Remaining = append(out.Remaining, l)
	}
	// An order still working on a flat position may open it again: not
	// done either.
	for _, o := range f.carry {
		if k := positionKey(o.Symbol, o.PositionSide); !left[k] {
			left[k] = true
			way := decimal.NewFromInt(1) // in one-way mode, the way it fills
			if o.Side == domain.Sell {
				way = way.Neg()
			}
			out.Remaining = append(out.Remaining, Left{
				Symbol: o.Symbol, Side: direction(domain.Position{Side: o.PositionSide, Qty: way}), Quantity: decimal.Zero,
				Reason: domain.ErrClosePending.Code,
			})
		}
	}
	if err := s.auditFlatten(ctx, userID, actor, reason, out); err != nil {
		return FlattenResult{}, err
	}
	s.Log.InfoContext(ctx, "contract accounts flattened", "user_id", userID, "actor", actor, "canceled_orders", out.CanceledOrders,
		"canceled_conditionals", out.CanceledConditionals, "closed", len(out.Closed), "remaining", len(out.Remaining))
	return out, nil
}

// flattening is how far a Flatten call got.
type flattening struct {
	why    map[string]string // by position: why it is still open
	ours   map[string]bool   // the closing orders it placed
	carry  []domain.Order    // the orders still working at the last look
	closed map[string]*Closed
	order  []string // closed's keys as they came
}

// look takes in the orders as await returned them: what the finished
// closing orders filled is closed (IOC may have left some), the orders
// still working are carried to the next round.
func (f *flattening) look(orders []domain.Order) {
	f.carry = nil
	for _, o := range orders {
		k := positionKey(o.Symbol, o.PositionSide)
		if !finished(o) {
			f.carry = append(f.carry, o)
			f.why[k] = domain.ErrClosePending.Code
			continue
		}
		if !f.ours[o.ID] {
			continue
		}
		if o.Filled.LessThan(o.Qty) {
			f.why[k] = LeftNotFilled
		}
		f.add(o)
	}
}

// add counts what a closing order filled.
func (f *flattening) add(o domain.Order) {
	if !o.Filled.IsPositive() {
		return
	}
	side := "LONG" // a sell closes a long
	if o.Side == domain.Buy {
		side = "SHORT"
	}
	k := o.Symbol + "|" + side
	c := f.closed[k]
	if c == nil {
		c = &Closed{Symbol: o.Symbol, Side: side, Quantity: decimal.Zero, quote: decimal.Zero}
		f.closed[k] = c
		f.order = append(f.order, k)
	}
	c.Quantity, c.quote = c.Quantity.Add(o.Filled), c.quote.Add(o.FilledQuote)
}

// cancelAll requests the cancel of the user's orders and ends their
// take-profits and stop-losses, under their lock, unless a position of
// theirs is under liquidation or their cross account is being liquidated;
// it returns the orders working then (the console's too), to wait for.
func (s *Service) cancelAll(ctx context.Context, userID, actor, reason string, out *FlattenResult) ([]string, error) {
	var working []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		working, out.CanceledOrders, out.CanceledConditionals = nil, 0, 0
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		open, err := r.CrossLiquidations().AllOpen(ctx)
		if err != nil {
			return err
		}
		for _, l := range open {
			if strings.EqualFold(l.UserID, userID) {
				return ErrCrossLiquidating.WithDetail("settle_asset", l.Asset).WithDetail("liquidation_id", l.ID)
			}
		}
		held, err := r.Positions().OfUser(ctx, userID, "")
		if err != nil {
			return err
		}
		for _, p := range held {
			if p.Liquidating && !p.Flat() {
				return domain.ErrLiquidating.WithDetail("symbol", p.Symbol).WithDetail("position_side", string(p.Side))
			}
		}
		active, err := r.Orders().Active(ctx, userID, "")
		if err != nil {
			return err
		}
		for _, o := range active {
			working = append(working, o.ID)
			if !canceled(o) {
				continue
			}
			if _, err := s.requestCancel(ctx, r, o); err != nil {
				return err
			}
			out.CanceledOrders++
			if err := auditCancel(ctx, r, ProductOrder{ID: o.ID, UserID: userID, Symbol: o.Symbol}, "", actor, reason); err != nil {
				return err
			}
		}
		const page = 100
		for before := ""; ; {
			conds, err := r.Conditionals().OfUser(ctx, userID, "", domain.ConditionalActive, before, page)
			if err != nil {
				return err
			}
			for _, c := range conds {
				c.Status, c.Reason, c.UpdatedAt = domain.ConditionalCanceled, ReasonAdmin, s.Now()
				if err := r.Conditionals().Update(ctx, c); err != nil {
					return err
				}
				out.CanceledConditionals++
				if err := auditCancel(ctx, r, ProductOrder{ID: c.ID, UserID: userID, Symbol: c.Symbol, Conditional: true}, "", actor, reason); err != nil {
					return err
				}
			}
			if len(conds) < page {
				return nil
			}
			before = conds[len(conds)-1].ID
		}
	})
	return working, err
}

// closeOut places the IOC orders closing p at its mark price moved by
// domain.Slippage against it, at most the contract's largest order each,
// and returns them, with the code of the refusal that stopped it ("" when
// none did). The store failing is an error.
func (s *Service) closeOut(ctx context.Context, userID string, p domain.Position) ([]string, string, error) {
	if p.Liquidating {
		return nil, domain.ErrLiquidating.Code, nil
	}
	c, err := s.Instruments.Contract(ctx, p.Symbol)
	if err != nil {
		code, err := refusal(err)
		return nil, code, err
	}
	m, fresh := s.Marks.Mark(p.Symbol)
	if !fresh {
		return nil, domain.ErrMarkUnavailable.Code, nil
	}
	side, price := domain.CloseAt(c, p, m.Price)
	var ids []string
	for left := p.Qty.Abs(); left.IsPositive(); {
		q := left
		if c.MaxQuantity.IsPositive() && q.GreaterThan(c.MaxQuantity) {
			q = c.MaxQuantity
		}
		o, err := s.Place(ctx, domain.Request{
			UserID: userID, Symbol: p.Symbol, Side: side, PositionSide: p.Side, Type: domain.Limit, TimeInForce: domain.IOC,
			Price: price, Qty: q, ReduceOnly: p.Side == domain.SideBoth, Kind: domain.KindAdmin,
		})
		if err != nil {
			code, err := refusal(err)
			return ids, code, err
		}
		ids = append(ids, o.ID)
		left = left.Sub(q)
	}
	return ids, "", nil
}

// refusal is the code a request was refused with; an error that is no
// refusal (the store or a service failing) is returned.
func refusal(err error) (string, error) {
	if e := apperr.From(err); e.Kind != apperr.KindInternal {
		return e.Code, nil
	}
	return "", err
}

// await waits until the orders are finished (finished), up to FlattenWait,
// and returns them as they are then.
func (s *Service) await(ctx context.Context, ids []string) ([]domain.Order, error) {
	looks := int(s.flattenWait() / s.flattenPoll())
	for look := 0; ; look++ {
		orders := make([]domain.Order, 0, len(ids))
		done := true
		for _, id := range ids {
			o, err := s.Store.Read().Orders().Get(ctx, id)
			if err != nil {
				return nil, err
			}
			orders = append(orders, o)
			done = done && finished(o)
		}
		if done || look >= looks {
			return orders, nil
		}
		if err := s.sleep(ctx, s.flattenPoll()); err != nil {
			return nil, err
		}
	}
}

// finished reports whether the engine is done with the order and its
// fills are applied to the positions.
func finished(o domain.Order) bool {
	return o.Status.Terminal() && !o.Consumed.LessThan(o.Filled)
}

// openOf returns the user's open positions.
func (s *Service) openOf(ctx context.Context, userID string) ([]domain.Position, error) {
	held, err := s.Store.Read().Positions().OfUser(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	open := held[:0]
	for _, p := range held {
		if !p.Flat() {
			open = append(open, p)
		}
	}
	return open, nil
}

func positionKey(symbol string, side domain.PositionSide) string { return symbol + "|" + string(side) }

// auditFlatten records the call and what it did.
func (s *Service) auditFlatten(ctx context.Context, userID, actor, reason string, out FlattenResult) error {
	type entry struct {
		Symbol   string `json:"symbol"`
		Side     string `json:"side"`
		Quantity string `json:"quantity"`
		Price    string `json:"price,omitempty"`
		Reason   string `json:"reason,omitempty"`
	}
	closed, left := []entry{}, []entry{}
	for _, c := range out.Closed {
		closed = append(closed, entry{Symbol: c.Symbol, Side: c.Side, Quantity: c.Quantity.String(), Price: c.Price.String()})
	}
	for _, l := range out.Remaining {
		left = append(left, entry{Symbol: l.Symbol, Side: l.Side, Quantity: l.Quantity.String(), Reason: l.Reason})
	}
	details, err := json.Marshal(map[string]any{
		"canceled_orders": out.CanceledOrders, "canceled_conditionals": out.CanceledConditionals, "closed": closed, "remaining": left,
		"complete": out.Complete(),
	})
	if err != nil {
		return err
	}
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "derivatives.user_flattened", Actor: actor, Reason: reason, Details: string(details),
		}, "actor", actor)
	})
}

func (s *Service) flattenWait() time.Duration  { return orDefault(s.FlattenWait, 8*time.Second) }
func (s *Service) flattenPoll() time.Duration  { return orDefault(s.FlattenPoll, 200*time.Millisecond) }
func (s *Service) flattenPause() time.Duration { return orDefault(s.FlattenPause, time.Second) }

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// sleep waits d, or until ctx ends.
func (s *Service) sleep(ctx context.Context, d time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
