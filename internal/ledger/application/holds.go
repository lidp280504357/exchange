package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/ledger/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Administrators' holds on part of a user's SPOT balance (design
// 2026-10-02 §4.1): the admin console checks the administrator's role; the
// ledger books the freeze and its release and audits both with the
// administrator as the actor.

func holdAudit(action string, h domain.Hold, reason string) *auditv1.AdminActionPerformed {
	details, _ := json.Marshal(map[string]string{
		"hold_id": h.ID, "account_type": h.AccountType, "asset": h.Asset, "amount": h.Amount.String(),
	})
	actor := h.Actor
	if action == "ledger.hold_released" {
		actor = h.ReleasedBy
	}
	return &auditv1.AdminActionPerformed{Target: "user:" + h.UserID, Action: action, Actor: actor, Reason: reason, Details: string(details)}
}

// PlaceHold freezes amount of a user's SPOT balance (ADMIN_FREEZE) under
// the caller's hold ID; repeating the ID with the same hold returns it,
// with another one fails with COMMON_IDEMPOTENCY_CONFLICT. Too little
// available fails with LEDGER_INSUFFICIENT_BALANCE.
func (s *Service) PlaceHold(ctx context.Context, id, userID, asset string, amount decimal.Decimal, actor, reason string) (domain.Hold, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Hold{}, apperr.Invalid("hold_id must be a UUID")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return domain.Hold{}, apperr.Invalid("user_id must be a UUID")
	}
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return domain.Hold{}, err
	}
	h, err := domain.NewHold(id, userID, asset, amount, decimals, actor, reason, s.Now())
	if err != nil {
		return domain.Hold{}, err
	}
	p, err := domain.HoldPosting(h, decimals)
	if err != nil {
		return domain.Hold{}, err
	}
	var out domain.Hold
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if prev, err := r.Holds().Get(ctx, id); err != nil {
			return err
		} else if prev != nil {
			if prev.UserID != h.UserID || prev.Asset != h.Asset || !prev.Amount.Equal(h.Amount) || prev.Reason != h.Reason {
				return domain.ErrIdempotencyConflict
			}
			out = *prev
			return nil
		}
		res, err := s.post(ctx, r, p)
		if err != nil {
			return err
		}
		h.JournalID = res.JournalID
		if err := r.Holds().Insert(ctx, h); err != nil {
			return err
		}
		out = h
		return r.Emit(ctx, event.TopicAudit, holdAudit("ledger.hold_placed", h, h.Reason), "actor", h.Actor)
	})
	if _, ok := pg.UniqueViolation(err); ok {
		// A concurrent request with the same ID won: answer as a repeat.
		if prev, gerr := s.Store.Read().Holds().Get(ctx, id); gerr == nil && prev != nil && prev.Amount.Equal(h.Amount) && prev.Asset == h.Asset {
			return *prev, nil
		}
	}
	return out, err
}

// ReleaseHold returns a hold's amount to the available balance
// (ADMIN_UNFREEZE), once: a hold released already fails with
// LEDGER_HOLD_RELEASED.
func (s *Service) ReleaseHold(ctx context.Context, id, actor, reason string) (domain.Hold, error) {
	var out domain.Hold
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		h, err := r.Holds().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if h == nil {
			return domain.ErrHoldNotFound
		}
		if err := h.Release(actor, reason, s.Now()); err != nil {
			return err
		}
		decimals, err := s.Assets.Decimals(ctx, h.Asset)
		if err != nil {
			return err
		}
		p, err := domain.ReleasePosting(*h, decimals)
		if err != nil {
			return err
		}
		res, err := s.post(ctx, r, p)
		if err != nil {
			return err
		}
		h.ReleaseJournalID = res.JournalID
		if err := r.Holds().Release(ctx, *h); err != nil {
			return err
		}
		out = *h
		return r.Emit(ctx, event.TopicAudit, holdAudit("ledger.hold_released", *h, h.ReleaseReason), "actor", h.ReleasedBy)
	})
	return out, err
}

// ForceReleaseHold is the operators' way out of a hold that cannot be
// released in full because part of its frozen amount was released
// elsewhere (ReleaseHold failing with LEDGER_INSUFFICIENT_BALANCE): it
// returns amount of it to the available balance (at most the hold and
// what is frozen; zero only marks it), marks the hold released and
// audits it as ledger.hold_released with "forced" and the amount
// released. The account's frozen balance is shared with the user's orders
// and withdrawals, so the caller works out how much of it is the hold's
// (exchangectl ledger release-hold; C5.5 ⑧, ⑯) and says how (basis, kept in
// the audit event: the frozen balance and what else it held, ⑱).
func (s *Service) ForceReleaseHold(ctx context.Context, id, actor, reason string, amount decimal.Decimal, basis map[string]string) (domain.Hold, error) {
	if amount.IsNegative() {
		return domain.Hold{}, apperr.Invalid("the amount released is not negative")
	}
	var out domain.Hold
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		h, err := r.Holds().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if h == nil {
			return domain.ErrHoldNotFound
		}
		if err := h.Release(actor, reason, s.Now()); err != nil {
			return err
		}
		if amount.GreaterThan(h.Amount) {
			return apperr.Invalid(fmt.Sprintf("the hold is %s: no more of it is released", h.Amount))
		}
		accs, err := r.Accounts().Lock(ctx, []domain.AccountKey{domain.UserAccount(h.UserID, h.AccountType, h.Asset)})
		if err != nil {
			return err
		}
		if len(accs) != 1 || amount.GreaterThan(accs[0].Frozen) {
			return domain.ErrInsufficientBalance
		}
		if amount.IsPositive() {
			decimals, err := s.Assets.Decimals(ctx, h.Asset)
			if err != nil {
				return err
			}
			part := *h
			part.Amount = amount
			p, err := domain.ReleasePosting(part, decimals)
			if err != nil {
				return err
			}
			res, err := s.post(ctx, r, p)
			if err != nil {
				return err
			}
			h.ReleaseJournalID = res.JournalID
		}
		if err := r.Holds().Release(ctx, *h); err != nil {
			return err
		}
		out = *h
		details, _ := json.Marshal(map[string]any{
			"hold_id": h.ID, "account_type": h.AccountType, "asset": h.Asset, "amount": h.Amount.String(), "released": amount.String(),
			"forced": true, "frozen_at_release": accs[0].Frozen.String(), "cap": basis,
		})
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "user:" + h.UserID, Action: "ledger.hold_released", Actor: h.ReleasedBy, Reason: h.ReleaseReason, Details: string(details),
		}, "actor", h.ReleasedBy)
	})
	return out, err
}

// Hold returns a hold with its account's balances (nil when unknown).
func (s *Service) Hold(ctx context.Context, id string) (*domain.Hold, domain.Account, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.Account{}, apperr.Invalid("the hold ID is a UUID")
	}
	var h *domain.Hold
	var acc domain.Account
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		if h, err = r.Holds().Get(ctx, id); err != nil || h == nil {
			return err
		}
		accs, err := r.Accounts().Lock(ctx, []domain.AccountKey{domain.UserAccount(h.UserID, h.AccountType, h.Asset)})
		if err == nil && len(accs) == 1 {
			acc = accs[0]
		}
		return err
	})
	return h, acc, err
}

// Holds lists a user's holds, newest first (at most 200).
func (s *Service) Holds(ctx context.Context, userID string, activeOnly bool) ([]domain.Hold, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, apperr.Invalid("user_id must be a UUID")
	}
	return s.Store.Read().Holds().OfUser(ctx, userID, activeOnly, 200)
}
