package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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

// repayGrace is how long after an order that borrowed ends its repayment
// waits (B160): its trades' settlement gives back what a limit price held
// beyond the trade prices, and the ledger settles them on its own.
// RecoverReleases repays it, past this (its cutoff is as long); the ledger
// still refuses while the order's settled trades fall short of what it
// filled (B163), and the next pass tries again, for as long as it takes
// (alert TradingOrderReleasesStuck). Past repayWait its trades the ledger
// parked as FAILED, which settle only once their cause is fixed, no longer
// hold it (B164): the repayment then takes what the account holds. A
// refusal within repayQuiet of the order's end is logged at Debug (a
// settlement some seconds behind is routine), later ones at Warn.
const (
	repayGrace = 10 * time.Second
	repayQuiet = 5 * time.Minute
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
		skipFailed := s.Now().Sub(o.UpdatedAt) > repayWait // B164
		call, cancel := s.bounded(ctx)
		repaid, err := s.Ledger.RepayReleased(call, o.Account(), o.FrozenAsset, repay, o.FilledQuantity, skipFailed, o.ID)
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
// its trades' settlement), the longest untried first. One that fails is
// sent to the back (B164) and left for a later pass while the others go
// on (B163); a repayment still waiting within repayQuiet of its order's
// end is no failure. It returns how many it released and the first
// failure.
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
		err := s.release(ctx, o)
		if err == nil {
			s.releaseWarns.forget(o.ID)
			n++
			continue
		}
		if aerr := s.Store.Tx(ctx, func(r ports.Repos) error { return r.Orders().ReleaseAttempted(ctx, o.ID, s.Now()) }); aerr != nil {
			s.Log.WarnContext(ctx, "recording a release attempt failed", "order_id", o.ID, "error", aerr)
		}
		if apperr.Is(err, codeTradesUnsettled) && s.Now().Sub(o.UpdatedAt) < repayQuiet {
			s.Log.DebugContext(ctx, "an order's repayment waits for its trades' settlement", "order_id", o.ID, "error", err)
			continue
		}
		level := slog.LevelDebug
		if s.releaseWarns.due(o.ID, s.Now()) {
			level = slog.LevelWarn
		}
		s.Log.Log(ctx, level, "an order's release did not complete; a later pass tries again (logged at Warn once a minute)",
			"order_id", o.ID, "error", err)
		failed++
		if first == nil {
			first = err
		}
	}
	if first != nil {
		return n, fmt.Errorf("%d of %d releases did not complete: %w", failed, len(orders), first)
	}
	return n, nil
}

// codeTradesUnsettled is the ledger's answer while an order's trades are
// not all settled (LEDGER_TRADES_UNSETTLED, B163).
const codeTradesUnsettled = "LEDGER_TRADES_UNSETTLED"

// releaseWarns remembers when each order's failing release was last logged
// at Warn: a pass every 5 seconds would otherwise log each waiting order 12
// times a minute (B166); in between it logs at Debug. The gauges and alert
// TradingOrderReleasesStuck carry the state.
type releaseWarns struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// warnEvery is how often one order's failing release is logged at Warn.
const warnEvery = time.Minute

// due reports whether the order's failure is to be logged at Warn now, and
// if so notes it; orders not seen for ten minutes are forgotten.
func (w *releaseWarns) due(orderID string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		w.last = map[string]time.Time{}
	}
	if at, ok := w.last[orderID]; ok && now.Sub(at) < warnEvery {
		return false
	}
	w.last[orderID] = now
	for id, at := range w.last {
		if now.Sub(at) > 10*warnEvery {
			delete(w.last, id)
		}
	}
	return true
}

// forget drops an order whose release completed.
func (w *releaseWarns) forget(orderID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.last, orderID)
}

// Unreleased counts the finished orders whose release did not complete
// and tells how long ago the longest finished one did (zero without
// any), for the gauges behind alert TradingOrderReleasesStuck (B164).
func (s *Service) Unreleased(ctx context.Context) (int, time.Duration, error) {
	n, oldest, err := s.Store.Read().Orders().UnreleasedStats(ctx)
	if err != nil || n == 0 {
		return n, 0, err
	}
	return n, max(s.Now().Sub(oldest), 0), nil
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
