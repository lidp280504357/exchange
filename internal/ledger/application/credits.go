package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/ledger/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/pg"
)

// ParseCredits reads "USDT:10000,BTC:0.1".
func ParseCredits(s string) ([]Credit, error) {
	var out []Credit
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		asset, amount, ok := strings.Cut(part, ":")
		d, err := decimal.NewFromString(amount)
		if !ok || err != nil || !d.IsPositive() {
			return nil, fmt.Errorf("credit %q: want ASSET:AMOUNT", part)
		}
		out = append(out, Credit{Asset: strings.ToUpper(asset), Amount: d})
	}
	return out, nil
}

func (s *Service) credits(ctx context.Context, list []Credit) ([]domain.Credit, error) {
	out := make([]domain.Credit, 0, len(list))
	for _, c := range list {
		decimals, err := s.Assets.Decimals(ctx, c.Asset)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.Credit{Asset: c.Asset, Amount: c.Amount, Decimals: decimals})
	}
	return out, nil
}

// OnUserRegistered gives a new user the welcome credits the operators set
// (design 2026-10-04 §4.2) while ledger.welcome_credit is on for them
// (ADR-0005): both must hold, and an empty list gives nothing. The
// journal's key makes it once per user; a redelivered event finds the
// grant made, whatever the credits are now.
func (s *Service) OnUserRegistered(ctx context.Context, eventID, userID, region string) error {
	if !s.Flags.Enabled(flags.KeyWelcomeCredit, flags.Subject{UserID: userID, Region: region}) {
		return nil
	}
	list, err := s.grantCredits(ctx)
	if err != nil || len(list) == 0 {
		return err
	}
	key := "welcome:" + userID
	if j, err := s.Store.Read().Journals().ByIdemKey(ctx, key); err != nil || j != nil {
		return err
	}
	credits, err := s.credits(ctx, list)
	if err != nil {
		return err
	}
	p, err := domain.AdjustmentPosting(key, userID, credits, "welcome credit (simulated funds)")
	if err != nil {
		return err
	}
	p.SourceEventID = eventID
	_, err = s.Post(ctx, p)
	return err
}

// Adjust credits (or debits, with a negative amount) a user's SPOT account
// against ADJUSTMENT and records an audit event, in one transaction. It is
// the operator's correction tool of phase 1 behind
// ledger.manual_adjustment; phase 2 adds two-person approval (§5.9).
func (s *Service) Adjust(ctx context.Context, idemKey, userID, asset string, amount decimal.Decimal, actor, reason string) (Result, error) {
	return s.AdjustAccount(ctx, idemKey, userID, domain.AccountSpot, asset, amount, actor, reason)
}

// AdjustAccount is Adjust on the user's SPOT or FUTURES account; the
// audit event names the account type unless it is SPOT.
func (s *Service) AdjustAccount(ctx context.Context, idemKey, userID, accountType, asset string, amount decimal.Decimal, actor, reason string) (Result, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return Result{}, apperr.Invalid("user_id must be a UUID")
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return Result{}, apperr.Invalid("a reason is required")
	}
	credits, err := s.credits(ctx, []Credit{{Asset: asset, Amount: amount}})
	if err != nil {
		return Result{}, err
	}
	p, err := domain.AccountAdjustmentPosting("adjust:"+idemKey, userID, accountType, credits, reason)
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
		details := fmt.Sprintf(`{"asset":%q,"amount":%q,"journal_id":%q}`, asset, amount.String(), res.JournalID)
		if accountType != domain.AccountSpot {
			details = fmt.Sprintf(`{"account_type":%q,"asset":%q,"amount":%q,"journal_id":%q}`, accountType, asset, amount.String(), res.JournalID)
		}
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "ledger.manual_adjustment", Actor: actor, Reason: reason, Details: details,
		}, "actor", actor)
	})
	return res, err
}

func pgUnique(err error) (string, bool) { return pg.UniqueViolation(err) }

// CreditDeposit books a confirmed deposit (§11.5) once per deposit: the
// journal key deposit:<id> turns a redelivery into a replay.
func (s *Service) CreditDeposit(ctx context.Context, eventID string, d domain.Deposit) (Result, error) {
	p, err := domain.DepositPosting(d)
	if err != nil {
		return Result{}, err
	}
	p.SourceEventID = eventID
	return s.Post(ctx, p)
}

// AdjustHouse credits (or debits) HOUSE's inventory against ADJUSTMENT,
// audited like a user's adjustment (ADR-0013: HOUSE's simulated funding).
// The caller checks ledger.manual_adjustment.
func (s *Service) AdjustHouse(ctx context.Context, idemKey, asset string, amount decimal.Decimal, actor, reason string) (Result, error) {
	if len(strings.TrimSpace(reason)) < 3 {
		return Result{}, apperr.Invalid("a reason is required")
	}
	credits, err := s.credits(ctx, []Credit{{Asset: asset, Amount: amount}})
	if err != nil {
		return Result{}, err
	}
	p, err := domain.HouseAdjustmentPosting("adjust-house:"+idemKey, credits, reason)
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
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "house:" + domain.AccountMarketMaker, Action: "ledger.manual_adjustment", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"asset":%q,"amount":%q,"journal_id":%q}`, asset, amount.String(), res.JournalID),
		}, "actor", actor)
	})
	return res, err
}

// AdjustApproved is AdjustAccount for the admin console, called once the
// adjustment was approved (§5.12) on a user's SPOT ("" too) or FUTURES
// account; it needs ledger.manual_adjustment on.
func (s *Service) AdjustApproved(ctx context.Context, idemKey, userID, accountType, asset string, amount decimal.Decimal, actor, reason string) (Result, error) {
	if !s.Flags.Enabled(flags.KeyManualAdjustment, flags.Subject{UserID: userID}) {
		return Result{}, apperr.New(apperr.KindForbidden, "LEDGER_ADJUSTMENT_DISABLED", "manual adjustments are switched off (ledger.manual_adjustment)")
	}
	if strings.TrimSpace(idemKey) == "" {
		return Result{}, apperr.Invalid("an idempotency key is required")
	}
	if accountType == "" {
		accountType = domain.AccountSpot
	}
	return s.AdjustAccount(ctx, idemKey, userID, accountType, asset, amount, actor, reason)
}
