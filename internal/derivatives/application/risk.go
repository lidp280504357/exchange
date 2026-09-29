package application

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	derivativesv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

// ReasonMarkStale is the degradation derivatives-service sets itself when a
// mark price stops.
const ReasonMarkStale = "MARK_PRICE_STALE"

// markGrace is how long after its start the service waits before a
// missing mark price counts as stale.
const markGrace = 30 * time.Second

// Monitor checks the margins at the mark prices (requirements §11.7; plan
// §7.3 task 7), once per call (every second):
//
//   - an isolated position is measured on its own, the cross positions of
//     a user together against the account's cross equity (the available
//     balance, their margin and the reservations of cross orders, with
//     their unrealized result);
//   - at most 1.2 times the maintenance margin a LiquidationWarning goes
//     out, once until the margin is back above 1.3 times it;
//   - at the maintenance margin the liquidation engine takes the
//     position(s) over: their orders are canceled and IOC liquidation
//     orders close them at the bankruptcy price (isolated) or the mark
//     price (cross), 0.5% worse; after MaxLiquidationAttempts orders the
//     rest is auto-deleveraged against the best-ranked counterparties;
//   - a contract with open positions whose mark price is 10 seconds old is
//     put under reduce-only (MARK_PRICE_STALE).
func (s *Service) Monitor(ctx context.Context) error {
	contracts, err := s.Instruments.Contracts(ctx)
	if err != nil {
		return err
	}
	byContract := map[string]domain.Contract{}
	marks := map[string]decimal.Decimal{}
	cross := map[string][]domain.Position{}
	for _, c := range contracts {
		if c.Status == "DELISTED" {
			continue
		}
		byContract[c.Symbol] = c
		open, err := s.Store.Read().Positions().Open(ctx, c.Symbol)
		if err != nil {
			return err
		}
		m, fresh := s.Marks.Mark(c.Symbol)
		if !fresh {
			if len(open) > 0 && s.Now().Sub(s.Started) > markGrace {
				if err := s.OnDegraded(ctx, c.Symbol, ReasonMarkStale); err != nil {
					return err
				}
			}
			continue
		}
		marks[c.Symbol] = m.Price
		for _, p := range open {
			if p.MarginMode == domain.Cross {
				cross[p.UserID] = append(cross[p.UserID], p)
				continue
			}
			if err := s.checkIsolated(ctx, c, p, m.Price); err != nil {
				s.Log.WarnContext(ctx, "margin check failed", "user_id", p.UserID, "symbol", p.Symbol, "error", err)
			}
		}
	}
	for user, positions := range cross {
		if err := s.checkCross(ctx, user, positions, byContract, marks); err != nil {
			s.Log.WarnContext(ctx, "cross margin check failed", "user_id", user, "error", err)
		}
	}
	return nil
}

func (s *Service) checkIsolated(ctx context.Context, c domain.Contract, p domain.Position, mark decimal.Decimal) error {
	if p.Liquidating {
		return s.continueLiquidation(ctx, c, p, mark)
	}
	balance, maintenance := p.MarginBalance(mark), p.MaintenanceMargin(c, mark)
	switch domain.State(balance, maintenance) {
	case domain.MarginLiquidate:
		return s.takeOver(ctx, []domain.Position{p}, map[string]decimal.Decimal{c.Symbol: mark}, balance, maintenance)
	case domain.MarginWarning:
		if p.WarnedAt.IsZero() {
			return s.warnPosition(ctx, p, balance, maintenance)
		}
	default:
		if !p.WarnedAt.IsZero() && domain.Recovered(balance, maintenance) {
			return s.clearWarning(ctx, p)
		}
	}
	return nil
}

