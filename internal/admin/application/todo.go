package application

import (
	"context"
	"encoding/json"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
)

// Todo counts what waits for an administrator, as far as their role shows
// it: withdrawals to review, fund operations and identity rebind requests
// to decide (the console's badges, pushed on GET /admin/v1/events).
type Todo struct {
	Withdrawals      int `json:"withdrawals"`
	Approvals        int `json:"approvals"`
	IdentityRequests int `json:"identity_requests"`
	// Deposits waiting for a decision (with deposits.review).
	Deposits int `json:"deposits"`
	// InstrumentChanges are the changes of trading parameters waiting for
	// a second ADMIN or their time (with instruments.trading).
	InstrumentChanges int `json:"instrument_changes"`
	// Partial names the counts that could not be read.
	Partial []string `json:"partial"`
}

// todoPage bounds the withdrawals and requests counted: beyond it the
// badge says so.
const todoPage = 200

// Todo returns the administrator's counts; a service that does not answer
// leaves its count at zero and is named in Partial.
func (s *Service) Todo(ctx context.Context, p Principal) (Todo, error) {
	out := Todo{Partial: []string{}}
	if p.require(domain.PermWithdrawalsRead) == nil {
		raw, err := s.Wallet.List(ctx, ports.WithdrawalQuery{Status: "PENDING_REVIEW", Limit: todoPage})
		var page struct {
			Items []json.RawMessage `json:"items"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &page)
		}
		if err != nil {
			s.Log.WarnContext(ctx, "todo: withdrawals unavailable", "error", err)
			out.Partial = append(out.Partial, "withdrawals")
		}
		out.Withdrawals = len(page.Items)
	}
	if p.require(domain.PermAdjustRequest) == nil || p.require(domain.PermAdjustApprove) == nil {
		n, err := s.Store.Read().Approvals().CountPending(ctx)
		if err != nil {
			return Todo{}, err
		}
		out.Approvals = n
	}
	if p.require(domain.PermUsersSecurity) == nil {
		list, _, err := s.Security.IdentityRequests(ctx, ports.IdentityRequestQuery{Status: RebindPending, Limit: todoPage})
		if err != nil {
			s.Log.WarnContext(ctx, "todo: identity requests unavailable", "error", err)
			out.Partial = append(out.Partial, "identity_requests")
		}
		out.IdentityRequests = len(list)
	}
	if p.require(domain.PermDepositsReview) == nil {
		raw, err := s.Deposits.List(ctx, ports.DepositReviewQuery{Attention: true, Limit: todoPage})
		var page struct {
			Items []json.RawMessage `json:"items"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &page)
		}
		if err != nil {
			s.Log.WarnContext(ctx, "todo: deposits unavailable", "error", err)
			out.Partial = append(out.Partial, "deposits")
		}
		out.Deposits = len(page.Items)
	}
	if p.require(domain.PermInstrumentsTrading) == nil {
		n, err := s.Store.Read().Changes().Open(ctx)
		if err != nil {
			return Todo{}, err
		}
		out.InstrumentChanges = n
	}
	return out, nil
}
