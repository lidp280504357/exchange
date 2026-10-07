// Package application runs the spot order use cases (requirements §5.6,
// §11.1): place, cancel and read orders.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/trading/domain"
	"github.com/skill/exchange/internal/trading/ports"
)

// The eligibility features of orders (§5.4): SPOT_TRADE for spot orders,
// MARGIN_TRADE for orders on a margin account (ACTIVE accounts while
// margin.enabled allows the user, the one margin eligibility the sites and
// margin-service share; review CO).
const (
	FeatureSpotTrade   = "SPOT_TRADE"
	FeatureMarginTrade = "MARGIN_TRADE"
)

// Service places and cancels orders.
type Service struct {
	Store  ports.Store
	Ledger ports.Ledger
	// Margin checks and funds orders on margin accounts (margin design
	// 2026-10-06); nil refuses them.
	Margin      ports.Margin
	Instruments ports.Instruments
	Eligibility ports.Eligibility
	Prices      ports.Prices
	// Features decides whether orders of a pair trade only with HOUSE
	// (ADR-0015); nil means users always trade with each other.
	Features ports.Features
	// Products closes spot trading as a product line (design 2026-10-07,
	// product switches: product.spot); nil keeps it open.
	Products ports.Products
	// FeeFree are the accounts whose orders pay no fees: the simulated
	// market's bots (ASTRA design §4).
	FeeFree []string
	// CallTimeout bounds each call to the ledger and margin-service, so a
	// dependency that hangs leaves the order pending instead of holding
	// the request or a recovery pass; 5 seconds when zero.
	CallTimeout time.Duration
	Log         *slog.Logger
	Now         func() time.Time

	// borrowers is SpotLine's last count of the margin accounts owing.
	borrowers struct {
		mu sync.Mutex
		n  int
		at time.Time
	}
}