// checkCross measures a user's cross positions against the cross equity.
// A position of a contract without a fresh mark price leaves the account
// unmeasured this round.
func (s *Service) checkCross(ctx context.Context, userID string, positions []domain.Position, contracts map[string]domain.Contract,
	marks map[string]decimal.Decimal,
) error {
	liquidating := false
	for _, p := range positions {
		if _, ok := marks[p.Symbol]; !ok {
			return nil
		}
		liquidating = liquidating || p.Liquidating
	}
	if liquidating {
		for _, p := range positions {
			if p.Liquidating {
				if err := s.continueLiquidation(ctx, contracts[p.Symbol], p, marks[p.Symbol]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	asset := contracts[positions[0].Symbol].Quote
	bal, err := s.Ledger.Balance(ctx, userID, asset)
	if err != nil {
		return err
	}
	orders, err := s.Store.Read().Orders().Unreleased(ctx, userID)
	if err != nil {
		return err
	}
	equity, maintenance := bal.Available, decimal.Zero
	for _, o := range orders {
		if o.MarginMode == domain.Cross {
			equity = equity.Add(o.Unreleased())
		}
	}
	for _, p := range positions {
		mark := marks[p.Symbol]
		equity = equity.Add(p.Margin).Add(p.UnrealizedPnL(mark))
		maintenance = maintenance.Add(p.MaintenanceMargin(contracts[p.Symbol], mark))
	}
	warned, err := s.Store.Read().Cross().WarnedAt(ctx, userID)
	if err != nil {
		return err
	}
	switch domain.State(equity, maintenance) {
	case domain.MarginLiquidate:
		return s.takeOver(ctx, positions, marks, equity, maintenance)
	case domain.MarginWarning:
		if warned.IsZero() {
			return s.Store.Tx(ctx, func(r ports.Repos) error {
				if err := r.Cross().SetWarnedAt(ctx, userID, s.Now()); err != nil {
					return err
				}
				return r.Emit(ctx, event.TopicDerivLiquidation, &derivativesv1.LiquidationWarning{
					UserId: userID, Cross: true, MarginBalance: equity.String(), MaintenanceMargin: maintenance.String(),
					At: timestamppb.New(s.Now()),
				}, "user", userID)
			})
		}
	default:
		if !warned.IsZero() && domain.Recovered(equity, maintenance) {
			return s.Store.Tx(ctx, func(r ports.Repos) error { return r.Cross().SetWarnedAt(ctx, userID, time.Time{}) })
		}
	}
	return nil
}

func (s *Service) warnPosition(ctx context.Context, p domain.Position, balance, maintenance decimal.Decimal) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, p.UserID); err != nil {
			return err
		}
		cur, ok, err := reload(ctx, r, p)
		if err != nil || !ok || !cur.WarnedAt.IsZero() {
			return err
		}
		cur.WarnedAt = s.Now()
		if _, err := r.Positions().Save(ctx, cur); err != nil {
			return err
		}
		s.step("warning")
		return r.Emit(ctx, event.TopicDerivLiquidation, &derivativesv1.LiquidationWarning{
			UserId: p.UserID, Symbol: p.Symbol, PositionSide: string(p.Side), MarginBalance: balance.String(),
			MaintenanceMargin: maintenance.String(), At: timestamppb.New(s.Now()),
		}, "user", p.UserID)
	})
}

func (s *Service) clearWarning(ctx context.Context, p domain.Position) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, p.UserID); err != nil {
			return err
		}
		cur, ok, err := reload(ctx, r, p)
		if err != nil || !ok {
			return err
		}
		cur.WarnedAt = time.Time{}
		_, err = r.Positions().Save(ctx, cur)
		return err
	})
}

// reload reads a position again inside a transaction.
func reload(ctx context.Context, r ports.Repos, p domain.Position) (domain.Position, bool, error) {
	held, err := r.Positions().OfUser(ctx, p.UserID, p.Symbol)
	if err != nil {
		return domain.Position{}, false, err
	}
	cur, ok := byside(held)[p.Side]
	return cur, ok && !cur.Flat(), nil
}

