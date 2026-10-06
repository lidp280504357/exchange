package application

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
)

// The steps of a liquidation (design §4.5), each resumed where it stopped:
// the account's orders canceled until nothing is locked; what it holds
// beyond its debts sold to HOUSE for the quote asset; what it lacks of the
// assets it owes bought with the quote; the fee charged; its debts repaid
// from what it holds; the rest covered by the insurance fund (review CY
// (b): the fee first, then the repayments, then the fund); the account
// freed with what is left.
const (
	StepCancel = "CANCEL"
	StepSell   = "SELL"
	StepBuy    = "BUY"
	StepRepay  = "REPAY"
	StepCover  = "COVER"
	StepFee    = "FEE"
	StepSettle = "SETTLE"
	StepDone   = "DONE"
)

// ErrNothingOwed refuses to liquidate an account that owes nothing.
var ErrNothingOwed = apperr.New(apperr.KindConflict, "MARGIN_NOTHING_OWED", "the account owes nothing")

// settleWait is how long a trading step waits after it began before it
// reads the account: the trading service freezes as it takes an order,
// and the settlement moves the freeze.
const settleWait = 2 * time.Second

// buyBuffer is what a buy's quote amount adds to the shortfall at the
// price: what is left over stays in the account.
var buyBuffer = decimal.RequireFromString("1.02")

// AutoLiquidate is the monitor's hand-over of an account at its
// liquidation level two passes in a row: liquidated when
// margin.liquidation is on for its user, else only counted (it stays
// warned).
func (s *Service) AutoLiquidate(ctx context.Context, st ports.Account, _ domain.Valuation) error {
	if s.Features == nil || !s.Features.Enabled(flags.KeyMarginLiquidation, flags.Subject{UserID: st.UserID}) {
		return nil
	}
	_, err := s.StartLiquidation(ctx, st.UserID, st.Account, ports.TriggerAuto, "", "")
	return err
}

// StartLiquidation starts an account's liquidation: AUTO from the monitor,
// MANUAL for an administrators' approval (approvalID, asked by by; the
// same approval again returns the liquidation it started). The account is
// LIQUIDATING from now to the end (no orders, borrowing or transfers) and
// MarginLiquidationStarted goes out. An account owing nothing answers
// MARGIN_NOTHING_OWED, one being liquidated MARGIN_FROZEN.
func (s *Service) StartLiquidation(ctx context.Context, userID string, a domain.Account, trigger, approvalID, by string) (ports.Liquidation, error) {
	r := s.Store.Read()
	if approvalID != "" {
		if l, ok, err := r.Liquidations().ByApproval(ctx, approvalID); err != nil || ok {
			return l, err
		}
	}
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return ports.Liquidation{}, err
	}
	terms, err := cat.Terms(a)
	if err != nil {
		terms = domain.DefaultTerms(a.Type, 3)
	}
	quote := domain.ValueAsset
	if !a.IsCross() {
		info, err := s.Instruments.Pair(ctx, a.Symbol)
		if err != nil {
			return ports.Liquidation{}, err
		}
		quote = info.Quote
	}
	all, err := s.Ledger.Holdings(ctx, userID)
	if err != nil {
		return ports.Liquidation{}, err
	}
	v := domain.Value(all[a], cat.Assets, s.Prices.Prices())
	if !v.HasDebt() {
		return ports.Liquidation{}, ErrNothingOwed
	}
	var out ports.Liquidation
	started := false
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		started = false
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		st, ok, err := r.Accounts().Get(ctx, userID, a)
		if err != nil {
			return err
		}
		if !ok {
			return ErrAccountNotFound
		}
		if st.Status == domain.StatusLiquidating {
			running, ok, err := r.Liquidations().RunningOf(ctx, userID, a)
			if err != nil {
				return err
			}
			if ok && (approvalID == "" || running.ApprovalID == approvalID) {
				out = running
				return nil
			}
			return domain.ErrFrozen.WithDetail("status", string(st.Status))
		}
		now := s.Now()
		prior := domain.StatusNormal
		if st.Status == domain.StatusFrozen {
			prior = domain.StatusFrozen
		}
		out = ports.Liquidation{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Account: a, Trigger: trigger, ApprovalID: approvalID, RequestedBy: by,
			Status: ports.LiquidationStarted, Step: StepCancel, PriorStatus: prior, TotalAsset: v.TotalAsset,
			TotalLiability: v.TotalLiability, FeeRate: terms.LiquidationFee, QuoteAsset: quote, QuoteMark: decimal.Zero,
			Traded: decimal.Zero, Fee: decimal.Zero, FeeUSDT: decimal.Zero, InsuranceCovered: decimal.Zero, StartedAt: now, StepAt: now,
		}
		if level, ok := v.Level(); ok {
			out.MarginLevel = &level
		}
		if err := r.Liquidations().Insert(ctx, out); err != nil {
			return err
		}
		st.Status, st.UpdatedAt = domain.StatusLiquidating, now
		if err := r.Accounts().Update(ctx, st); err != nil {
			return err
		}
		started = true
		return r.Emit(ctx, event.TopicMargin, &marginv1.MarginLiquidationStarted{
			LiquidationId: out.ID, UserId: userID, AccountType: string(a.Type), Symbol: a.Symbol, MarginLevel: levelText(out.MarginLevel),
			TotalAsset: out.TotalAsset.String(), TotalLiability: out.TotalLiability.String(), StartedAt: timestamppb.New(now),
			Trigger: trigger, ApprovalId: approvalID,
		}, "user", userID)
	})
	if err != nil {
		return ports.Liquidation{}, err
	}
	if started && s.Metrics != nil {
		s.Metrics.Liquidations.WithLabelValues(trigger).Inc()
	}
	s.touch(userID)
	return out, nil
}

