package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
)

// The contracts' product lines (design 2026-10-07, product switches, K1b):
// a coin-margined contract is on product.coin_m, any other on
// product.usdt_m. A closed line takes what closes and nothing else:
// reduce-only orders and hedge-mode orders against their side (the
// liquidation engine's, a take-profit's or stop-loss's and the console's
// closes among them) and cancels; an opening order or a new take-profit or
// stop-loss is PRODUCT_CLOSED. Its positions, funding, liquidations and the
// reconciliation go on, and so does HOUSE's quoting, which only closes can
// trade with now. The market makers' accounts (FeeFree: the simulated
// market's bots) are not held to it: on the platform coin's perpetual
// their quotes are what a close trades with.

// ProductOf returns the flag of the contract's product line.
func ProductOf(c domain.Contract) string {
	if c.Inverse() {
		return flags.KeyProductCoinM
	}
	return flags.KeyProductUSDTM
}

// ProductKey returns the flag of the contracts' product line named as the
// API names it: usdt_m or coin_m.
func ProductKey(name string) (string, error) {
	for _, key := range []string{flags.KeyProductUSDTM, flags.KeyProductCoinM} {
		if flags.ProductNames[key] == name {
			return key, nil
		}
	}
	return "", apperr.NotFound("no such contract product line: usdt_m or coin_m")
}

// closedTo reports whether the contract's product line is closed to the
// user's opening orders.
func (s *Service) closedTo(c domain.Contract, userID string) bool {
	return s.Features != nil && s.Features.Closed(ProductOf(c)) && !s.marketMaker(userID)
}

func (s *Service) marketMaker(userID string) bool { return slices.Contains(s.FeeFree, userID) }

// ProductClosed reports whether the product line of key is closed.
func (s *Service) ProductClosed(key string) bool { return s.Features != nil && s.Features.Closed(key) }

// ErrProductOpen refuses to cancel the orders of a product line that is
// open: the console closes it first (as spot-trading-service refuses).
var ErrProductOpen = apperr.New(apperr.KindConflict, apperr.CodeConflict, "the product line is open: its orders are not canceled")

// ProductOrder is an order, or a take-profit or stop-loss (Conditional),
// that a product line's closing canceled.
type ProductOrder struct {
	ID          string
	UserID      string
	Symbol      string
	Conditional bool
}

// sweepActor is who cancels what SweepClosed finds.
const sweepActor = "system:derivatives-service"

// sweepFrom is how long before a line closed SweepClosed looks: an order
// taken as it closed (by a call that read the flag before the change, at
// most flags.RefreshInterval stale) was created after that. The flag's
// time is the database's clock, the order's this service's: the rest is
// room for the two to differ (review C60 ②).
const sweepFrom = 30 * time.Second

// CancelProduct cancels what a closed product line holds open as the
// console closes it: the users' orders on its contracts (the liquidation
// engine's, ADL's and the console's closes stay, and so do the market
// makers') and their take-profits and stop-losses, each audited as
// admin.orders.canceled by actor. The flags are read again first, so no
// opening order is taken once it returns; one taken just before is
// canceled here or by SweepClosed. Calling it again cancels what is open
// by then.
func (s *Service) CancelProduct(ctx context.Context, key, actor, reason string) ([]ProductOrder, error) {
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return nil, apperr.Invalid("an actor and a reason of at least 3 characters are required")
	}
	if s.Features == nil {
		return nil, ErrProductOpen
	}
	if err := s.Features.Refresh(ctx); err != nil {
		return nil, apperr.Unavailable(err)
	}
	if !s.Features.Closed(key) {
		return nil, ErrProductOpen
	}
	return s.cancelOn(ctx, key, actor, reason, time.Time{}, false)
}

// SweepClosed cancels what came in on the closed product lines as they
// closed: the users' opening orders and take-profits or stop-losses
// created from sweepFrom before the line closed, taken by a call that read
// the flag just before it changed (closing orders stay, and so does what
// was open before: the console's CancelProduct takes that). It runs every
// few seconds; its cancels are audited as the system's.
func (s *Service) SweepClosed(ctx context.Context) (int, error) {
	if s.Features == nil {
		return 0, nil
	}
	n := 0
	var errs []error
	for _, key := range []string{flags.KeyProductUSDTM, flags.KeyProductCoinM} {
		f, stored := s.Features.Get(key)
		if !stored || f.Enabled {
			continue
		}
		done, err := s.cancelOn(ctx, key, sweepActor, "the product line is closed", f.UpdatedAt.Add(-sweepFrom), true)
		n += len(done)
		errs = append(errs, err)
	}
	return n, errors.Join(errs...)
}

