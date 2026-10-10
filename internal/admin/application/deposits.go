package application

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Deposits and withdrawals that need a person (design 2026-10-02 §4.2,
// §4.3). wallet-service records each decision and audits it with the
// administrator as the actor; a backfill of a lost callback goes through
// the fund operations (SubmitFunds, kind DEPOSIT_BACKFILL).

// DepositsForReview returns a page of wallet-service's deposits:
// q.Attention lists those waiting for a decision, q.ManualPending the
// backfilled ones without a callback yet.
func (s *Service) DepositsForReview(ctx context.Context, p Principal, q ports.DepositReviewQuery) (json.RawMessage, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return nil, apperr.Invalid("user_id must be a UUID")
		}
	}
	q.Limit = pageLimit(q.Limit)
	// The humans' by default (L1); a deposit nobody has claimed (the nil
	// UUID) is no other kind's and stays.
	var err error
	if q.ByKind, err = s.kindFilter(ctx, q.Kinds, q.UserID, true); err != nil {
		return nil, err
	}
	raw, err := s.Deposits.List(ctx, q)
	if err != nil {
		return nil, err
	}
	return withNarrowed(raw, q.ByKind)
}

// DepositDetail returns one of wallet-service's deposits.
func (s *Service) DepositDetail(ctx context.Context, p Principal, id string) (json.RawMessage, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such deposit")
	}
	return s.Deposits.Get(ctx, id)
}

func (s *Service) depositDecision(p Principal, id, reason string) error {
	if err := p.require(domain.PermDepositsReview); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil {
		return apperr.NotFound("no such deposit")
	}
	return needReason(reason)
}

// CreditDeposit gives an unclaimed deposit's funds, booked to
// UNCLAIMED_DEPOSIT, to its user: the same asset and amount (the ledger
// audits it as ledger.unclaimed_released). The same request again with
// the same key answers with the deposit it credited.
func (s *Service) CreditDeposit(ctx context.Context, p Principal, key, id, reason string) (json.RawMessage, error) {
	if err := s.depositDecision(p, id, reason); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	c, err := s.claimKey(ctx, p, key, scopeCredit, fingerprint(id, reason))
	if err != nil {
		return nil, err
	}
	raw, err := s.Deposits.Credit(ctx, id, p.Admin.Email, reason)
	if err == nil || c.Fresh || apperr.From(err).Kind != apperr.KindConflict {
		return raw, err
	}
	current, gerr := s.Deposits.Get(ctx, id)
	var d struct {
		Resolution string `json:"resolution"`
		ResolvedBy string `json:"resolved_by"`
	}
	if gerr != nil || json.Unmarshal(current, &d) != nil || d.Resolution != "CREDITED" || d.ResolvedBy != p.Admin.Email {
		return nil, err
	}
	return current, nil
}

// AssignDeposit credits a deposit of nobody (B7a: a custodian's deposit to
// an address no user has) to the user the administrator names: a fund
// operation (DEPOSIT_ASSIGN), carried out alone within the single-person
// limits and otherwise decided by a second administrator; the same
// request under its key returns it (C5.5 ㉑).
func (s *Service) AssignDeposit(ctx context.Context, p Principal, key, depositID, userID, reason string) (domain.Approval, error) {
	return s.SubmitFunds(ctx, p, FundRequest{
		Kind: domain.KindDepositAssign, DepositID: depositID, UserID: userID, Reason: reason, Direct: true, Key: key,
	})
}

// depositOfNobody is what a DEPOSIT_ASSIGN needs of its deposit.
type depositOfNobody struct {
	UserID     string          `json:"user_id"`
	Asset      string          `json:"asset"`
	Amount     decimal.Decimal `json:"amount"`
	Network    string          `json:"network"`
	Address    string          `json:"address"`
	TxHash     string          `json:"tx_hash"`
	Unclaimed  bool            `json:"unclaimed"`
	Resolution string          `json:"resolution"`
	// AddressOwner is the user its address belongs to now, or belonged to
	// before it was retired (AddressOwnerRetired).
	AddressOwner        *string `json:"address_owner"`
	AddressOwnerRetired bool    `json:"address_owner_retired"`
}

// notHolder reports whether its address has a holder, now or before it
// was retired, other than user: then a second administrator decides
// whatever its worth (C5.5 ㉑).
func (d depositOfNobody) notHolder(user string) bool {
	return d.AddressOwner != nil && *d.AddressOwner != "" && !strings.EqualFold(*d.AddressOwner, user)
}