func levelText(l *decimal.Decimal) string {
	if l == nil {
		return ""
	}
	return l.String()
}

// AdvanceLiquidations moves every liquidation under way as far as it can
// now; it returns how many there are.
func (s *Service) AdvanceLiquidations(ctx context.Context) (int, error) {
	running, err := s.Store.Read().Liquidations().Running(ctx)
	if err != nil {
		return 0, err
	}
	if s.Metrics != nil {
		oldest, short := 0.0, 0
		if len(running) > 0 {
			oldest = s.Now().Sub(running[0].StartedAt).Seconds()
		}
		for _, l := range running {
			if l.Status == ports.LiquidationShortfall {
				short++
			}
		}
		s.Metrics.LiquidationOldest.Set(oldest)
		s.Metrics.LiquidationsShort.Set(float64(short))
	}
	var errs []error
	for _, l := range running {
		for range 8 { // a step that is done moves to the next at once
			next, moved, err := s.step(ctx, l)
			if err != nil {
				errs = append(errs, fmt.Errorf("liquidation %s at %s: %w", l.ID, l.Step, err))
			}
			if err != nil || !moved || next.Step == StepDone {
				break
			}
			l = next
		}
	}
	return len(running), errors.Join(errs...)
}

// save stores a liquidation's progress; note says why it waits.
func (s *Service) save(ctx context.Context, l ports.Liquidation) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error { return r.Liquidations().Update(ctx, l) })
}

// wait records why a liquidation waits, once.
func (s *Service) wait(ctx context.Context, l ports.Liquidation, note string) (ports.Liquidation, bool, error) {
	if l.Note == note {
		return l, false, nil
	}
	l.Note = note
	return l, false, s.save(ctx, l)
}

// next moves a liquidation to its next step.
func (s *Service) next(ctx context.Context, l ports.Liquidation, step string) (ports.Liquidation, bool, error) {
	l.Step, l.StepAt, l.Note = step, s.Now(), ""
	return l, true, s.save(ctx, l)
}

