package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// RepayInput is a request to repay a debt from the margin account's free
// balance.
type RepayInput struct {
	UserID  string
	IdemKey string
	Account domain.Account
	Asset   string
	// Amount to repay; ignored with All.
	Amount decimal.Decimal
	// All repays the whole debt of the asset, as far as the free balance
	// goes.
	All bool
	// Reason is USER (the default), AUTO_REPAY or LIQUIDATION, with the
	// order or the liquidation behind it.
	Reason        string
	OrderID       string
	LiquidationID string
}

func (in RepayInput) hash() []byte {
	amount := in.Amount.String()
	if in.All {
		amount = "ALL"
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%s|%s|%s", in.Account.Key(), in.Asset, amount, in.reason(), in.OrderID,
		in.LiquidationID))
	return sum[:]
}

func (in RepayInput) reason() string {
	if in.Reason == "" {
		return ports.RepayUser
	}
	return in.Reason
}

// RepayResult is a booked repayment and the loan after it.
type RepayResult struct {
	Repay ports.Repay
	Loan  ports.Loan
}

// ErrInsufficient refuses a repayment above the free balance.
var ErrInsufficient = apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")

// Repay pays a debt back from the margin account's free balance, interest
// first (design §4.3). Repaying lowers the risk, so it stays open while
// margin.enabled is off and while an operator froze the account; only a
// liquidation in progress refuses it (it repays the debts itself).
func (s *Service) Repay(ctx context.Context, in RepayInput) (RepayResult, error) {
	if err := checkKey(in.IdemKey); err != nil {
		return RepayResult{}, err
	}
	if prior, ok, err := s.Store.Read().Repays().ByKey(ctx, in.UserID, in.IdemKey); err != nil {
		return RepayResult{}, err
	} else if ok {
		return s.replayRepay(ctx, prior, in)
	}
	var p ports.Repay
	replay := false
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, in.UserID); err != nil {
			return err
		}
		prior, ok, err := r.Repays().ByKey(ctx, in.UserID, in.IdemKey)
		if err != nil {
			return err
		}
		if p, replay = prior, ok; ok {
			return nil
		}
		p, err = s.planRepay(ctx, r, in)
		return err
	})
	if err != nil {
		return RepayResult{}, err
	}
	if replay {
		return s.replayRepay(ctx, p, in)
	}
	return s.postRepay(ctx, p)
}

func (s *Service) planRepay(ctx context.Context, r ports.Repos, in RepayInput) (ports.Repay, error) {
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return ports.Repay{}, err
	}
	t, listed := cat.Assets[in.Asset]
	if !listed {
		return ports.Repay{}, domain.ErrNotBorrowable.WithDetail("asset", in.Asset)
	}
	if !in.All {
		if err := checkAmount(in.Amount, t.Decimals); err != nil {
			return ports.Repay{}, err
		}
	}
	st, ok, err := r.Accounts().Get(ctx, in.UserID, in.Account)
	if err != nil {
		return ports.Repay{}, err
	}
	if !ok {
		return ports.Repay{}, domain.ErrRepayTooMuch.WithDetail("debt", "0")
	}
	if st.Status == domain.StatusLiquidating && in.reason() != ports.RepayLiquidation {
		return ports.Repay{}, domain.ErrFrozen.WithDetail("status", string(st.Status))
	}
	all, err := s.Ledger.Holdings(ctx, in.UserID)
	if err != nil {
		return ports.Repay{}, err
	}
	h := holding(all[in.Account], in.Asset)
	var split domain.Repayment
	if in.All {
		if split = domain.RepayAll(h); !split.Total().IsPositive() {
			if !h.Debt().IsPositive() {
				return ports.Repay{}, domain.ErrRepayTooMuch.WithDetail("debt", "0")
			}
			return ports.Repay{}, ErrInsufficient.WithDetail("asset", in.Asset)
		}
	} else {
		if split, err = domain.Repay(in.Amount, h); err != nil {
			return ports.Repay{}, err
		}
		if in.Amount.GreaterThan(h.Free) {
			return ports.Repay{}, ErrInsufficient.WithDetail("asset", in.Asset).WithDetail("free", h.Free.String())
		}
	}
	p := ports.Repay{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: in.UserID, Account: in.Account, Asset: in.Asset, Interest: split.Interest,
		Principal: split.Principal, Reason: in.reason(), OrderID: in.OrderID, LiquidationID: in.LiquidationID, IdemKey: in.IdemKey,
		RequestHash: in.hash(), Status: ports.OpPending, CreatedAt: s.Now(),
	}
	return p, r.Repays().Insert(ctx, p)
}

