package application

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// Settling a user's margin accounts (design 2026-10-09 user kinds §1 #9,
// batch L4b): a purge of a test account ends its margin trading before
// its balances are moved out - its orders canceled, its debts repaid from
// what its accounts hold.

// SettlePrefix starts the keys of Settle's repayments.
const SettlePrefix = "settle:"

// SettleResult is what Settle did and what is still owed.
type SettleResult struct {
	CanceledOrders int
	// CancelRefused are the accounts whose orders the trading service
	// would not cancel: they may still rest there (review C80 ①).
	CancelRefused []CancelRefused
	Repaid        []Repaid
	Remaining     []Owed
}

// CancelRefused is an account whose cancel was refused, and the code.
type CancelRefused struct {
	Account domain.Account
	Code    string
}

// Complete reports whether nothing is owed any more.
func (r SettleResult) Complete() bool { return len(r.Remaining) == 0 }

// Repaid is a repayment Settle made: interest first.
type Repaid struct {
	Account   domain.Account
	Asset     string
	Principal decimal.Decimal
	Interest  decimal.Decimal
}

// Owed is a debt (principal and interest) Settle left: the account did
// not hold enough of the asset free.
type Owed struct {
	Account domain.Account
	Asset   string
	Amount  decimal.Decimal
}

// Settle ends a user's margin trading for a purge. The open orders of
// each of their margin accounts are canceled (spot-trading-service; it
// waits up to SettleWait for them to let go of what they held), then each
// debt is repaid from what the account holds free of the asset, interest
// first, as an AUTO_REPAY repayment; what is left owed comes back (the
// purge moves funds in from spot and calls again). A user with an account
// being liquidated is refused (MARGIN_FROZEN: the liquidation repays its
// debts) before anything changes. A user without margin accounts -
// user-service is not asked whether it knows them (the coordinator's
// 06:14 rule: a purge calls it for every test account) - has nothing to
// settle. Calling it again cancels and repays what is open by then:
// nothing once all is done. Each call is audited as margin.user_settled.
func (s *Service) Settle(ctx context.Context, userID, actor, reason string) (SettleResult, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return SettleResult{}, apperr.Invalid("user_id must be a UUID")
	}
	userID = id.String()
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return SettleResult{}, apperr.Invalid("an actor and a reason of at least 3 characters are required")
	}
	stored, err := s.Store.Read().Accounts().OfUser(ctx, userID)
	if err != nil {
		return SettleResult{}, err
	}
	held, err := s.Ledger.Holdings(ctx, userID)
	if err != nil {
		return SettleResult{}, err
	}
	accounts := map[domain.Account]bool{}
	for a := range held {
		accounts[a] = true
	}
	for _, a := range stored {
		if a.Status == domain.StatusLiquidating {
			return SettleResult{}, domain.ErrFrozen.WithDetail("status", string(a.Status)).WithDetail("account", a.Account.Key())
		}
		accounts[a.Account] = true
	}
	order := slices.SortedFunc(maps.Keys(accounts), func(a, b domain.Account) int { return strings.Compare(a.Key(), b.Key()) })
	var out SettleResult
	err = s.settle(ctx, userID, order, &out)
	// What it did is audited even when it failed midway (review C79 ④):
	// cancels may have gone out, some debts been repaid.
	if aerr := s.auditSettle(ctx, userID, actor, reason, out, err); aerr != nil {
		if err != nil {
			s.Log.ErrorContext(ctx, "a failed settle not audited", "user_id", userID, "error", aerr)
		}
		err = cmp.Or(err, aerr)
	}
	s.touch(userID)
	if err != nil {
		s.Log.WarnContext(ctx, "margin accounts not settled", "user_id", userID, "actor", actor, "error", err)
		return SettleResult{}, err
	}
	s.Log.InfoContext(ctx, "margin accounts settled", "user_id", userID, "actor", actor, "canceled_orders", out.CanceledOrders,
		"repaid", len(out.Repaid), "remaining", len(out.Remaining))
	return out, nil
}