// step runs a liquidation's current step; moved reports that it is done.
func (s *Service) step(ctx context.Context, l ports.Liquidation) (ports.Liquidation, bool, error) {
	switch l.Step {
	case StepCancel:
		return s.stepCancel(ctx, l)
	case StepSell, StepBuy:
		return s.stepTrade(ctx, l)
	case StepRepay:
		return s.stepRepay(ctx, l, false)
	case StepCover:
		return s.stepRepay(ctx, l, true)
	case StepFee:
		return s.stepFee(ctx, l)
	case StepSettle:
		return s.stepSettle(ctx, l)
	}
	return l, false, nil
}

// held returns what the liquidated account holds and owes now.
func (s *Service) held(ctx context.Context, l ports.Liquidation) ([]domain.Holding, error) {
	all, err := s.Ledger.Holdings(ctx, l.UserID)
	if err != nil {
		return nil, err
	}
	return all[l.Account], nil
}

func locked(list []domain.Holding) bool {
	return slices.ContainsFunc(list, func(h domain.Holding) bool { return h.Locked.IsPositive() })
}

// stepCancel asks for the account's orders to be canceled until nothing
// is locked, then plans the sales.
func (s *Service) stepCancel(ctx context.Context, l ports.Liquidation) (ports.Liquidation, bool, error) {
	holdings, err := s.held(ctx, l)
	if err != nil {
		return l, false, err
	}
	if locked(holdings) {
		if err := s.Trading.CancelAccount(ctx, l.UserID, l.Account); err != nil && !refused(err) {
			return l, false, err
		}
		return s.wait(ctx, l, "waiting for the account's orders to be canceled")
	}
	if err := s.addOrders(ctx, s.planSells(ctx, l, holdings)); err != nil {
		return l, false, err
	}
	l.QuoteMark = holding(holdings, l.QuoteAsset).Free
	return s.next(ctx, l, StepSell)
}

func (s *Service) addOrders(ctx context.Context, orders []ports.LiquidationOrder) error {
	if len(orders) == 0 {
		return nil
	}
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		for _, o := range orders {
			if err := r.Liquidations().AddOrder(ctx, o); err != nil {
				return err
			}
		}
		return nil
	})
}

// pairOf returns the pair an asset trades against the quote on: the
// isolated account's own, or <asset>-<quote>; ok is false when it does
// not trade now.
func (s *Service) pairOf(ctx context.Context, l ports.Liquidation, asset string) (ports.PairInfo, bool) {
	symbol := asset + "-" + l.QuoteAsset
	if !l.Account.IsCross() {
		symbol = l.Account.Symbol
	}
	info, err := s.Instruments.Pair(ctx, symbol)
	if err != nil || info.Status != "TRADING" || info.Base != asset || info.Quote != l.QuoteAsset {
		return ports.PairInfo{}, false
	}
	return info, true
}

// planSells sells what the account holds of each asset beyond its own
// debt, in whole lots, for the quote asset.
func (s *Service) planSells(ctx context.Context, l ports.Liquidation, holdings []domain.Holding) []ports.LiquidationOrder {
	var out []ports.LiquidationOrder
	for _, h := range holdings {
		surplus := h.Free.Sub(h.Debt())
		if h.Asset == l.QuoteAsset || !surplus.IsPositive() {
			continue
		}
		info, ok := s.pairOf(ctx, l, h.Asset)
		if !ok || !info.Lot.IsPositive() {
			continue // it stays in the account
		}
		qty := surplus.Div(info.Lot).Floor().Mul(info.Lot)
		if info.MaxQuantity.IsPositive() && qty.GreaterThan(info.MaxQuantity) {
			qty = info.MaxQuantity.Div(info.Lot).Floor().Mul(info.Lot) // one order a side; the rest stays
		}
		if qty.IsPositive() {
			out = append(out, ports.LiquidationOrder{
				LiquidationID: l.ID, Symbol: info.Symbol, Side: "SELL", Quantity: qty, QuoteAmount: decimal.Zero,
				Status: ports.OrderPlanned, CreatedAt: s.Now(),
			})
		}
	}
	return out
}