// productSymbols returns the contracts of a product line, whatever their
// status.
func (s *Service) productSymbols(ctx context.Context, key string) ([]string, error) {
	list, err := s.Instruments.Contracts(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range list {
		if ProductOf(c) == key {
			out = append(out, c.Symbol)
		}
	}
	return out, nil
}

// canceled reports whether a product line's closing cancels the order: a
// user's own, a take-profit's or a stop-loss's that is not on its way out
// already.
func canceled(o domain.Order) bool {
	switch o.Kind {
	case domain.KindUser, domain.KindTakeProfit, domain.KindStopLoss:
		return !o.CancelRequested
	}
	return false
}

// cancelOn cancels the users' orders on the product line's contracts
// created from since (only the opening ones with openingOnly) and their
// take-profits and stop-losses created from since: one user at a time
// under their lock, each cancel audited. A user whose cancels fail leaves
// the others to go on (review C60 ①); the call then fails with what it did
// (503 with canceled and failed_users), and a retry takes what is left.
//
// The orders come from the orders_active partial index read whole (its
// first column is the user), the take-profits and stop-losses from
// conditional_orders_active by symbol: active rows only.
func (s *Service) cancelOn(ctx context.Context, key, actor, reason string, since time.Time, openingOnly bool) ([]ProductOrder, error) {
	symbols, err := s.productSymbols(ctx, key)
	if err != nil || len(symbols) == 0 {
		return nil, err
	}
	pick := func(o domain.Order) bool {
		return slices.Contains(symbols, o.Symbol) && canceled(o) && !o.CreatedAt.Before(since) && (!openingOnly || !o.Closing())
	}
	orders, err := s.Store.Read().Orders().ActiveOn(ctx, symbols)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.Read().Conditionals().ActiveOn(ctx, symbols)
	if err != nil {
		return nil, err
	}
	var users []string
	seen := map[string]bool{}
	conds := map[string][]domain.Conditional{}
	add := func(user string) {
		if !seen[user] && !s.marketMaker(user) {
			seen[user] = true
			users = append(users, user)
		}
	}
	for _, o := range orders {
		if pick(o) {
			add(o.UserID)
		}
	}
	for _, c := range all {
		if !c.CreatedAt.Before(since) {
			conds[c.UserID] = append(conds[c.UserID], c)
			add(c.UserID)
		}
	}
	var out []ProductOrder
	var failed []string
	var first error
	for _, user := range users {
		var mine []ProductOrder
		err := s.Store.Tx(ctx, func(r ports.Repos) error {
			mine = nil
			if err := r.LockUser(ctx, user); err != nil {
				return err
			}
			active, err := r.Orders().Active(ctx, user, "")
			if err != nil {
				return err
			}
			for _, o := range active {
				if !pick(o) {
					continue
				}
				if _, err := s.requestCancel(ctx, r, o); err != nil {
					return err
				}
				mine = append(mine, ProductOrder{ID: o.ID, UserID: user, Symbol: o.Symbol})
			}
			for _, c := range conds[user] {
				cur, err := r.Conditionals().Get(ctx, c.ID)
				if err != nil {
					return err
				}
				if cur.Status != domain.ConditionalActive {
					continue
				}
				cur.Status, cur.Reason, cur.UpdatedAt = domain.ConditionalCanceled, flags.CodeProductClosed, s.Now()
				if err := r.Conditionals().Update(ctx, cur); err != nil {
					return err
				}
				mine = append(mine, ProductOrder{ID: cur.ID, UserID: user, Symbol: cur.Symbol, Conditional: true})
			}
			for _, p := range mine {
				if err := auditCancel(ctx, r, p, key, actor, reason); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			s.Log.WarnContext(ctx, "a user's orders on a closed product line not canceled", "product", flags.ProductNames[key],
				"user_id", user, "error", err)
			failed = append(failed, user)
			if first == nil {
				first = err
			}
			continue
		}
		out = append(out, mine...)
	}
	if len(out) > 0 {
		s.Log.InfoContext(ctx, "orders of a closed product line canceled", "product", flags.ProductNames[key], "orders", len(out), "actor", actor)
	}
	if first != nil {
		return out, apperr.Unavailable(first).WithDetail("canceled", len(out)).WithDetail("failed_users", len(failed))
	}
	return out, nil
}

// auditCancel records a cancel of a product line's closing as the console
// records the cancels it asks for (admin.orders.canceled).
func auditCancel(ctx context.Context, r ports.Repos, p ProductOrder, key, actor, reason string) error {
	kind := "ORDER"
	if p.Conditional {
		kind = "CONDITIONAL"
	}
	details, err := json.Marshal(map[string]string{"order_id": p.ID, "symbol": p.Symbol, "product": flags.ProductNames[key], "type": kind})
	if err != nil {
		return err
	}
	return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
		Target: "user:" + p.UserID, Action: "admin.orders.canceled", Actor: actor, Reason: reason, Details: string(details),
	}, "actor", actor)
}

// ProductCounts counts what closing a product line touches now: the users'
// orders it would cancel (take-profits and stop-losses included) and their
// open positions, which stay; neither HOUSE's nor the market makers' count.
func (s *Service) ProductCounts(ctx context.Context, key string) (orders, positions int, err error) {
	symbols, err := s.productSymbols(ctx, key)
	if err != nil || len(symbols) == 0 {
		return 0, 0, err
	}
	user := func(id string) bool { return !s.marketMaker(id) && (s.HouseUser == "" || id != s.HouseUser) }
	active, err := s.Store.Read().Orders().ActiveOn(ctx, symbols)
	if err != nil {
		return 0, 0, err
	}
	for _, o := range active {
		if canceled(o) && user(o.UserID) {
			orders++
		}
	}
	conds, err := s.Store.Read().Conditionals().ActiveOn(ctx, symbols)
	if err != nil {
		return 0, 0, err
	}
	for _, c := range conds {
		if user(c.UserID) {
			orders++
		}
	}
	open, err := s.Store.Read().Positions().Open(ctx, "")
	if err != nil {
		return 0, 0, err
	}
	for _, p := range open {
		if slices.Contains(symbols, p.Symbol) && !p.Qty.IsZero() && user(p.UserID) {
			positions++
		}
	}
	return orders, positions, nil
}
