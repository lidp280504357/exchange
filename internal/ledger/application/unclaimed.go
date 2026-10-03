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
// wallet-service calls it; repeating it returns the first journal, whoever
// repeats it and with whatever reason: the reason is the audit event's,
// not the journal's (C5.5 ⑦), so a retry with other words is not a
// conflict.
func (s *Service) ReleaseUnclaimed(ctx context.Context, depositID, userID, asset string, amount decimal.Decimal, actor, reason string) (Result, error) {
	if _, err := uuid.Parse(depositID); err != nil {
		return Result{}, apperr.Invalid("deposit_id must be a UUID")
	}
	if id, err := uuid.Parse(userID); err != nil || id == uuid.Nil {
		// The nil UUID is wallet-service's owner of a deposit to an address
		// no user has (B7a): someone must be named before it is released.
		return Result{}, apperr.Invalid("user_id must be a user's UUID")
	}
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return Result{}, apperr.Invalid("an actor and a reason are required")
	}
	p, err := domain.ReleasePostingOf(domain.Deposit{ID: depositID, UserID: userID, Asset: asset, Amount: amount},
		"unclaimed deposit "+depositID+" released")
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

// UnclaimedRelease returns the journal that released an unclaimed deposit
// and the user it paid ("" when it was not released): wallet-service does
// not close a deposit whose release it failed to record (C5.5 ⑦), and
// records one of a deposit of nobody for the user it went to (review AJ).
func (s *Service) UnclaimedRelease(ctx context.Context, depositID string) (journal, user string, err error) {
	if _, err := uuid.Parse(depositID); err != nil {
		return "", "", apperr.Invalid("deposit_id must be a UUID")
	}
	r := s.Store.Read()
	j, err := r.Journals().ByIdemKey(ctx, domain.ReleaseKey(depositID))
	if err != nil || j == nil {
		return "", "", err
	}
	if user, err = r.Journals().Payee(ctx, j.ID); err != nil {
		return "", "", err
	}
	return j.ID, user, nil
}

// CreditUnclaimed books a custodian's deposit to an address no user has to
// UNCLAIMED_DEPOSIT (B7a of the real gateway's integration): the same
// posting as an unclaimed deposit's (DepositPosting, key deposit:<id>,
// so a retry replays), without a user. wallet-service calls it instead of
// announcing a deposit nobody owns.
func (s *Service) CreditUnclaimed(ctx context.Context, depositID, asset string, amount decimal.Decimal, network, txHash, reason string) (Result, error) {
	if _, err := uuid.Parse(depositID); err != nil {
		return Result{}, apperr.Invalid("deposit_id must be a UUID")
	}
	if strings.TrimSpace(reason) == "" {
		return Result{}, apperr.Invalid("a reason is required")
	}
	p, err := domain.DepositPosting(domain.Deposit{
		ID: depositID, Asset: asset, Amount: amount, Network: network, TxHash: txHash, Unclaimed: true, Reason: reason,
	})
	if err != nil {
		return Result{}, err
	}
	if err := s.checkPrecision(ctx, p); err != nil {
		return Result{}, err
	}
	return s.Post(ctx, p)
}