// bounded is ctx with the call timeout.
func (s *Service) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	d := s.CallTimeout
	if d <= 0 {
		d = 5 * time.Second
	}
	return context.WithTimeout(ctx, d)
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
	closed := s.spotClosed(req.UserID)
	if closed && !req.Defaults().AccountType.Margin() {
		return domain.Order{}, flags.ErrProductClosed(flags.KeyProductSpot)
	}
	pair, err := s.Instruments.Pair(ctx, req.Symbol)
	if err != nil {
		return domain.Order{}, err
	}
	feature, what, symbol := FeatureSpotTrade, "spot trading", pair.Symbol
	if d := req.Defaults(); d.AccountType.Margin() {
		if err := s.marginOpen(req.UserID, d.SideEffect); err != nil {
			return domain.Order{}, err
		}
		if closed {
			if err := s.repays(ctx, d, pair); err != nil {
				return domain.Order{}, err
			}
		}
		// The account's symbol, as margin-service asks it: the isolated
		// account's pair, none for the cross account.
		feature, what = FeatureMarginTrade, "margin trading"
		if d.AccountType == domain.AccountMarginCross {
			symbol = ""
		}
	}
	allowed, reason, err := s.Eligibility.Check(ctx, req.UserID, feature, symbol)
	if err != nil {
		return domain.Order{}, err
	}
	if !allowed {
		return domain.Order{}, apperr.New(apperr.KindForbidden, reason, what+" is not available to this account now")
	}
	if slices.Contains(s.FeeFree, req.UserID) {
		pair.MakerFeeRate, pair.TakerFeeRate = decimal.Zero, decimal.Zero
	}
	anchor, err := s.Prices.Anchor(ctx, pair.Symbol, pair.Reference != "")
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
		// And the line again, just before the order is stored: an order
		// checked before CancelOpen reread the flag is stored by its second
		// pass. A repayment passes only when the line was seen closed up
		// front: then repays checked its debt.
		if s.spotClosed(o.UserID) && (!closed || !takenWhileClosed(o)) {
			return flags.ErrProductClosed(flags.KeyProductSpot)
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

// Liquidation is margin-service's order closing a margin account (margin
// design §4.5, E0 §3.4): a market sell of quantity, or a market buy
// spending quote amount. Attempt numbers the orders of one liquidation,
// pair and side (1 when not given): another one after a rejection, or the
// next part of a sale too big for one order (review CU, B92).
type Liquidation struct {
	LiquidationID string
	UserID        string
	Account       domain.AccountType
	Symbol        string
	Side          domain.Side
	Quantity      decimal.Decimal
	QuoteAmount   decimal.Decimal
	SideEffect    domain.SideEffect
	Attempt       int
}

// maxLiquidationAttempts bounds Attempt.
const maxLiquidationAttempts = 1000

// liquidationClient is a liquidation order's client_order_id: the same
// liquidation, pair, side and attempt is the same order. The first
// attempt keeps the key orders had before attempts were numbered.
func liquidationClient(l Liquidation) string {
	key := l.LiquidationID + "|" + l.Symbol + "|" + string(l.Side)
	if l.Attempt > 1 {
		key += "|" + strconv.Itoa(l.Attempt)
	}
	sum := sha256.Sum256([]byte(key))
	return "liq_" + hex.EncodeToString(sum[:16])
}

// Liquidate places margin-service's liquidation order: a market order on
// the margin account against the book (HOUSE where the pair has its
// liquidity), past the order limits, the minimum notional, margin.enabled
// and the eligibility (the account is frozen; this is its way out), funded
// without a reservation; past the protection price too on a pair with a
// reference market (one without keeps the order within half and twice the
// anchor, domain.NewOrder). Idempotent by liquidation, pair, side and
// attempt: a repeat returns the order (a rejected one its rejection; the
// next attempt is a new order).
func (s *Service) Liquidate(ctx context.Context, l Liquidation) (domain.Order, error) {
	if _, err := uuid.Parse(l.LiquidationID); err != nil {
		return domain.Order{}, apperr.Invalid("liquidation_id must be a UUID")
	}
	if _, err := uuid.Parse(l.UserID); err != nil {
		return domain.Order{}, apperr.Invalid("user_id must be a UUID")
	}
	if l.Attempt < 0 || l.Attempt > maxLiquidationAttempts {
		return domain.Order{}, apperr.Invalid(fmt.Sprintf("attempt must be from 1 to %d", maxLiquidationAttempts))
	}
	req := domain.Request{
		UserID: l.UserID, ClientOrderID: liquidationClient(l), Symbol: l.Symbol, Side: l.Side, Type: domain.TypeMarket,
		Quantity: l.Quantity, QuoteAmount: l.QuoteAmount, AccountType: l.Account, SideEffect: l.SideEffect,
		LiquidationID: l.LiquidationID,
	}
	if prev, err := s.Store.Read().Orders().ByClientID(ctx, req.UserID, req.ClientOrderID); err == nil {
		return s.repeat(ctx, prev, req)
	} else if !errors.Is(err, domain.ErrOrderNotFound) {
		return domain.Order{}, err
	}
	pair, err := s.Instruments.Pair(ctx, req.Symbol)
	if err != nil {
		return domain.Order{}, err
	}
	// Only a pair without a reference market bounds the order by its anchor.
	anchor := decimal.Zero
	if pair.Reference == "" {
		if anchor, err = s.Prices.Anchor(ctx, pair.Symbol, false); err != nil {
			return domain.Order{}, err
		}
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
		if p, err := r.Orders().ByClientID(ctx, o.UserID, o.ClientOrderID); err == nil {
			prev = &p
			return nil
		} else if !errors.Is(err, domain.ErrOrderNotFound) {
			return err
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

// CancelAccount asks the engine to cancel the user's active orders on one
// account (margin-service, before it liquidates the account): every
// symbol of the cross account, or the isolated account's pair. It returns
// how many it asked for.
func (s *Service) CancelAccount(ctx context.Context, userID string, account domain.AccountType, symbol string) (int, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return 0, apperr.Invalid("user_id must be a UUID")
	}
	if !account.Margin() || (account == domain.AccountMarginIsolated) != (symbol != "") {
		return 0, apperr.Invalid("a margin account: MARGIN_CROSS, or MARGIN_ISOLATED with its symbol")
	}
	n := 0
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		n = 0
		active, err := r.Orders().Active(ctx, userID, symbol)
		if err != nil {
			return err
		}
		for _, o := range active {
			if o.AccountType != account || o.CancelRequested {
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

// spotClosed reports whether an operator has spot trading closed for the
// user (design 2026-10-07, product switches §1 #3 and #7): for every
// account, spot and margin alike (one book), but the market-making
// accounts' (FeeFree: the simulated market's bots), which quote whatever
// the lines are, as HOUSE does. margin-service's liquidations do not come
// through Place.
func (s *Service) spotClosed(userID string) bool {
	return s.Products != nil && s.Products.Closed(flags.KeyProductSpot) && !slices.Contains(s.FeeFree, userID)
}

// repays lets an order on a margin account through while spot trading is
// closed only when it repays (§1 #7, the coordinator's 16:59 correction:
// repayment sells): AUTO_REPAY, on an account that owes the asset the
// order brings (a sell's quote asset, a buy's base: a short closes with a
// buy). What it brings beyond the debt stays in the account.
func (s *Service) repays(ctx context.Context, d domain.Request, pair domain.Pair) error {
	refused := flags.ErrProductClosed(flags.KeyProductSpot)
	if d.SideEffect != domain.SideEffectAutoRepay {
		return refused
	}
	account, brings := domain.Account{UserID: d.UserID, Type: d.AccountType}, pair.Base
	if d.AccountType == domain.AccountMarginIsolated {
		account.Scope = pair.Symbol
	}
	if d.Side == domain.SideSell {
		brings = pair.Quote
	}
	owing, err := s.owes(ctx, account, brings)
	if err != nil {
		return err
	}
	if !owing {
		return refused
	}
	return nil
}

// owes reports whether a margin account owes asset (principal or
// interest), the ledger asked within the call timeout.
func (s *Service) owes(ctx context.Context, account domain.Account, asset string) (bool, error) {
	call, cancel := s.bounded(ctx)
	defer cancel()
	debt, err := s.Ledger.MarginDebt(call, account, asset)
	return debt.IsPositive(), err
}

// takenWhileClosed tells the kind of order a closed spot line still takes
// from users: AUTO_REPAY on a margin account (repays checks the debt of a
// new one, repayment of a stored one).
func takenWhileClosed(o domain.Order) bool {
	return o.AccountType.Margin() && o.SideEffect == domain.SideEffectAutoRepay
}

// repayment reports a stored order the closed line takes, as repays
// decides for a new one (review B155: the sweeps keep only these). When
// the ledger cannot answer, the order counts as none: closed means closed,
// and the user may place it again.
func (s *Service) repayment(ctx context.Context, o domain.Order) bool {
	if !takenWhileClosed(o) {
		return false
	}
	brings := o.BaseAsset
	if o.Side == domain.SideSell {
		brings = o.QuoteAsset
	}
	if brings == "" {
		return false
	}
	owing, err := s.owes(ctx, o.Account(), brings)
	if err != nil {
		s.Log.WarnContext(ctx, "the margin debt was not read; the order is canceled as spot trading is closed", "order_id", o.ID, "error", err)
		return false
	}
	return owing
}

// CanceledOrder is an order CancelOpen asked the engine to cancel.
type CanceledOrder struct {
	OrderID, UserID, Symbol string
}

// sweepGap is how long CancelOpen waits before its second pass: long
// enough for an order that passed Place's last check just before the flag
// was reread to be stored. sweepLead is how far before the line closed
// SweepClosed looks.
const (
	sweepGap  = time.Second
	sweepLead = 10 * time.Second
)

// SweepActor is who SweepClosed's cancels are audited as.
const SweepActor = "system:spot-trading-service"

// CancelOpen asks the engine to cancel every active order, on spot and
// margin accounts alike, once an operator closed spot trading (design
// 2026-10-07, product switches; the console calls it after closing
// product.spot), auditing each as admin.orders.canceled by actor with
// reason in the transaction that marks it. It rereads the flag first and
// refuses while the line is open; a second pass after sweepGap takes the
// orders that were on their way in when it closed. The orders the closed
// line takes stay (repayments: run again later, it does not take those
// placed since), as do the market-making accounts' orders and
// margin-service's liquidations; orders already being canceled are not
// asked again. It returns the orders it asked for.
func (s *Service) CancelOpen(ctx context.Context, actor, reason string) ([]CanceledOrder, error) {
	if actor == "" || reason == "" {
		return nil, apperr.Invalid("actor and reason are required")
	}
	if s.Products == nil {
		return nil, errSpotOpen
	}
	if err := s.Products.Refresh(ctx); err != nil {
		return nil, fmt.Errorf("reread the product lines: %w", err)
	}
	if !s.Products.Closed(flags.KeyProductSpot) {
		return nil, errSpotOpen
	}
	out, err := s.sweep(ctx, actor, reason, time.Time{}, nil)
	if err != nil {
		return out, err
	}
	select {
	case <-ctx.Done():
		return out, ctx.Err()
	case <-time.After(sweepGap):
	}
	return s.sweep(ctx, actor, reason, time.Time{}, out)
}

var errSpotOpen = apperr.New(apperr.KindConflict, apperr.CodeConflict, "spot trading is open; close it first")

// SweepClosed is CancelOpen's fallback, run every few seconds: while spot
// trading is closed it cancels the orders stored from sweepLead before it
// closed on that the line no longer takes (taken while some copy of the
// flag was behind, or with no CancelOpen at all), audited as SweepActor;
// the repayments it takes stay. Older orders are CancelOpen's.
func (s *Service) SweepClosed(ctx context.Context) (int, error) {
	if s.Products == nil {
		return 0, nil
	}
	f, ok := s.Products.Get(flags.KeyProductSpot)
	if !ok || f.Enabled {
		return 0, nil
	}
	out, err := s.sweep(ctx, SweepActor, "spot trading is closed", f.UpdatedAt.Add(-sweepLead), nil)
	return len(out), err
}

// borrowersEvery is how long SpotLine keeps its count of the margin
// accounts owing anything: each count reads every margin debt
// (ListMarginDebts).
const borrowersEvery = 15 * time.Second

// SpotLine is spot trading's line as the console counts it: whether it is
// closed, the open orders on spot and margin accounts (the market-making
// accounts' and the liquidations left out; repayments, which closing it
// keeps, counted) and the margin accounts that owe anything (its
// positions; counted at most every borrowersEvery).
func (s *Service) SpotLine(ctx context.Context) (closed bool, openOrders, borrowers int, err error) {
	closed = s.Products != nil && s.Products.Closed(flags.KeyProductSpot)
	if openOrders, err = s.Store.Read().Orders().CountOpen(ctx, s.FeeFree); err != nil {
		return closed, 0, 0, err
	}
	s.borrowers.mu.Lock()
	defer s.borrowers.mu.Unlock()
	if s.borrowers.at.IsZero() || s.Now().Sub(s.borrowers.at) >= borrowersEvery {
		call, cancel := s.bounded(ctx)
		defer cancel()
		n, err := s.Ledger.MarginBorrowers(call)
		if err != nil {
			return closed, openOrders, 0, err
		}
		s.borrowers.n, s.borrowers.at = n, s.Now()
	}
	return closed, openOrders, s.borrowers.n, nil
}

// sweep cancels the active orders stored at or after since that the
// closed line does not take, but the market-making accounts' and
// margin-service's liquidations, one transaction per user, appending what
// it asked for to out.
func (s *Service) sweep(ctx context.Context, actor, reason string, since time.Time, out []CanceledOrder) ([]CanceledOrder, error) {
	users, err := s.Store.Read().Orders().ActiveUsers(ctx, since)
	if err != nil {
		return out, err
	}
	for _, u := range users {
		if slices.Contains(s.FeeFree, u) {
			continue
		}
		var mine []CanceledOrder
		err := s.Store.Tx(ctx, func(r ports.Repos) error {
			mine = mine[:0]
			active, err := r.Orders().Active(ctx, u, "")
			if err != nil {
				return err
			}
			for _, o := range active {
				if o.CancelRequested || o.LiquidationID != "" || o.CreatedAt.Before(since) || s.repayment(ctx, o) {
					continue
				}
				if _, err := s.requestCancel(ctx, r, o); err != nil {
					return err
				}
				details, _ := json.Marshal(map[string]string{
					"order_id": o.ID, "symbol": o.Symbol, "account_type": string(o.AccountType), "product": flags.ProductNames[flags.KeyProductSpot],
				})
				if err := r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
					Target: "user:" + o.UserID, Action: "admin.orders.canceled", Actor: actor, Reason: reason, Details: string(details),
				}, "actor", actor); err != nil {
					return err
				}
				mine = append(mine, CanceledOrder{OrderID: o.ID, UserID: o.UserID, Symbol: o.Symbol})
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		out = append(out, mine...)
	}
	return out, nil
}

// repeat answers a request whose client_order_id names an existing order.
func (s *Service) repeat(_ context.Context, prev domain.Order, req domain.Request) (domain.Order, error) {
	if !prev.SameAs(req) {
		return domain.Order{}, domain.ErrClientIDReused.WithDetail("order_id", prev.ID)
	}
	if prev.Status == domain.StatusRejected {
		refusal := apperr.New(kindOf(prev.RejectStatus), prev.RejectReason, "the order was rejected")
		refusal.Details = maps.Clone(prev.RejectDetails)
		return prev, refusal.WithDetail("order_id", prev.ID)
	}
	return prev, nil
}

// kindOf is the kind of a stored refusal's HTTP status; the engine's
// rejections, which have none, are unprocessable.
func kindOf(status int) apperr.Kind {
	for _, k := range []apperr.Kind{
		apperr.KindInvalid, apperr.KindUnauthenticated, apperr.KindForbidden, apperr.KindNotFound, apperr.KindConflict,
		apperr.KindRateLimited, apperr.KindUnavailable,
	} {
		if k.HTTPStatus() == status {
			return k
		}
	}
	return apperr.KindUnprocessable
}

// marginOpen refuses an order on a margin account, before anything is
// stored and before the MARGIN_TRADE eligibility, while margin.enabled is
// off for the user, and one with AUTO_BORROW while margin.auto_borrow is
// off (margin design 2026-10-06 §5.1; margin-service checks both again).
func (s *Service) marginOpen(userID string, effect domain.SideEffect) error {
	subject := flags.Subject{UserID: userID}
	if s.Margin == nil || s.Features == nil || !s.Features.Enabled(flags.KeyMarginEnabled, subject) {
		return domain.ErrMarginDisabled
	}
	if effect == domain.SideEffectAutoBorrow && !s.Features.Enabled(flags.KeyMarginAutoBorrow, subject) {
		return domain.ErrMarginDisabled.WithDetail("flag", flags.KeyMarginAutoBorrow)
	}
	return nil
}

// fund freezes the order's funds (idempotent by order ID) and, in one
// transaction, records the freeze with OrderAccepted and the PlaceOrder
// command. An order on a margin account is first reserved with
// margin-service (idempotent by order ID too, so Recover replays both
// steps). A refusal rejects the order; when a service cannot be reached
// the outcome is unknown, so the order stays pending for Recover.
func (s *Service) fund(ctx context.Context, o domain.Order) (domain.Order, error) {
	if o.AccountType.Margin() && o.LiquidationID == "" {
		if s.Margin == nil {
			return s.reject(ctx, o, domain.ErrMarginDisabled)
		}
		call, cancel := s.bounded(ctx)
		r, err := s.Margin.ReserveOrder(call, o)
		cancel()
		if err != nil {
			if e := apperr.From(err); refused(e) {
				return s.reject(ctx, o, e)
			}
			s.Log.WarnContext(ctx, "margin reservation did not complete; recovery retries it", "order_id", o.ID, "error", err)
			return o, nil
		}
		o.Borrowed, o.BorrowID = r.Borrowed, r.BorrowID
		if r.Borrowed.IsPositive() {
			s.Log.InfoContext(ctx, "margin order borrowed", "order_id", o.ID, "asset", o.FrozenAsset,
				"borrowed", r.Borrowed.String(), "borrow_id", r.BorrowID)
		}
	}
	call, cancel := s.bounded(ctx)
	err := s.Ledger.Freeze(call, "order:"+o.ID, o.Account(), o.FrozenAsset, o.FrozenAmount, o.ID)
	cancel()
	if err != nil {
		if e := apperr.From(err); refused(e) {
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
		cur.Borrowed, cur.BorrowID = o.Borrowed, o.BorrowID
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
		if err := r.Emit(ctx, event.TopicOrderCommands, &orderv1.PlaceOrder{Order: msg, HouseOnly: s.houseOnly(ctx, cur.Symbol)}, "symbol", cur.Symbol); err != nil {
			return err
		}
		if cur.CancelRequested { // canceled while its freeze was pending
			return r.Emit(ctx, event.TopicOrderCommands, cancelCommand(cur), "symbol", cur.Symbol)
		}
		return nil
	})
	return out, err
}

// refused tells a service's answer from a failure to answer, for placing
// and recovering alike: an answer (any coded refusal, and
// MARGIN_PRICE_UNAVAILABLE although it is a 503) rejects the order; a
// failure (internal, unavailable, canceled) leaves the outcome unknown,
// and recovery retries it.
func refused(e *apperr.Error) bool {
	switch e.Kind {
	case apperr.KindInternal:
		return false
	case apperr.KindUnavailable:
		return e.Code == "MARGIN_PRICE_UNAVAILABLE"
	}
	return true
}

// houseOnly decides whether an order of the pair trades only with HOUSE's
// reference liquidity (ADR-0015): the pair follows a reference market,
// market.house_liquidity is on for it and market.internal_matching off.
// When the pair cannot be read the order matches users, as before HOUSE.
func (s *Service) houseOnly(ctx context.Context, symbol string) bool {
	if s.Features == nil {
		return false
	}
	pair, err := s.Instruments.Pair(ctx, symbol)
	if err != nil || pair.Reference == "" {
		return false
	}
	subject := flags.Subject{Symbol: symbol}
	return s.Features.Enabled(flags.KeyHouseLiquidity, subject) && !s.Features.Enabled(flags.KeyInternalMatching, subject)
}

// reject stores the order as REJECTED with OrderRejected and returns the
// refusal, its details kept (margin-service's max_borrowable,
// margin_level and the like), with the order's ID.
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
		cur.RejectStatus, cur.RejectDetails = cause.Kind.HTTPStatus(), maps.Clone(cause.Details)
		// A borrow made for the order stays when the freeze is refused; the
		// order keeps it on record.
		cur.Borrowed, cur.BorrowID = o.Borrowed, o.BorrowID
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
	refusal := apperr.New(cause.Kind, cause.Code, cause.Message)
	refusal.Details = maps.Clone(cause.Details)
	return out, refusal.WithDetail("order_id", out.ID)
}

// Recover finishes orders whose freeze outcome was not recorded (§11.1
// step 5): a crash or a ledger outage between storing an order and
// recording its freeze. The freeze is retried with the same key, so an
// order the ledger already froze is accepted, never frozen twice. It
// returns how many orders it finished (accepted or rejected) and stops
// when ctx ends (the pass's deadline).
func (s *Service) Recover(ctx context.Context) (int, error) {
	pending, err := s.Store.Read().Orders().PendingFreeze(ctx, s.Now().Add(-10*time.Second), 100)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, o := range pending {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		out, err := s.fund(ctx, o)
		if err != nil && !refused(apperr.From(err)) {
			return n, err
		}
		if out.FreezeState != domain.FreezePending {
			n++
		}
	}
	return n, nil
}

// Pending reports the orders whose freeze outcome is not recorded yet:
// how many, and how long the oldest has waited (zero without any).
func (s *Service) Pending(ctx context.Context) (int, time.Duration, error) {
	n, oldest, err := s.Store.Read().Orders().PendingStats(ctx)
	if err != nil || n == 0 {
		return n, 0, err
	}
	return n, max(s.Now().Sub(oldest), 0), nil
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