func (s *Service) replayRepay(ctx context.Context, prior ports.Repay, in RepayInput) (RepayResult, error) {
	if !bytes.Equal(prior.RequestHash, in.hash()) {
		return RepayResult{}, domainIdempotencyConflict
	}
	switch prior.Status {
	case ports.OpPending:
		return s.postRepay(ctx, prior)
	case ports.OpFailed:
		return RepayResult{}, failure(prior.Failure)
	}
	loan, err := s.Store.Read().Loans().Get(ctx, prior.UserID, prior.Account, prior.Asset)
	return RepayResult{Repay: prior, Loan: loan}, err
}

// postRepay books a PENDING repayment and records the outcome.
func (s *Service) postRepay(ctx context.Context, p ports.Repay) (RepayResult, error) {
	moves := []ports.Move{{Type: domain.MoveRepay, Asset: p.Asset, Amount: p.Interest.Add(p.Principal), Interest: p.Interest}}
	journals, postErr := s.Ledger.Post(ctx, ports.Posting{
		IdemKey: "margin-repay:" + p.ID, UserID: p.UserID, Account: p.Account, Reference: "repay " + p.ID, Moves: moves,
	})
	if postErr != nil && !refused(postErr) {
		s.count("repay", "pending")
		s.Log.WarnContext(ctx, "repayment not booked yet; recovery retries it", "repay_id", p.ID, "error", postErr)
		return RepayResult{}, errInProgress
	}
	var out RepayResult
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, p.UserID); err != nil {
			return err
		}
		cur, err := r.Repays().GetForUpdate(ctx, p.ID)
		if err != nil {
			return err
		}
		out.Repay = cur
		if cur.Status != ports.OpPending {
			out.Loan, err = r.Loans().Get(ctx, p.UserID, p.Account, p.Asset)
			return err
		}
		cur.DoneAt = s.Now()
		if postErr != nil {
			cur.Status, cur.Failure = ports.OpFailed, failureText(apperr.From(postErr))
			out.Repay = cur
			return r.Repays().Finish(ctx, cur)
		}
		cur.Status = ports.OpDone
		out.Repay = cur
		if err := r.Repays().Finish(ctx, cur); err != nil {
			return err
		}
		if err := r.Loans().Add(ctx, p.UserID, p.Account, p.Asset, p.Principal.Neg(), p.Interest.Neg(), cur.DoneAt); err != nil {
			return err
		}
		if p.Principal.IsPositive() {
			if _, err := r.Pools().LockLent(ctx, p.Asset); err != nil {
				return err
			}
			if err := r.Pools().AddLent(ctx, p.Asset, p.Principal.Neg()); err != nil {
				return err
			}
		}
		if out.Loan, err = r.Loans().Get(ctx, p.UserID, p.Account, p.Asset); err != nil {
			return err
		}
		journal := ""
		if len(journals) > 0 {
			journal = journals[0]
		}
		return r.Emit(ctx, event.TopicMargin, &marginv1.MarginRepaid{
			RepayId: cur.ID, UserId: cur.UserID, AccountType: string(cur.Account.Type), Symbol: cur.Account.Symbol, Asset: cur.Asset,
			InterestRepaid: cur.Interest.String(), PrincipalRepaid: cur.Principal.String(), Principal: out.Loan.Principal.String(),
			Interest: out.Loan.Interest.String(), Reason: cur.Reason, OrderId: cur.OrderID, LiquidationId: cur.LiquidationID,
			JournalId: journal, RepaidAt: timestamppb.New(cur.DoneAt),
		}, "user", cur.UserID)
	})
	if err != nil {
		return RepayResult{}, err
	}
	if postErr != nil {
		s.count("repay", "failed")
		return RepayResult{}, postErr
	}
	s.count("repay", "done")
	return out, nil
}
