package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/ledger/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/pg"
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

// OnUserRegistered gives a new user the simulated funds of phase 1 while
// ledger.welcome_credit is on for them (test environments only, ADR-0005).
// The journal's key makes it once per user.
func (s *Service) OnUserRegistered(ctx context.Context, eventID, userID, region string) error {
	if len(s.WelcomeCredits) == 0 || !s.Flags.Enabled(flags.KeyWelcomeCredit, flags.Subject{UserID: userID, Region: region}) {
		return nil
	}
	credits, err := s.credits(ctx, s.WelcomeCredits)
	if err != nil {
		return err
	}
	p, err := domain.AdjustmentPosting("welcome:"+userID, userID, credits, "welcome credit (simulated funds)")
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
	p, err := domain.AdjustmentPosting("adjust:"+idemKey, userID, credits, reason)
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
			Target: "user:" + userID, Action: "ledger.manual_adjustment", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"asset":%q,"amount":%q,"journal_id":%q}`, asset, amount.String(), res.JournalID),
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
