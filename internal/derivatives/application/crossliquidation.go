package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// The cross accounts' liquidation clearance fee (coin-margined design
// 2026-10-06 §0, C68, user decision 2026-10-09): as Binance does, what the
// liquidation of a cross account leaves goes to the insurance fund once
// the account's cross positions are closed and settled, and the account
// ends at zero. What it leaves is counted from the liquidation's own
// money - what the account held at the take-over and what its cross
// positions' fills and funding added or took since (domain.CrossLiquidation)
// - so funds that come in meanwhile, a transfer in or an isolated
// position's margin set free, stay the user's. While a cross account is
// being liquidated (seconds, as a rule) nothing takes from its available
// balance: no cross order, no opening order of either margin mode on the
// contracts settled in its asset, no isolated margin added, no transfer
// out of FUTURES (as Binance's "user in liquidation mode").

// ErrCrossLiquidating refuses what a cross account being liquidated does
// not take.
var ErrCrossLiquidating = apperr.New(apperr.KindConflict, "DERIV_POSITION_LIQUIDATING",
	"the cross account is being liquidated")

// openCrossLiquidation records a cross account's liquidation as its
// positions are taken over, in the take-over's transaction (under the
// user's lock): what the account holds now - its available balance as the
// ledger has it, what its cross orders reserve and its cross positions'
// margin - and its equity at the marks, the most its clearance fee takes.
// An account already being liquidated keeps its record.
func (s *Service) openCrossLiquidation(ctx context.Context, r ports.Repos, userID, asset string, taken []domain.Position,
	equity decimal.Decimal,
) error {
	if open, err := r.CrossLiquidations().Open(ctx, userID, asset); err != nil || open != nil {
		return err
	}
	bal, err := s.Ledger.Balance(ctx, userID, asset)
	if err != nil {
		return err
	}
	held := bal.Available
	unreleased, err := r.Orders().Unreleased(ctx, userID)
	if err != nil {
		return err
	}
	orders, err := s.settledIn(ctx, unreleased, asset)
	if err != nil {
		return err
	}
	for _, o := range orders {
		if o.MarginMode == domain.Cross {
			held = held.Add(o.Unreleased())
		}
	}
	for _, p := range taken {
		held = held.Add(p.Margin)
	}
	l := domain.CrossLiquidation{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Asset: asset, StartedAt: s.Now(), Equity: equity, Balance: held,
		Flows: decimal.Zero, Status: domain.CrossLiquidationOpen,
	}
	if err := r.CrossLiquidations().Insert(ctx, l); err != nil {
		return err
	}
	s.Log.WarnContext(ctx, "cross account liquidation started", "user_id", userID, "asset", asset, "liquidation_id", l.ID,
		"balance", held.String(), "equity", equity.String())
	return nil
}

// crossFlow adds what a cross position's funding payment gave or took
// (flow) to the open liquidation of its account, if any, in the payment's
// transaction. Fills add theirs in applyFill.
func crossFlow(ctx context.Context, r ports.Repos, userID, asset string, flow decimal.Decimal) error {
	l, err := r.CrossLiquidations().Open(ctx, userID, asset)
	if err != nil || l == nil || flow.IsZero() {
		return err
	}
	return r.CrossLiquidations().AddFlow(ctx, l.ID, flow)
}

// takeOverOpened takes over the cross positions a fill leaves open in an
// account being liquidated (liq not nil): a cross order the take-over
// asked the engine to cancel can fill first, and the position it opens or
// adds to would stay open - the account takes no cross order to close it
// - and the liquidation would never end. The liquidation engine closes it
// with the others.
func (s *Service) takeOverOpened(ctx context.Context, liq *domain.CrossLiquidation, changes []domain.PositionChange) {
	if liq == nil {
		return
	}
	for i := range changes {
		p := &changes[i].Position
		if p.MarginMode != domain.Cross || p.Flat() || p.Liquidating {
			continue
		}
		p.Liquidating, p.LiquidationAttempts, p.LiquidationAt = true, 0, s.Now()
		s.step("takeover")
		s.Log.WarnContext(ctx, "position taken over for liquidation", "user_id", p.UserID, "symbol", p.Symbol,
			"position_side", p.Side, "liquidation_id", liq.ID, "reason", "filled while its cross account is liquidated")
	}
}