// planBuys buys what the account lacks of each asset it owes with the
// quote asset left beyond the quote's own debt, the largest shortfall
// first, at the price and buyBuffer more.
func (s *Service) planBuys(ctx context.Context, l ports.Liquidation, holdings []domain.Holding) ([]ports.LiquidationOrder, error) {
	q := holding(holdings, l.QuoteAsset)
	budget := q.Free.Sub(q.Debt())
	if !budget.IsPositive() {
		return nil, nil
	}
	decimals, err := s.Instruments.Decimals(ctx, l.QuoteAsset)
	if err != nil {
		return nil, err
	}
	prices := s.Prices.Prices()
	quotePrice, ok := prices.Of(l.QuoteAsset)
	if !ok {
		return nil, nil
	}
	type need struct {
		info   ports.PairInfo
		amount decimal.Decimal // in the quote
	}
	var needs []need
	for _, h := range holdings {
		short := h.Debt().Sub(h.Free)
		if h.Asset == l.QuoteAsset || !short.IsPositive() {
			continue
		}
		info, ok := s.pairOf(ctx, l, h.Asset)
		p, priced := prices.Of(h.Asset)
		if !ok || !priced {
			continue // the insurance fund covers it
		}
		amount := short.Mul(p.Value).DivRound(quotePrice.Value, 18).Mul(buyBuffer).RoundCeil(decimals)
		needs = append(needs, need{info: info, amount: amount})
	}
	slices.SortFunc(needs, func(a, b need) int { return b.amount.Cmp(a.amount) })
	var out []ports.LiquidationOrder
	for _, n := range needs {
		amount := decimal.Min(n.amount, budget)
		if !amount.IsPositive() {
			break
		}
		budget = budget.Sub(amount)
		out = append(out, ports.LiquidationOrder{
			LiquidationID: l.ID, Symbol: n.info.Symbol, Side: "BUY", Quantity: decimal.Zero, QuoteAmount: amount,
			Status: ports.OrderPlanned, CreatedAt: s.Now(),
		})
	}
	return out, nil
}

// stepTrade sends the step's orders until the trading service took or
// refused each, waits until the account has nothing locked, counts what
// they traded of the quote asset and plans the next step.
func (s *Service) stepTrade(ctx context.Context, l ports.Liquidation) (ports.Liquidation, bool, error) {
	side := "SELL"
	if l.Step == StepBuy {
		side = "BUY"
	}
	orders, err := s.Store.Read().Liquidations().Orders(ctx, l.ID)
	if err != nil {
		return l, false, err
	}
	for _, o := range orders {
		if o.Side != side || o.Status != ports.OrderPlanned {
			continue
		}
		id, err := s.Trading.PlaceLiquidation(ctx, l.UserID, l.Account, o)
		switch {
		case err == nil:
			o.OrderID, o.Status = id, ports.OrderSent
		case refused(err):
			o.Status, o.Error = ports.OrderRefused, failureText(apperr.From(err))
			s.Log.WarnContext(ctx, "the trading service refused a liquidation order", "liquidation_id", l.ID, "symbol", o.Symbol,
				"side", o.Side, "error", o.Error)
		default:
			return s.wait(ctx, l, "sending the orders: "+apperr.From(err).Message)
		}
		if err := s.Store.Tx(ctx, func(r ports.Repos) error { return r.Liquidations().SetOrder(ctx, o) }); err != nil {
			return l, false, err
		}
	}
	if s.Now().Sub(l.StepAt) < settleWait {
		return l, false, nil
	}
	holdings, err := s.held(ctx, l)
	if err != nil {
		return l, false, err
	}
	if locked(holdings) {
		return s.wait(ctx, l, "waiting for the orders to settle")
	}
	now := holding(holdings, l.QuoteAsset).Free
	if l.Step == StepSell {
		l.Traded = l.Traded.Add(decimal.Max(now.Sub(l.QuoteMark), decimal.Zero))
		buys, err := s.planBuys(ctx, l, holdings)
		if err != nil {
			return l, false, err
		}
		if err := s.addOrders(ctx, buys); err != nil {
			return l, false, err
		}
		l.QuoteMark = now
		return s.next(ctx, l, StepBuy)
	}
	l.Traded = l.Traded.Add(decimal.Max(l.QuoteMark.Sub(now), decimal.Zero))
	return s.next(ctx, l, StepFee)
}