// holderPayload records its address's holder in its operation: the
// former holder of a retired address, or the one it has now.
func (d depositOfNobody) holderPayload(p map[string]string) {
	switch {
	case d.AddressOwner == nil || *d.AddressOwner == "":
	case d.AddressOwnerRetired:
		p["former_holder"] = strings.ToLower(*d.AddressOwner)
	default:
		p["address_owner"] = strings.ToLower(*d.AddressOwner)
	}
}

// ErrNotNobodys refuses to assign a deposit that is not one of nobody
// waiting for a decision.
var ErrNotNobodys = apperr.New(apperr.KindConflict, "ADMIN_DEPOSIT_NOT_UNOWNED",
	"only a deposit of nobody (an address no user has) waiting for a decision is credited to a user")

// depositOfNobody reads a deposit of nobody waiting for a decision.
func (s *Service) depositOfNobody(ctx context.Context, id string) (depositOfNobody, error) {
	raw, err := s.Deposits.Get(ctx, id)
	if err != nil {
		return depositOfNobody{}, err
	}
	var d depositOfNobody
	if err := json.Unmarshal(raw, &d); err != nil {
		return depositOfNobody{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "wallet-service answered badly")
	}
	if d.UserID != domain.NoOwner || !d.Unclaimed || d.Resolution != "" || d.Asset == "" || !d.Amount.IsPositive() {
		return depositOfNobody{}, ErrNotNobodys
	}
	return d, nil
}

// DismissDeposit closes a deposit that waited for a decision without
// moving funds (wallet-service audits it as wallet.deposit.dismissed).
func (s *Service) DismissDeposit(ctx context.Context, p Principal, id, reason string) (json.RawMessage, error) {
	if err := s.depositDecision(p, id, reason); err != nil {
		return nil, err
	}
	return s.Deposits.Dismiss(ctx, id, p.Admin.Email, strings.TrimSpace(reason))
}

// BackfillCheck is what a backfill would book and how it would be decided.
type BackfillCheck struct {
	ports.ManualCheck
	// ValueUSDT is the amount's worth (nil: no price, so a second
	// administrator decides).
	ValueUSDT *decimal.Decimal
}

// CheckBackfill checks a backfill without booking it: wallet-service
// finds the user and the asset, the console its worth.
func (s *Service) CheckBackfill(ctx context.Context, p Principal, b ports.ManualDeposit) (BackfillCheck, error) {
	in := FundRequest{Kind: domain.KindDepositBackfill, Backfill: &b, Reason: "check"}
	if err := p.require(domain.PermDepositsReview); err != nil {
		return BackfillCheck{}, err
	}
	if err := in.validate(); err != nil {
		return BackfillCheck{}, err
	}
	b.Actor = p.Admin.Email
	check, err := s.Deposits.CheckManual(ctx, b)
	if err != nil {
		return BackfillCheck{}, err
	}
	out := BackfillCheck{ManualCheck: check}
	if v, ok := s.worth(ctx, check.Asset, b.Amount); ok {
		out.ValueUSDT = &v
	}
	return out, nil
}

// Backfill books a custodian deposit whose callback was lost, through the
// fund operations' guardrails: in single-person mode within the limits at
// once, otherwise a second administrator approves it (design 2026-10-02
// §4.3; the custodian could not be asked).
func (s *Service) Backfill(ctx context.Context, p Principal, key string, b ports.ManualDeposit, reason string) (domain.Approval, error) {
	return s.SubmitFunds(ctx, p, FundRequest{Kind: domain.KindDepositBackfill, Backfill: &b, Reason: reason, Direct: true, Key: key})
}

// WithdrawalDetail returns a withdrawal with its address-book entry and
// its user's withdrawals' worth today and this month.
func (s *Service) WithdrawalDetail(ctx context.Context, p Principal, id string) (json.RawMessage, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such withdrawal")
	}
	return s.Wallet.Detail(ctx, id)
}

// HoldWithdrawal puts a withdrawal in review on hold with a note, or off
// hold (wallet-service audits it).
func (s *Service) HoldWithdrawal(ctx context.Context, p Principal, id string, hold bool, note string) (json.RawMessage, error) {
	if err := p.require(domain.PermWithdrawalsEdit); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such withdrawal")
	}
	if hold {
		if err := needReason(note); err != nil {
			return nil, err
		}
	}
	return s.Wallet.Hold(ctx, id, hold, p.Admin.Email, strings.TrimSpace(note))
}