// takeOver hands positions to the liquidation engine: their orders are
// canceled (every order of the user in the cross margin mode for cross
// positions, the position's own for an isolated one) and each is marked
// liquidating, which refuses the user's orders on it.
func (s *Service) takeOver(ctx context.Context, positions []domain.Position, marks map[string]decimal.Decimal, balance, maintenance decimal.Decimal) error {
	userID := positions[0].UserID
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		active, err := r.Orders().Active(ctx, userID, "")
		if err != nil {
			return err
		}
		for _, p := range positions {
			cur, ok, err := reload(ctx, r, p)
			if err != nil {
				return err
			}
			if !ok || cur.Liquidating {
				continue
			}
			for _, o := range active {
				mine := o.Symbol == cur.Symbol && (o.PositionSide == cur.Side || cur.Side == domain.SideBoth)
				if cur.MarginMode == domain.Cross {
					mine = o.MarginMode == domain.Cross
				}
				if !mine || o.Kind != domain.KindUser || o.CancelRequested {
					continue
				}
				if _, err := s.requestCancel(ctx, r, o); err != nil {
					return err
				}
			}
			cur.Liquidating, cur.LiquidationAttempts, cur.LiquidationAt, cur.UpdatedAt = true, 0, s.Now(), s.Now()
			saved, err := r.Positions().Save(ctx, cur)
			if err != nil {
				return err
			}
			started := &derivativesv1.LiquidationStarted{
				Position: positionProto(saved), Cross: cur.MarginMode == domain.Cross, MarkPrice: marks[cur.Symbol].String(),
				MarginBalance: balance.String(), MaintenanceMargin: maintenance.String(),
			}
			if cur.MarginMode == domain.Isolated {
				started.BankruptcyPrice = cur.BankruptcyPrice().String()
			}
			if err := r.Emit(ctx, event.TopicDerivLiquidation, started, "user", userID); err != nil {
				return err
			}
			s.step("takeover")
			s.Log.WarnContext(ctx, "position taken over for liquidation", "user_id", userID, "symbol", cur.Symbol,
				"position_side", cur.Side, "margin_balance", balance.String(), "maintenance_margin", maintenance.String())
		}
		return nil
	})
}

// continueLiquidation places the next liquidation order of a taken-over
// position, or auto-deleverages it once MaxLiquidationAttempts orders did
// not close it. A liquidation order still active is waited for.
func (s *Service) continueLiquidation(ctx context.Context, c domain.Contract, p domain.Position, mark decimal.Decimal) error {
	if p.LiquidationAttempts >= domain.MaxLiquidationAttempts {
		return s.deleverage(ctx, c, p, mark)
	}
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, p.UserID); err != nil {
			return err
		}
		cur, ok, err := reload(ctx, r, p)
		if err != nil || !ok || !cur.Liquidating {
			return err
		}
		active, err := r.Orders().Active(ctx, p.UserID, p.Symbol)
		if err != nil {
			return err
		}
		for _, o := range active {
			if o.Kind == domain.KindLiquidation && o.PositionSide == cur.Side {
				return nil // wait for it
			}
		}
		anchor := mark
		if cur.MarginMode == domain.Isolated {
			anchor = cur.BankruptcyPrice()
		}
		o := domain.LiquidationOrder(uuid.Must(uuid.NewV7()).String(), c, cur, anchor, s.Now())
		if err := r.Orders().Insert(ctx, o); err != nil {
			return err
		}
		cur.LiquidationAttempts++
		cur.UpdatedAt = s.Now()
		if _, err := r.Positions().Save(ctx, cur); err != nil {
			return err
		}
		s.step("order")
		return s.accept(ctx, r, o, c)
	})
}

