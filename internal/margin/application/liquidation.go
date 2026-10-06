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
// beyond its debts sold to HOUSE for the quote asset, the most valuable
// first, as much as its debts, the buys and the fee need; the fee
// charged; what it lacks of the assets it owes bought with the quote; its
// debts repaid from what it holds; the rest covered by the insurance fund
// (reviews CY (b), DD C19); the account freed with what is left.
const (
	StepCancel = "CANCEL"
	StepSell   = "SELL"
	StepFee    = "FEE"
	StepBuy    = "BUY"
	StepRepay  = "REPAY"
	StepCover  = "COVER"
	StepSettle = "SETTLE"
	StepDone   = "DONE"
)

// ErrNothingOwed refuses to liquidate an account that owes nothing.
var ErrNothingOwed = apperr.New(apperr.KindConflict, "MARGIN_NOTHING_OWED", "the account owes nothing")

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
			TotalLiability: v.TotalLiability, FeeRate: terms.LiquidationFee, QuoteAsset: quote, Traded: decimal.Zero, Fee: decimal.Zero,
			FeeUSDT: decimal.Zero, InsuranceCovered: decimal.Zero, StartedAt: now, StepAt: now,
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
		for _, l := range running {
			switch {
			case l.Status == ports.LiquidationShortfall:
				short++ // InsuranceFundShort's, not MarginLiquidationStuck's
			case oldest == 0:
				oldest = s.Now().Sub(l.StartedAt).Seconds()
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
	case StepFee:
		return s.stepFee(ctx, l)
	case StepRepay:
		return s.stepRepay(ctx, l, false)
	case StepCover:
		return s.stepRepay(ctx, l, true)
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
// is locked.
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
	return s.next(ctx, l, StepSell)
}

// stepTrade runs the step's orders (SELL or BUY) round by round (review
// DD C19 ①②): it sends each order until the trading service took or
// refused it and reads it again until it executes no more; once all have
// and the account has nothing locked (the ledger settled what they
// executed), it plans the next round from what the account holds now,
// each order the next attempt of its pair. What an order executed is what
// the trading service reports, never read off the balances. The step
// ends when nothing more is needed, or what is needed can never trade.
func (s *Service) stepTrade(ctx context.Context, l ports.Liquidation) (ports.Liquidation, bool, error) {
	side := sideOf(l.Step)
	orders, err := s.Store.Read().Liquidations().Orders(ctx, l.ID)
	if err != nil {
		return l, false, err
	}
	open := false
	for i, o := range orders {
		if o.Side != side || !o.Open() {
			continue
		}
		if orders[i], err = s.send(ctx, l, o); err != nil {
			return s.wait(ctx, l, "sending the orders: "+apperr.From(err).Message)
		}
		open = open || orders[i].Open()
	}
	if open {
		return s.wait(ctx, l, "waiting for the orders to execute")
	}
	holdings, err := s.held(ctx, l)
	if err != nil {
		return l, false, err
	}
	if locked(holdings) {
		return s.wait(ctx, l, "waiting for the ledger to settle the orders")
	}
	l.Traded = executed(orders, "")
	plan, why, err := s.planSells(ctx, l, holdings, orders)
	if side == sideBuy {
		plan, why, err = s.planBuys(ctx, l, holdings, orders)
	}
	switch {
	case err != nil:
		return s.wait(ctx, l, "planning the orders: "+err.Error())
	case len(plan) > 0:
		l.Note = ""
		err := s.Store.Tx(ctx, func(r ports.Repos) error {
			for _, o := range plan {
				if err := r.Liquidations().AddOrder(ctx, o); err != nil {
					return err
				}
			}
			return r.Liquidations().Update(ctx, l)
		})
		return l, err == nil, err // sent at once
	case why != "":
		return s.wait(ctx, l, why)
	case side == sideSell:
		return s.next(ctx, l, StepFee)
	}
	return s.next(ctx, l, StepRepay)
}

const (
	sideSell = "SELL"
	sideBuy  = "BUY"
)

func sideOf(step string) string {
	if step == StepBuy {
		return sideBuy
	}
	return sideSell
}

// send places a liquidation order or reads it again, and stores where it
// stands when that changed: SENT once taken, DONE once it executes no
// more (what it executed with it), REFUSED when refused, as it came or
// later (nothing executed). Only a refusal of kind Invalid, Conflict or
// Unprocessable is one: any other failure (the trading service down, its
// 403 or 404) says nothing of the order, which is read again later.
func (s *Service) send(ctx context.Context, l ports.Liquidation, o ports.LiquidationOrder) (ports.LiquidationOrder, error) {
	was := o
	st, err := s.Trading.PlaceLiquidation(ctx, l.UserID, l.Account, o)
	switch {
	case err == nil:
		o.OrderID, o.OrderStatus, o.FilledQuantity, o.FilledQuote = st.OrderID, st.Status, st.FilledQuantity, st.FilledQuote
		o.Status = ports.OrderSent
		if st.Final() {
			o.Status = ports.OrderDone
		}
	case orderRefused(err):
		e := apperr.From(err)
		o.Status, o.Error, o.FilledQuantity, o.FilledQuote = ports.OrderRefused, failureText(e), decimal.Zero, decimal.Zero
		if id, ok := e.Details["order_id"].(string); ok && id != "" {
			o.OrderID, o.OrderStatus = id, "REJECTED"
		}
		s.Log.WarnContext(ctx, "the trading service refused a liquidation order", "liquidation_id", l.ID, "symbol", o.Symbol,
			"side", o.Side, "attempt", o.Attempt, "error", o.Error)
	default:
		return was, err
	}
	if o.Status == was.Status && o.OrderID == was.OrderID && o.OrderStatus == was.OrderStatus &&
		o.FilledQuantity.Equal(was.FilledQuantity) && o.FilledQuote.Equal(was.FilledQuote) {
		return o, nil
	}
	o.UpdatedAt = s.Now()
	return o, s.Store.Tx(ctx, func(r ports.Repos) error { return r.Liquidations().SetOrder(ctx, o) })
}

// orderRefused tells the trading service's refusal of an order from a
// failure that says nothing of it.
func orderRefused(err error) bool {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Kind {
	case apperr.KindInvalid, apperr.KindConflict, apperr.KindUnprocessable:
		return true
	}
	return false
}

// executed sums what a liquidation's finished orders of a side ("" both)
// executed of the quote asset.
func executed(orders []ports.LiquidationOrder, side string) decimal.Decimal {
	sum := decimal.Zero
	for _, o := range orders {
		if o.Status == ports.OrderDone && (side == "" || o.Side == side) {
			sum = sum.Add(o.FilledQuote)
		}
	}
	return sum
}

// maxOrderAttempts is the most orders of one pair and side a liquidation
// places (spot-trading-service's bound): past it the pair counts as one
// that never trades.
const maxOrderAttempts = 1000

// backoff is how long the next attempt waits after idle attempts in a row
// that executed nothing (or the next repayment after refused ones): at
// once after the first, then 2 seconds doubling, at most a minute.
func backoff(idle int) time.Duration {
	if idle <= 1 {
		return 0
	}
	return min(2*time.Second<<min(idle-2, 5), time.Minute)
}

// tries is where a liquidation's orders of one pair and side stand: the
// latest attempt, and how many in a row up to it executed nothing.
type tries struct {
	last ports.LiquidationOrder
	idle int
}

// triesOf returns the tries of each pair on a side (orders oldest first).
func triesOf(orders []ports.LiquidationOrder, side string) map[string]tries {
	out := map[string]tries{}
	for _, o := range orders {
		t := out[o.Symbol]
		if o.Side != side || o.Attempt < t.last.Attempt {
			continue
		}
		t.last = o
		if t.idle++; o.FilledQuantity.IsPositive() {
			t.idle = 0
		}
		out[o.Symbol] = t
	}
	return out
}

// nextAttempt returns a pair's next attempt and what it still waits; ok
// is false past maxOrderAttempts.
func (s *Service) nextAttempt(t tries) (n int, wait time.Duration, ok bool) {
	if t.last.Attempt == 0 {
		return 1, 0, true
	}
	if t.last.Attempt >= maxOrderAttempts {
		return 0, 0, false
	}
	return t.last.Attempt + 1, backoff(t.idle) - s.Now().Sub(t.last.UpdatedAt), true
}

// waitNote says why a pair's next order waits: the pair not trading, or
// the backoff after attempts that executed nothing.
func waitNote(info ports.PairInfo, t tries) string {
	if info.Status != pairTrading {
		return fmt.Sprintf("waiting for %s to trade (%s)", info.Symbol, info.Status)
	}
	return fmt.Sprintf("%d orders on %s executed nothing; the next one after %s", t.idle, info.Symbol, backoff(t.idle))
}

const pairTrading = "TRADING"

// market returns the pair an asset trades against the quote asset on (the
// isolated account's own, or <asset>-<quote>): ok is false when there is
// none that may ever trade (unknown, CANCEL_ONLY, DELISTED); open tells
// whether it trades now (not while HALT or PREPARE).
func (s *Service) market(ctx context.Context, l ports.Liquidation, asset string) (info ports.PairInfo, open, ok bool, err error) {
	symbol := asset + "-" + l.QuoteAsset
	if !l.Account.IsCross() {
		symbol = l.Account.Symbol
	}
	info, err = s.Instruments.Pair(ctx, symbol)
	var e *apperr.Error
	switch {
	case errors.As(err, &e) && e.Kind == apperr.KindNotFound:
		return info, false, false, nil
	case err != nil:
		return info, false, false, err
	case info.Base != asset || info.Quote != l.QuoteAsset || !info.Lot.IsPositive():
		return info, false, false, nil
	}
	switch info.Status {
	case pairTrading:
		return info, true, true, nil
	case "HALT", "PREPARE":
		return info, false, true, nil
	}
	return info, false, false, nil
}

// least is the smallest quantity an order of the pair takes: a lot, or
// the pair's minimum in whole lots.
func least(info ports.PairInfo) decimal.Decimal {
	return decimal.Max(info.Lot, info.MinQuantity.Div(info.Lot).Ceil().Mul(info.Lot))
}

// lots rounds a quantity to the pair's lots, up or down.
func lots(q decimal.Decimal, info ports.PairInfo, up bool) decimal.Decimal {
	n := q.Div(info.Lot)
	if up {
		return n.Ceil().Mul(info.Lot)
	}
	return n.Floor().Mul(info.Lot)
}

// priceIn returns an asset's price in the liquidation's quote asset.
func (s *Service) priceIn(l ports.Liquidation, asset string) (decimal.Decimal, error) {
	all := s.Prices.Prices()
	p, ok := all.Of(asset)
	q, okQ := all.Of(l.QuoteAsset)
	if !ok || !okQ {
		return decimal.Zero, fmt.Errorf("no price for %s in %s", asset, l.QuoteAsset)
	}
	return p.Value.DivRound(q.Value, 18), nil
}

// need is what the account lacks of an asset it owes, to buy with the
// quote asset: amount buys it in whole lots at the price and buyBuffer
// more; least what buys one order's least.
type need struct {
	info   ports.PairInfo
	open   bool
	amount decimal.Decimal
	least  decimal.Decimal
}

// needs returns what the account lacks of each asset it owes and may buy
// with the quote asset, the largest first; one without a pair that may
// ever trade is left to the insurance fund.
func (s *Service) needs(ctx context.Context, l ports.Liquidation, holdings []domain.Holding) ([]need, error) {
	var out []need
	for _, h := range holdings {
		short := h.Debt().Sub(h.Free)
		if h.Asset == l.QuoteAsset || !short.IsPositive() {
			continue
		}
		info, open, ok, err := s.market(ctx, l, h.Asset)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		price, err := s.priceIn(l, h.Asset)
		if err != nil {
			return nil, err
		}
		decimals, err := s.Instruments.Decimals(ctx, l.QuoteAsset)
		if err != nil {
			return nil, err
		}
		inQuote := func(q decimal.Decimal) decimal.Decimal { return q.Mul(price).Mul(buyBuffer).RoundCeil(decimals) }
		smallest := least(info)
		out = append(out, need{
			info: info, open: open, amount: inQuote(decimal.Max(lots(short, info, true), smallest)), least: inQuote(smallest),
		})
	}
	slices.SortFunc(out, func(a, b need) int { return b.amount.Cmp(a.amount) })
	return out, nil
}

// sellBuffer is what the sales bring beyond what is needed (review DD
// C19 ⑤): for the trading fee and the prices they get.
var sellBuffer = decimal.RequireFromString("1.02")

// planSells plans the next sales (review DD C19 ⑤): what they must still
// bring in the quote asset — the quote's own debt, the buys, and the fee
// on what was sold and what will be bought, beyond what the account holds
// of the quote — and sellBuffer more, from what the account holds beyond
// each asset's own debt, the most valuable first, in whole lots, at most
// an order's most. why says what it waits for when it plans nothing while
// a sale is still needed and some asset may sell later.
func (s *Service) planSells(ctx context.Context, l ports.Liquidation, holdings []domain.Holding, orders []ports.LiquidationOrder) ([]ports.LiquidationOrder, string, error) {
	needs, err := s.needs(ctx, l, holdings)
	if err != nil {
		return nil, "", err
	}
	buys := decimal.Zero
	for _, n := range needs {
		buys = buys.Add(n.amount)
	}
	q := holding(holdings, l.QuoteAsset)
	one := decimal.NewFromInt(1)
	// The sales x: free + x >= debt + buys + rate x (sold + x + buys).
	short := q.Debt().Add(buys.Mul(one.Add(l.FeeRate))).Add(l.FeeRate.Mul(executed(orders, sideSell))).Sub(q.Free)
	if !short.IsPositive() {
		return nil, "", nil
	}
	rest := short.DivRound(one.Sub(l.FeeRate), 18).Mul(sellBuffer)
	type asset struct {
		info  ports.PairInfo
		open  bool
		most  decimal.Decimal // what it holds beyond its debt, in whole lots
		price decimal.Decimal // in the quote
	}
	var assets []asset
	for _, h := range holdings {
		surplus := h.Free.Sub(h.Debt())
		if h.Asset == l.QuoteAsset || !surplus.IsPositive() {
			continue
		}
		info, open, ok, err := s.market(ctx, l, h.Asset)
		if err != nil {
			return nil, "", err
		}
		if !ok || lots(surplus, info, false).LessThan(least(info)) {
			continue // it stays in the account
		}
		price, err := s.priceIn(l, h.Asset)
		if err != nil {
			return nil, "", err
		}
		assets = append(assets, asset{info: info, open: open, most: lots(surplus, info, false), price: price})
	}
	slices.SortFunc(assets, func(a, b asset) int { return b.most.Mul(b.price).Cmp(a.most.Mul(a.price)) })
	all := triesOf(orders, sideSell)
	var plan []ports.LiquidationOrder
	why := ""
	for _, a := range assets {
		if !rest.IsPositive() {
			break
		}
		t := all[a.info.Symbol]
		n, wait, ok := s.nextAttempt(t)
		switch {
		case !ok:
			continue // its attempts used up: it stays
		case !a.open || wait > 0:
			why = cmp.Or(why, waitNote(a.info, t))
			continue
		}
		qty := decimal.Min(decimal.Max(lots(rest.DivRound(a.price, 18), a.info, true), least(a.info)), a.most)
		if a.info.MaxQuantity.IsPositive() {
			qty = decimal.Min(qty, lots(a.info.MaxQuantity, a.info, false))
		}
		plan = append(plan, s.order(l, a.info.Symbol, sideSell, n, qty, decimal.Zero))
		rest = rest.Sub(qty.Mul(a.price))
	}
	return plan, why, nil
}

// planBuys plans the next buys: what the account lacks of each asset it
// owes (needs), the largest first, with what it holds of the quote asset
// beyond the quote's own debt (the fee is booked by then); what is left
// too small for an order's least buys nothing. why as for planSells.
func (s *Service) planBuys(ctx context.Context, l ports.Liquidation, holdings []domain.Holding, orders []ports.LiquidationOrder) ([]ports.LiquidationOrder, string, error) {
	needs, err := s.needs(ctx, l, holdings)
	if err != nil {
		return nil, "", err
	}
	q := holding(holdings, l.QuoteAsset)
	budget := q.Free.Sub(q.Debt())
	all := triesOf(orders, sideBuy)
	var plan []ports.LiquidationOrder
	why := ""
	for _, n := range needs {
		amount := decimal.Min(n.amount, budget)
		if amount.LessThan(n.least) {
			continue // the insurance fund covers it
		}
		t := all[n.info.Symbol]
		attempt, wait, ok := s.nextAttempt(t)
		switch {
		case !ok:
			continue
		case !n.open || wait > 0:
			why = cmp.Or(why, waitNote(n.info, t))
			continue
		}
		plan = append(plan, s.order(l, n.info.Symbol, sideBuy, attempt, decimal.Zero, amount))
		budget = budget.Sub(amount)
	}
	return plan, why, nil
}

// order is a liquidation's new order.
func (s *Service) order(l ports.Liquidation, symbol, side string, attempt int, qty, quote decimal.Decimal) ports.LiquidationOrder {
	now := s.Now()
	return ports.LiquidationOrder{
		LiquidationID: l.ID, Symbol: symbol, Side: side, Attempt: attempt, Quantity: qty, QuoteAmount: quote, Status: ports.OrderPlanned,
		FilledQuantity: decimal.Zero, FilledQuote: decimal.Zero, CreatedAt: now, UpdatedAt: now,
	}
}

// stepFee charges the fee after the sales and before the buys (review DD
// C19 ④): the fee rate of what the sales brought and of what the buys
// will spend, in the quote asset, at most what the account holds of it;
// the buys then spend what is left beyond the quote's own debt. The
// amount is stored before it is booked, so that a retry books the same.
func (s *Service) stepFee(ctx context.Context, l ports.Liquidation) (ports.Liquidation, bool, error) {
	if l.Fee.IsZero() {
		holdings, err := s.held(ctx, l)
		if err != nil {
			return l, false, err
		}
		orders, err := s.Store.Read().Liquidations().Orders(ctx, l.ID)
		if err != nil {
			return l, false, err
		}
		needs, err := s.needs(ctx, l, holdings)
		if err != nil {
			return s.wait(ctx, l, "planning the buys: "+err.Error())
		}
		decimals, err := s.Instruments.Decimals(ctx, l.QuoteAsset)
		if err != nil {
			return l, false, err
		}
		buys, sold := decimal.Zero, executed(orders, sideSell)
		for _, n := range needs {
			buys = buys.Add(n.amount)
		}
		q := holding(holdings, l.QuoteAsset)
		// What the buys may spend, the fee on the sales and on them taken:
		// (free - debt - rate x sold) / (1 + rate).
		room := q.Free.Sub(q.Debt()).Sub(l.FeeRate.Mul(sold)).DivRound(decimal.NewFromInt(1).Add(l.FeeRate), 18)
		buy := decimal.Max(decimal.Min(buys, room), decimal.Zero)
		fee := decimal.Min(sold.Add(buy).Mul(l.FeeRate).RoundFloor(decimals), q.Free)
		if !fee.IsPositive() {
			return s.next(ctx, l, StepBuy)
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
	return s.next(ctx, l, StepBuy)
}

// repayKey names a liquidation's n-th repayment of an asset: from the
// account (repay), or by the insurance fund (cover); the first repayment
// from the account keeps the key it had before they were numbered.
func repayKey(l ports.Liquidation, cover bool, asset string, n int) string {
	if cover {
		return fmt.Sprintf("liquidation:%s:cover:%s:%d", l.ID, asset, n)
	}
	if n == 0 {
		return fmt.Sprintf("liquidation:%s:repay:%s", l.ID, asset)
	}
	return fmt.Sprintf("liquidation:%s:repay:%s:%d", l.ID, asset, n)
}

// coverRetry is how long a refused cover (the fund short) waits before
// the next attempt.
const coverRetry = time.Minute

// stepRepay repays each debt from what the account holds of the asset
// (repay), or the insurance fund pays what the account cannot (cover:
// debt − free, once the account holds none of the asset; review DD C19
// ③), interest first, as liquidation repayments (MARGIN_LIQUIDATE); one
// waiting to be booked holds the step, recovery finishes it. A refused
// repayment is made again, recomputed, under a new key (at once, then
// after the backoff); a refused cover leaves the liquidation SHORTFALL,
// tried again every coverRetry.
func (s *Service) stepRepay(ctx context.Context, l ports.Liquidation, cover bool) (ports.Liquidation, bool, error) {
	holdings, err := s.held(ctx, l)
	if err != nil {
		return l, false, err
	}
	made, err := s.Store.Read().Repays().OfLiquidation(ctx, l.ID)
	if err != nil {
		return l, false, err
	}
	waiting, short, again := "", false, false
	for _, h := range holdings {
		debt := h.Debt()
		amount := decimal.Min(h.Free, debt)
		if cover {
			if amount.IsPositive() {
				again = true // the account repays what it holds first
				continue
			}
			amount = debt
		}
		if !amount.IsPositive() {
			continue
		}
		p, err := s.liquidationRepay(ctx, l, h, amount, cover, made)
		if err != nil {
			return l, false, err
		}
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
			p.Failure = failureText(apperr.From(err))
			s.Log.WarnContext(ctx, "the ledger refused a liquidation's repayment; it is made again", "liquidation_id", l.ID,
				"asset", h.Asset, "cover", cover, "error", p.Failure)
		}
		if cover {
			waiting, short = fmt.Sprintf("the insurance fund could not cover %s %s (%s); retried every minute", amount, h.Asset, p.Failure), true
		} else {
			waiting = cmp.Or(waiting, fmt.Sprintf("the ledger refused repaying %s %s (%s); made again, recomputed", amount, h.Asset, p.Failure))
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
	switch {
	case waiting != "":
		return s.wait(ctx, l, waiting)
	case cover && again:
		return s.next(ctx, l, StepRepay)
	case cover:
		return s.next(ctx, l, StepSettle)
	}
	return s.next(ctx, l, StepCover)
}

// liquidationRepay returns the repayment of a holding's debt to make now
// (made are the liquidation's repayments, oldest first): one waiting to
// be booked, the last one refused while it waits to be made again, or a
// new one of amount, stored PENDING.
func (s *Service) liquidationRepay(ctx context.Context, l ports.Liquidation, h domain.Holding, amount decimal.Decimal, cover bool, made []ports.Repay) (ports.Repay, error) {
	n, refusals := 0, 0
	var last ports.Repay
	for _, p := range made {
		if p.Asset != h.Asset || (p.CoveredBy == ports.CoveredByInsurance) != cover {
			continue
		}
		if p.Status == ports.OpPending {
			return p, nil
		}
		n, last = n+1, p
		if refusals++; p.Status != ports.OpFailed {
			refusals = 0
		}
	}
	if refusals > 0 {
		retry := backoff(refusals)
		if cover {
			retry = coverRetry
		}
		if s.Now().Sub(last.DoneAt) < retry {
			return last, nil
		}
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
		return s.next(ctx, l, StepRepay) // never completed owing: repaid, then the fund
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
