package application

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
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
	return s.Deposits.List(ctx, q)
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
// audits it as ledger.unclaimed_released).
func (s *Service) CreditDeposit(ctx context.Context, p Principal, id, reason string) (json.RawMessage, error) {
	if err := s.depositDecision(p, id, reason); err != nil {
		return nil, err
	}
	return s.Deposits.Credit(ctx, id, p.Admin.Email, strings.TrimSpace(reason))
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
func (s *Service) Backfill(ctx context.Context, p Principal, b ports.ManualDeposit, reason string) (domain.Approval, error) {
	return s.SubmitFunds(ctx, p, FundRequest{Kind: domain.KindDepositBackfill, Backfill: &b, Reason: reason, Direct: true})
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