// settle cancels the accounts' orders, repays what they can and lists what
// is still owed, in out.
func (s *Service) settle(ctx context.Context, userID string, order []domain.Account, out *SettleResult) error {
	for _, a := range order {
		n, err := s.Trading.CancelAccount(ctx, userID, a)
		switch {
		case err == nil:
			out.CanceledOrders += n
		case refused(err):
			// The trading service refused (review C79 ③): what the orders
			// hold stays locked and its debt owed below; the rest goes on.
			// The answer and the audit name it (C80 ①).
			out.CancelRefused = append(out.CancelRefused, CancelRefused{Account: a, Code: apperr.From(err).Code})
			s.Log.WarnContext(ctx, "a purge's cancel refused", "user_id", userID, "account", a.Key(), "error", err)
		default:
			return err
		}
	}
	held, err := s.unlocked(ctx, userID)
	if err != nil {
		return err
	}
	for _, a := range order {
		for _, h := range sortedHoldings(held[a]) {
			if !h.Debt().IsPositive() || !h.Free.IsPositive() {
				continue
			}
			res, err := s.repay(ctx, RepayInput{
				UserID: userID, IdemKey: SettlePrefix + uuid.Must(uuid.NewV7()).String(), Account: a, Asset: h.Asset, All: true,
				Reason: ports.RepayAuto,
			})
			switch {
			case err == nil:
				out.Repaid = append(out.Repaid, Repaid{Account: a, Asset: h.Asset, Principal: res.Repay.Principal, Interest: res.Repay.Interest})
			case errors.Is(err, errInProgress), refused(err):
				// Not booked yet (recovery finishes it), or refused (a
				// liquidation began meanwhile): still owed below.
				s.Log.WarnContext(ctx, "a purge's repayment not made", "user_id", userID, "account", a.Key(), "asset", h.Asset, "error", err)
			default:
				return err
			}
		}
	}
	if held, err = s.Ledger.Holdings(ctx, userID); err != nil {
		return err
	}
	for _, a := range slices.SortedFunc(maps.Keys(held), func(a, b domain.Account) int { return strings.Compare(a.Key(), b.Key()) }) {
		for _, h := range sortedHoldings(held[a]) {
			if h.Debt().IsPositive() {
				out.Remaining = append(out.Remaining, Owed{Account: a, Asset: h.Asset, Amount: h.Debt()})
			}
		}
	}
	return nil
}

// unlocked waits until nothing is locked in the user's margin accounts
// (the orders canceled are gone), up to SettleWait, and returns what they
// hold then.
func (s *Service) unlocked(ctx context.Context, userID string) (map[domain.Account][]domain.Holding, error) {
	poll := cmp.Or(s.SettlePoll, 200*time.Millisecond)
	looks := int(cmp.Or(s.SettleWait, 8*time.Second) / poll)
	for look := 0; ; look++ {
		held, err := s.Ledger.Holdings(ctx, userID)
		if err != nil || look >= looks || !slices.ContainsFunc(slices.Collect(maps.Values(held)), locked) {
			return held, err
		}
		if err := s.sleep(ctx, poll); err != nil {
			return nil, err
		}
	}
}

func sortedHoldings(list []domain.Holding) []domain.Holding {
	return slices.SortedFunc(slices.Values(list), func(a, b domain.Holding) int { return strings.Compare(a.Asset, b.Asset) })
}

// auditSettle records the call and what it did.
func (s *Service) auditSettle(ctx context.Context, userID, actor, reason string, out SettleResult, failed error) error {
	type entry struct {
		Account   string `json:"account"`
		Symbol    string `json:"symbol,omitempty"`
		Asset     string `json:"asset,omitempty"`
		Principal string `json:"principal,omitempty"`
		Interest  string `json:"interest,omitempty"`
		Amount    string `json:"amount,omitempty"`
		Code      string `json:"code,omitempty"`
	}
	refused, repaid, owed := []entry{}, []entry{}, []entry{}
	for _, c := range out.CancelRefused {
		refused = append(refused, entry{Account: string(c.Account.Type), Symbol: c.Account.Symbol, Code: c.Code})
	}
	for _, r := range out.Repaid {
		repaid = append(repaid, entry{
			Account: string(r.Account.Type), Symbol: r.Account.Symbol, Asset: r.Asset, Principal: r.Principal.String(), Interest: r.Interest.String(),
		})
	}
	for _, o := range out.Remaining {
		owed = append(owed, entry{Account: string(o.Account.Type), Symbol: o.Account.Symbol, Asset: o.Asset, Amount: o.Amount.String()})
	}
	d := map[string]any{
		"canceled_orders": out.CanceledOrders, "cancel_refused": refused, "repaid": repaid, "remaining_debt": owed,
		"complete": failed == nil && out.Complete(),
	}
	if failed != nil { // what is owed unknown: it stopped midway
		d["error"] = apperr.From(failed).Code
	}
	details, err := json.Marshal(d)
	if err != nil {
		return err
	}
	// Recorded even when the caller has gone: the cancels went out.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "margin.user_settled", Actor: actor, Reason: reason, Details: string(details),
		}, "actor", actor)
	})
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
