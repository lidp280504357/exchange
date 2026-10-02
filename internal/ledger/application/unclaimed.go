package application

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/ledger/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

// ReleaseUnclaimed books an unclaimed deposit to its user once an
// administrator decided it is theirs (design 2026-10-02 §4.3):
// UNCLAIMED_DEPOSIT pays the user's SPOT account the deposit's own asset
// and amount (DEPOSIT_CREDIT, key deposit-release:<id>), audited as
// ledger.unclaimed_released with the administrator as the actor.
// wallet-service calls it; repeating it returns the first journal.
func (s *Service) ReleaseUnclaimed(ctx context.Context, depositID, userID, asset string, amount decimal.Decimal, actor, reason string) (Result, error) {
	if _, err := uuid.Parse(depositID); err != nil {
		return Result{}, apperr.Invalid("deposit_id must be a UUID")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return Result{}, apperr.Invalid("user_id must be a UUID")
	}
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return Result{}, apperr.Invalid("an actor and a reason are required")
	}
	p, err := domain.ReleasePostingOf(domain.Deposit{ID: depositID, UserID: userID, Asset: asset, Amount: amount},
		"unclaimed deposit "+depositID+" released: "+strings.TrimSpace(reason))
	if err != nil {
		return Result{}, err
	}
	if err := s.checkPrecision(ctx, p); err != nil {
		return Result{}, err
	}
	var res Result
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		if res, err = s.post(ctx, r, p); err != nil || res.Replayed {
			return err
		}
		details, _ := json.Marshal(map[string]string{
			"deposit_id": depositID, "asset": asset, "amount": amount.String(), "journal_id": res.JournalID,
		})
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "ledger.unclaimed_released", Actor: actor, Reason: reason, Details: string(details),
		}, "actor", actor)
	})
	if _, ok := pgUnique(err); ok {
		return s.replay(ctx, s.Store.Read(), p)
	}
	return res, err
}
