package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/ledger/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// FuturesResult is a booked settlement request of derivatives-service.
type FuturesResult struct {
	Outcomes []domain.FuturesOutcome
	Replayed bool
}

// SettleFutures books a settlement step of a user's perpetual contract
// trading (plan §7.3 task 4): the moves in order, one journal each
// (domain.FuturesPostings), and the outcome in futures_settlements, all in
// one transaction. The same key with the same request returns the first
// outcome, with another request COMMON_IDEMPOTENCY_CONFLICT; a refusal
// (an overdrawn exact move, an insurance fund too small for a shortfall)
// books nothing.
func (s *Service) SettleFutures(ctx context.Context, req domain.FuturesRequest) (FuturesResult, error) {
	if err := req.Validate(); err != nil {
		return FuturesResult{}, err
	}
	decimals, err := s.Assets.Decimals(ctx, req.Asset)
	if err != nil {
		return FuturesResult{}, err
	}
	for i, m := range req.Moves {
		if !fits(m.Amount, decimals) || (m.Limit != nil && !fits(*m.Limit, decimals)) {
			return FuturesResult{}, apperr.New(apperr.KindInvalid, "LEDGER_AMOUNT_PRECISION",
				fmt.Sprintf("move %d has more than %d decimals", i+1, decimals)).WithDetail("asset", req.Asset).WithDetail("decimals", decimals)
		}
	}
	hash := req.Hash()
	var res FuturesResult
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		res, err = s.settleFutures(ctx, r, req, hash)
		return err
	})
	if c, ok := pg.UniqueViolation(err); ok && c == "futures_settlements_pkey" {
		// A concurrent request with the same key won: report it.
		done, err := s.Store.Read().Futures().ByKey(ctx, req.IdemKey)
		if err != nil {
			return FuturesResult{}, err
		}
		if done == nil {
			return FuturesResult{}, errors.New("futures settlement vanished after a key conflict")
		}
		if !bytes.Equal(done.RequestHash, hash) {
			return FuturesResult{}, domain.ErrIdempotencyConflict
		}
		return FuturesResult{Outcomes: done.Outcomes, Replayed: true}, nil
	}
	return res, err
}

func (s *Service) settleFutures(ctx context.Context, r ports.Repos, req domain.FuturesRequest, hash []byte) (FuturesResult, error) {
	done, err := r.Futures().ByKey(ctx, req.IdemKey)
	if err != nil {
		return FuturesResult{}, err
	}
	if done != nil {
		if !bytes.Equal(done.RequestHash, hash) {
			return FuturesResult{}, domain.ErrIdempotencyConflict
		}
		return FuturesResult{Outcomes: done.Outcomes, Replayed: true}, nil
	}
	// Every account the moves may touch is locked up front, in lock order.
	accounts, err := r.Accounts().Lock(ctx, req.FuturesAccounts())
	if err != nil {
		return FuturesResult{}, err
	}
	var user domain.Account
	for _, a := range accounts {
		if a.Key == domain.UserAccount(req.UserID, domain.AccountFutures, req.Asset) {
			user = a
		}
	}
	plan, err := domain.FuturesPostings(req, user)
	if err != nil {
		return FuturesResult{}, err
	}
	journals := make([]string, len(plan.Postings))
	for i, p := range plan.Postings {
		posted, err := s.post(ctx, r, p)
		if err != nil {
			return FuturesResult{}, err
		}
		journals[i] = posted.JournalID
	}
	for i, at := range plan.Posted {
		if at >= 0 {
			plan.Outcomes[i].JournalID = journals[at]
		}
	}
	if err := r.Futures().Insert(ctx, domain.FuturesSettlement{
		IdemKey: req.IdemKey, UserID: req.UserID, RequestHash: hash, Reference: req.Reference, Outcomes: plan.Outcomes, CreatedAt: s.Now(),
	}); err != nil {
		return FuturesResult{}, err
	}
	return FuturesResult{Outcomes: plan.Outcomes}, nil
}

func fits(d decimal.Decimal, decimals int32) bool { return d.Equal(d.Truncate(decimals)) }

// FundInsurance adds simulated funds to the insurance fund against
// ADJUSTMENT (INSURANCE_CONTRIBUTION) with an audit event: the test
// environment's seed of the fund (§11.7), behind ledger.manual_adjustment.
// Real funding comes on chain (FundSystemAccount).
func (s *Service) FundInsurance(ctx context.Context, idemKey, asset string, amount decimal.Decimal, actor, reason string) (Result, error) {
	if !s.Flags.Enabled(flags.KeyManualAdjustment, flags.Subject{}) {
		return Result{}, apperr.New(apperr.KindForbidden, "LEDGER_ADJUSTMENT_DISABLED", "manual adjustments are switched off (ledger.manual_adjustment)")
	}
	if strings.TrimSpace(idemKey) == "" {
		return Result{}, apperr.Invalid("an idempotency key is required")
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return Result{}, apperr.Invalid("a reason is required")
	}
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, err
	}
	p, err := domain.FundInsurancePosting("insurance:"+idemKey, asset, amount, decimals, reason)
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		if res, err = s.post(ctx, r, p); err != nil || res.Replayed {
			return err
		}
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "system:INSURANCE_FUND", Action: "ledger.insurance_fund", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"asset":%q,"amount":%q,"journal_id":%q}`, asset, amount.String(), res.JournalID),
		}, "actor", actor)
	})
	return res, err
}