// repayKey names a liquidation's repayment of an asset: from the account
// (repay), or the insurance fund's n-th attempt (cover).
func repayKey(l ports.Liquidation, cover bool, asset string, n int) string {
	if cover {
		return fmt.Sprintf("liquidation:%s:cover:%s:%d", l.ID, asset, n)
	}
	return fmt.Sprintf("liquidation:%s:repay:%s", l.ID, asset)
}

// coverRetry is how long a refused cover (the fund short) waits before
// the next attempt.
const coverRetry = time.Minute

// stepRepay repays each debt from what the account holds (repay), or the
// insurance fund pays what is left of it (cover), interest first, as
// liquidation repayments (MARGIN_LIQUIDATE); one waiting to be booked
// holds the step, recovery finishes it.
func (s *Service) stepRepay(ctx context.Context, l ports.Liquidation, cover bool) (ports.Liquidation, bool, error) {
	holdings, err := s.held(ctx, l)
	if err != nil {
		return l, false, err
	}
	waiting, short := "", false
	for _, h := range holdings {
		debt := h.Debt()
		amount := decimal.Min(h.Free, debt)
		if cover {
			amount = debt
		}
		if !amount.IsPositive() {
			continue
		}
		p, err := s.liquidationRepay(ctx, l, h, amount, cover)
		if err != nil {
			return l, false, err
		}
		failed := p.Failure
		if p.Status == ports.OpPending {
			_, err := s.postRepay(ctx, p)
			switch {
			case err == nil:
				continue
			case errors.Is(err, errInProgress):
				waiting = "waiting for the ledger to book the repayments"
				continue
			case !refused(err):
				return l, false, err
			}
			failed = failureText(apperr.From(err))
		} else if p.Status != ports.OpFailed {
			continue
		}
		if cover {
			waiting, short = fmt.Sprintf("the insurance fund could not cover %s %s (%s); retried every minute", amount, h.Asset, failed), true
		} else {
			s.Log.WarnContext(ctx, "the ledger refused a liquidation's repayment; the insurance fund covers the debt", "liquidation_id", l.ID,
				"asset", h.Asset, "error", failed)
		}
	}
	if short != (l.Status == ports.LiquidationShortfall) && cover {
		// SHORTFALL while the fund lacks an asset: the debt stays, owed,
		// and InsuranceFundShort calls an operator (review CY (a)).
		l.Status = ports.LiquidationStarted
		if short {
			l.Status = ports.LiquidationShortfall
		}
		l.Note = waiting
		if err := s.save(ctx, l); err != nil {
			return l, false, err
		}
	}
	if waiting != "" {
		return s.wait(ctx, l, waiting)
	}
	if cover {
		return s.next(ctx, l, StepSettle)
	}
	return s.next(ctx, l, StepCover)
}