// deleverage closes what is left of a taken-over position against its
// counterparties (§11.7 ADL), best-ranked first, at the position's
// bankruptcy price (isolated) or the mark price (cross), outside the book
// and without fees. Each pair is a synthetic trade applied like the
// engine's.
func (s *Service) deleverage(ctx context.Context, c domain.Contract, p domain.Position, mark decimal.Decimal) error {
	s.fills.Lock()
	defer s.fills.Unlock()
	open, err := s.Store.Read().Positions().Open(ctx, c.Symbol)
	if err != nil {
		return err
	}
	var cur domain.Position
	for _, o := range open {
		if o.ID == p.ID {
			cur = o
		}
	}
	if cur.ID == "" || !cur.Liquidating {
		return nil
	}
	price := domain.ADLPrice(c, cur, mark)
	left := cur.Qty.Abs()
	for _, cp := range domain.ADLQueue(cur, open, mark) {
		if !left.IsPositive() {
			break
		}
		q := decimal.Min(left, cp.Qty.Abs())
		now := s.Now()
		mine := domain.ADLOrder(uuid.Must(uuid.NewV7()).String(), c, cur, q, price, now)
		theirs := domain.ADLOrder(uuid.Must(uuid.NewV7()).String(), c, cp, q, price, now)
		if err := s.Store.Tx(ctx, func(r ports.Repos) error {
			if err := r.Orders().Insert(ctx, mine); err != nil {
				return err
			}
			return r.Orders().Insert(ctx, theirs)
		}); err != nil {
			return err
		}
		t := Trade{ID: uuid.Must(uuid.NewV7()).String(), Symbol: c.Symbol, Price: price, Qty: q, At: now}
		if mine.Side == domain.Buy {
			t.BuyerOrderID, t.BuyerUserID, t.SellerOrderID, t.SellerUserID = mine.ID, cur.UserID, theirs.ID, cp.UserID
		} else {
			t.BuyerOrderID, t.BuyerUserID, t.SellerOrderID, t.SellerUserID = theirs.ID, cp.UserID, mine.ID, cur.UserID
		}
		if err := s.applyFill(ctx, c, t, mine.Side, mine.ID, cur.UserID, false); err != nil {
			return err
		}
		if err := s.applyFill(ctx, c, t, theirs.Side, theirs.ID, cp.UserID, false); err != nil {
			return err
		}
		s.step("adl")
		s.Log.WarnContext(ctx, "position auto-deleveraged", "user_id", cur.UserID, "counterparty", cp.UserID, "symbol", c.Symbol,
			"quantity", q.String(), "price", price.String())
		left = left.Sub(q)
	}
	if left.IsPositive() {
		return fmt.Errorf("auto-deleveraging %s of %s left %s without counterparties", cur.ID, c.Symbol, left)
	}
	return nil
}

// liquidationEvent is the event a settled fill of a taken-over position,
// or of an auto-deleveraged counterparty, adds; nil for other fills.
func liquidationEvent(o domain.Order, f domain.Fill, liquidated bool) proto.Message {
	switch {
	case o.Kind == domain.KindLiquidation || (o.Kind == domain.KindADL && liquidated):
		return &derivativesv1.LiquidationFilled{
			UserId: f.UserID, Symbol: f.Symbol, PositionSide: string(f.PositionSide), TradeId: f.TradeID, Price: f.Price.String(),
			Quantity: f.Qty.String(), RealizedPnl: f.RealizedPnL.String(), InsurancePaid: f.Insurance.String(), Adl: o.Kind == domain.KindADL,
		}
	case o.Kind == domain.KindADL:
		return &derivativesv1.AdlExecuted{
			UserId: f.UserID, Symbol: f.Symbol, PositionSide: string(f.PositionSide), TradeId: f.TradeID, Price: f.Price.String(),
			Quantity: f.Qty.String(), RealizedPnl: f.RealizedPnL.String(),
		}
	}
	return nil
}

func (s *Service) step(name string) {
	if s.Metrics != nil {
		s.Metrics.Liquidations.WithLabelValues(name).Inc()
	}
}
