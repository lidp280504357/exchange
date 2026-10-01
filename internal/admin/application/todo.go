package application

import (
	"context"
	"encoding/json"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
)

// Todo counts what waits for an administrator, as far as their role shows
// it: withdrawals to review and fund operations to decide (the console's
// badges, pushed on GET /admin/v1/events).
type Todo struct {
	Withdrawals int `json:"withdrawals"`
	Approvals   int `json:"approvals"`
	// Partial names the counts that could not be read.
	Partial []string `json:"partial"`
}

// todoPage bounds the withdrawals counted: beyond it the badge says so.
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
	return out, nil
}