// liquidationRepay returns the liquidation's repayment of a holding's
// debt, storing it PENDING the first time: a refused cover is tried again
// under a new key after coverRetry, and what accrued after a cover (an
// hour's interest) gets a cover of its own.
func (s *Service) liquidationRepay(ctx context.Context, l ports.Liquidation, h domain.Holding, amount decimal.Decimal, cover bool) (ports.Repay, error) {
	r := s.Store.Read()
	n := 0
	for {
		p, ok, err := r.Repays().ByKey(ctx, l.UserID, repayKey(l, cover, h.Asset, n))
		if err != nil {
			return ports.Repay{}, err
		}
		if !ok {
			break
		}
		if cover && p.Status == ports.OpDone {
			n++ // covered before; this is what accrued since (an hour's interest)
			continue
		}
		if p.Status != ports.OpFailed || !cover || s.Now().Sub(p.DoneAt) < coverRetry {
			return p, nil
		}
		n++
	}
	key := repayKey(l, cover, h.Asset, n)
	toInterest := decimal.Min(amount, h.Interest)
	hash := sha256.Sum256([]byte(key + "|" + amount.String()))
	p := ports.Repay{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: l.UserID, Account: l.Account, Asset: h.Asset, Interest: toInterest,
		Principal: amount.Sub(toInterest), Reason: ports.RepayLiquidation, LiquidationID: l.ID, IdemKey: key, RequestHash: hash[:],
		Status: ports.OpPending, CreatedAt: s.Now(),
	}
	if cover {
		p.CoveredBy = ports.CoveredByInsurance
	}
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, l.UserID); err != nil {
			return err
		}
		if prior, ok, err := r.Repays().ByKey(ctx, l.UserID, key); err != nil || ok {
			p = prior
			return err
		}
		return r.Repays().Insert(ctx, p)
	})
	return p, err
}

// stepFee charges the fee: the liquidation fee rate of what the orders
// traded, in the quote asset, at most what the account holds of it; the
// amount is stored before it is booked, so that a retry books the same.
func (s *Service) stepFee(ctx context.Context, l ports.Liquidation) (ports.Liquidation, bool, error) {
	if l.Fee.IsZero() {
		holdings, err := s.held(ctx, l)
		if err != nil {
			return l, false, err
		}
		decimals, err := s.Instruments.Decimals(ctx, l.QuoteAsset)
		if err != nil {
			return l, false, err
		}
		fee := decimal.Min(l.Traded.Mul(l.FeeRate).RoundFloor(decimals), holding(holdings, l.QuoteAsset).Free)
		if !fee.IsPositive() {
			return s.next(ctx, l, StepRepay)
		}
		l.Fee = fee
		if p, ok := s.Prices.Prices().Of(l.QuoteAsset); ok {
			l.FeeUSDT = fee.Mul(p.Value).Round(8)
		}
		if err := s.save(ctx, l); err != nil {
			return l, false, err
		}
	}
	_, err := s.Ledger.Post(ctx, ports.Posting{
		IdemKey: "liquidation-fee:" + l.ID, UserID: l.UserID, Account: l.Account, Reference: "liquidation " + l.ID,
		Moves: []ports.Move{{Type: domain.MoveLiquidationFee, Asset: l.QuoteAsset, Amount: l.Fee}},
	})
	switch {
	case err == nil:
	case refused(err):
		s.Log.WarnContext(ctx, "the ledger refused a liquidation fee; none is charged", "liquidation_id", l.ID, "error", err)
		l.Fee, l.FeeUSDT = decimal.Zero, decimal.Zero
	default:
		return s.wait(ctx, l, "waiting for the ledger to book the fee")
	}
	return s.next(ctx, l, StepRepay)
}