// parkedCrossFlow adds to the open liquidation of a cross account what a
// fill of it the ledger had refused (parked, its flow counted with the
// outcomes assumed then) turned out to add once booked: the insurance
// fund's part of its loss above all (review C74 ①). Only a fill from the
// take-over on had its flow counted; one parked before it is left as it
// is: the ledger lagged the positions when the liquidation was recorded, an
// insurance-fund outage at the take-over (the runbook says so).
func (s *Service) parkedCrossFlow(ctx context.Context, r ports.Repos, p ports.PendingSettlement, outcomes []domain.Outcome) error {
	l, err := r.CrossLiquidations().Open(ctx, p.UserID, p.Request.Asset)
	if err != nil || l == nil {
		return err
	}
	f, err := r.Fills().Get(ctx, p.TradeID, p.Side)
	if err != nil {
		return err
	}
	if f.ExecutedAt.Before(l.StartedAt) {
		return nil
	}
	o, err := r.Orders().Get(ctx, f.OrderID)
	if errors.Is(err, domain.ErrOrderNotFound) {
		return nil // HOUSE's side
	}
	if err != nil || o.MarginMode != domain.Cross {
		return err
	}
	flow := domain.ParkedFlow(p.Request.Moves, outcomes)
	if flow.IsZero() {
		return nil
	}
	return r.CrossLiquidations().AddFlow(ctx, l.ID, flow)
}

// crossLiquidating refuses what a cross account being liquidated does not
// take.
func crossLiquidating(ctx context.Context, r ports.Repos, userID, asset string) error {
	l, err := r.CrossLiquidations().Open(ctx, userID, asset)
	if err != nil {
		return err
	}
	if l != nil {
		return ErrCrossLiquidating.WithDetail("settle_asset", asset).WithDetail("liquidation_id", l.ID)
	}
	return nil
}