// stepSettle completes a liquidation: what it repaid (by the account and
// the fund) and what stayed, the account back to its status before
// (NORMAL, or FROZEN when an administrator had frozen it), and
// MarginLiquidationCompleted.
func (s *Service) stepSettle(ctx context.Context, l ports.Liquidation) (ports.Liquidation, bool, error) {
	holdings, err := s.held(ctx, l)
	if err != nil {
		return l, false, err
	}
	if slices.ContainsFunc(holdings, func(h domain.Holding) bool { return h.Debt().IsPositive() }) {
		return s.next(ctx, l, StepCover) // never completed owing: back to the fund
	}
	repays, err := s.Store.Read().Repays().OfLiquidation(ctx, l.ID)
	if err != nil {
		return l, false, err
	}
	prices := s.Prices.Prices()
	repaid := map[string]decimal.Decimal{}
	covered := decimal.Zero
	for _, p := range repays {
		if p.Status != ports.OpDone {
			continue
		}
		amount := p.Interest.Add(p.Principal)
		repaid[p.Asset] = repaid[p.Asset].Add(amount)
		if p.CoveredBy == ports.CoveredByInsurance {
			if price, ok := prices.Of(p.Asset); ok {
				covered = covered.Add(amount.Mul(price.Value))
			}
		}
	}
	l.Repaid = nil
	for _, asset := range sortedKeys(repaid) {
		l.Repaid = append(l.Repaid, ports.AssetAmount{Asset: asset, Amount: repaid[asset]})
	}
	l.Remaining = nil
	for _, h := range sorted(holdings) {
		if h.Total().IsPositive() {
			l.Remaining = append(l.Remaining, ports.AssetAmount{Asset: h.Asset, Amount: h.Total()})
		}
	}
	l.InsuranceCovered = covered.RoundCeil(8)
	now := s.Now()
	l.Status, l.Step, l.CompletedAt, l.StepAt, l.Note = ports.LiquidationCompleted, StepDone, now, now, ""
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, l.UserID); err != nil {
			return err
		}
		if cur, err := r.Liquidations().GetForUpdate(ctx, l.ID); err != nil || cur.Status == ports.LiquidationCompleted {
			return err
		}
		if err := r.Liquidations().Update(ctx, l); err != nil {
			return err
		}
		st, ok, err := r.Accounts().Get(ctx, l.UserID, l.Account)
		if err != nil {
			return err
		}
		if ok && st.Status == domain.StatusLiquidating {
			st.Status, st.UpdatedAt = l.PriorStatus, now
			if st.Status != domain.StatusWarned {
				st.WarnedAt = time.Time{}
			}
			if err := r.Accounts().Update(ctx, st); err != nil {
				return err
			}
		}
		return r.Emit(ctx, event.TopicMargin, &marginv1.MarginLiquidationCompleted{
			LiquidationId: l.ID, UserId: l.UserID, AccountType: string(l.Account.Type), Symbol: l.Account.Symbol,
			Repaid: assetAmounts(l.Repaid), Fee: l.FeeUSDT.String(), InsuranceCovered: l.InsuranceCovered.String(),
			Remaining: assetAmounts(l.Remaining), CompletedAt: timestamppb.New(now), Trigger: l.Trigger, ApprovalId: l.ApprovalID,
			MarginLevel: levelText(l.MarginLevel), TotalAsset: l.TotalAsset.String(), TotalLiability: l.TotalLiability.String(),
			StartedAt: timestamppb.New(l.StartedAt),
		}, "user", l.UserID)
	})
	if err != nil {
		return l, false, err
	}
	s.touch(l.UserID)
	return l, true, nil
}

func sortedKeys(m map[string]decimal.Decimal) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.SortFunc(out, cmp.Compare[string])
	return out
}

func assetAmounts(list []ports.AssetAmount) []*marginv1.AssetAmount {
	out := make([]*marginv1.AssetAmount, 0, len(list))
	for _, a := range list {
		out = append(out, &marginv1.AssetAmount{Asset: a.Asset, Amount: a.Amount.String()})
	}
	return out
}

// Liquidations lists a page of the user's liquidations, newest first.
func (s *Service) Liquidations(ctx context.Context, userID string, a *domain.Account, before string, limit int) ([]ports.Liquidation, string, error) {
	if limit <= 0 {
		limit = 20
	}
	list, err := s.Store.Read().Liquidations().OfUser(ctx, userID, a, before, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].ID
	}
	return list, next, nil
}