// SettleCrossLiquidations books, every second, the clearance fee of the
// cross accounts whose liquidation is over, and returns how many ended.
func (s *Service) SettleCrossLiquidations(ctx context.Context) (int, error) {
	open, err := s.Store.Read().CrossLiquidations().AllOpen(ctx)
	if err != nil {
		return 0, err
	}
	// How long the oldest has been open (review C74 ②): seconds as a rule;
	// the alert DerivativesCrossLiquidationStuck says when one is not.
	oldest := 0.0
	for _, l := range open {
		oldest = max(oldest, s.Now().Sub(l.StartedAt).Seconds())
	}
	s.Metrics.CrossOpenOldest.Set(oldest)
	if len(open) == 0 {
		return 0, nil
	}
	contracts, err := s.Instruments.Contracts(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, l := range open {
		done, err := s.settleCrossLiquidation(ctx, l, contracts)
		if err != nil {
			errs = append(errs, fmt.Errorf("cross liquidation %s: %w", l.ID, err))
			continue
		}
		if done {
			n++
		}
	}
	return n, errors.Join(errs...)
}

// clearanceKey is the ledger key the clearance fee of a cross liquidation
// is booked under.
func clearanceKey(l domain.CrossLiquidation) string { return "cross-liquidation:" + l.ID }

// settleCrossLiquidation ends one cross liquidation once it is over
// (crossLiquidationOver): its clearance fee worked out against what is
// available now and stored first, so that a booking retried after a crash
// books the same amount under the same key; then booked as the INSURANCE
// move of a settlement (INSURANCE_CONTRIBUTION), and the liquidation DONE
// with CrossLiquidationCompleted. A booking the ledger refuses (less is
// available than the fee: a move out of the account meanwhile) books
// nothing, and the fee is worked out again next time.
func (s *Service) settleCrossLiquidation(ctx context.Context, l domain.CrossLiquidation, contracts []domain.Contract) (bool, error) {
	settled, decimals := map[string]bool{}, int32(8)
	for _, c := range contracts {
		if c.Settle() == l.Asset {
			settled[c.Symbol], decimals = true, c.QuoteDecimals
		}
	}
	ready := false
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, l.UserID); err != nil {
			return err
		}
		cur, err := r.CrossLiquidations().Open(ctx, l.UserID, l.Asset)
		if err != nil || cur == nil || cur.ID != l.ID {
			return err
		}
		over, err := s.crossLiquidationOver(ctx, r, l.UserID, settled)
		if err != nil || !over {
			return err
		}
		if !cur.Fee.Valid {
			bal, err := s.Ledger.Balance(ctx, l.UserID, l.Asset)
			if err != nil {
				return err
			}
			cur.Fee = decimal.NullDecimal{Decimal: cur.ClearanceFee(bal.Available, decimals), Valid: true}
			if err := r.CrossLiquidations().Update(ctx, *cur); err != nil {
				return err
			}
		}
		l, ready = *cur, true
		return nil
	})
	if err != nil || !ready {
		return false, err
	}
	fee := l.Fee.Decimal
	if fee.IsPositive() {
		_, err := s.Ledger.Settle(ctx, ports.SettleRequest{
			IdemKey: clearanceKey(l), UserID: l.UserID, Asset: l.Asset,
			Reference: fmt.Sprintf("cross liquidation %s: what it left, to the insurance fund", l.ID),
			Moves:     []domain.Move{{Type: domain.MoveInsurance, Amount: fee}},
		})
		switch {
		case err == nil:
		case refused(err) && !apperr.Is(err, apperr.CodeIdempotencyConflict):
			s.Log.WarnContext(ctx, "cross liquidation fee refused; worked out again", "liquidation_id", l.ID, "fee", fee.String(),
				"error", err)
			l.Fee = decimal.NullDecimal{}
			return false, s.Store.Tx(ctx, func(r ports.Repos) error { return r.CrossLiquidations().Update(ctx, l) })
		default:
			return false, err
		}
	}
	now := s.Now()
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, l.UserID); err != nil {
			return err
		}
		l.Status, l.DoneAt = domain.CrossLiquidationDone, now
		if err := r.CrossLiquidations().Update(ctx, l); err != nil {
			return err
		}
		return r.Emit(ctx, event.TopicDerivLiquidation, &derivativesv1.CrossLiquidationCompleted{
			LiquidationId: l.ID, UserId: l.UserID, SettleAsset: l.Asset, ClearanceFee: fee.String(),
			StartedAt: timestamppb.New(l.StartedAt), CompletedAt: timestamppb.New(now),
		}, "user", l.UserID)
	})
	if err != nil {
		return false, err
	}
	s.step("cleared")
	s.Log.WarnContext(ctx, "cross account liquidation over", "user_id", l.UserID, "asset", l.Asset, "liquidation_id", l.ID,
		"left", l.Left().String(), "clearance_fee", fee.String())
	return true, nil
}

// crossLiquidationOver reports whether a cross account's liquidation is
// over: no cross position on the contracts settled in its asset still
// open, no cross order on them active or still holding a reservation, no
// settlement of the user waiting on the ledger.
func (s *Service) crossLiquidationOver(ctx context.Context, r ports.Repos, userID string, settled map[string]bool) (bool, error) {
	held, err := r.Positions().OfUser(ctx, userID, "")
	if err != nil {
		return false, err
	}
	for _, p := range held {
		if settled[p.Symbol] && p.MarginMode == domain.Cross && !p.Flat() {
			return false, nil
		}
	}
	active, err := r.Orders().Active(ctx, userID, "")
	if err != nil {
		return false, err
	}
	unreleased, err := r.Orders().Unreleased(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, o := range append(active, unreleased...) {
		if settled[o.Symbol] && o.MarginMode == domain.Cross {
			return false, nil
		}
	}
	n, err := r.Pending().CountOf(ctx, userID)
	return err == nil && n == 0, err
}
