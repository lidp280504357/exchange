package application

import (
	"context"
	"errors"
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
)

// Journals wallet-service asks for (§11.6). Their keys are prefixed per
// kind, so one key cannot serve two kinds of journal.

func (s *Service) postWallet(ctx context.Context, build func(decimals int32) (domain.Posting, error), asset string) (Result, error) {
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, err
	}
	p, err := build(decimals)
	if err != nil {
		return Result{}, err
	}
	return s.Post(ctx, p)
}

func requireKey(key string) error {
	if key == "" {
		return apperr.Invalid("an idempotency key is required")
	}
	return nil
}

// SettleWithdrawal books a broadcast withdrawal (WITHDRAW_SETTLE).
func (s *Service) SettleWithdrawal(ctx context.Context, key, userID, asset string, amount, fee decimal.Decimal, reference string) (Result, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return Result{}, apperr.Invalid("user_id must be a UUID")
	}
	if err := requireKey(key); err != nil {
		return Result{}, err
	}
	return s.postWallet(ctx, func(decimals int32) (domain.Posting, error) {
		return domain.WithdrawSettlePosting("withdraw-settle:"+key, userID, asset, amount, fee, decimals, reference)
	}, asset)
}

// TransferInternal completes a withdrawal to another user's deposit
// address in the ledger (INTERNAL_TRANSFER).
func (s *Service) TransferInternal(ctx context.Context, key, fromUser, toUser, asset string, amount decimal.Decimal, reference string) (Result, error) {
	for _, id := range []string{fromUser, toUser} {
		if _, err := uuid.Parse(id); err != nil {
			return Result{}, apperr.Invalid("user IDs must be UUIDs")
		}
	}
	if err := requireKey(key); err != nil {
		return Result{}, err
	}
	return s.postWallet(ctx, func(decimals int32) (domain.Posting, error) {
		return domain.InternalTransferPosting("internal:"+key, fromUser, toUser, asset, amount, decimals, reference)
	}, asset)
}

// BookChainFee books gas the platform paid (GAS_SUPPLY to
// WITHDRAWAL_PENDING).
func (s *Service) BookChainFee(ctx context.Context, key, asset string, amount decimal.Decimal, reference string) (Result, error) {
	if err := requireKey(key); err != nil {
		return Result{}, err
	}
	return s.postWallet(ctx, func(decimals int32) (domain.Posting, error) {
		return domain.ChainFeePosting("chain-fee:"+key, asset, amount, decimals, reference)
	}, asset)
}

// FundGasSupply moves fee revenue to GAS_SUPPLY for the custodian's fees
// (GasSupplyPosting), with an audit event; the key makes a repeat
// harmless. An operator's posting like the insurance fund's, it needs
// ledger.manual_adjustment on (ADR-0005).
func (s *Service) FundGasSupply(ctx context.Context, key, asset string, amount decimal.Decimal, actor, reason string) (Result, error) {
	if !s.Flags.Enabled(flags.KeyManualAdjustment, flags.Subject{}) {
		return Result{}, apperr.New(apperr.KindForbidden, "LEDGER_ADJUSTMENT_DISABLED", "manual adjustments are switched off (ledger.manual_adjustment)")
	}
	if err := requireKey(key); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return Result{}, apperr.Invalid("an actor and a reason are required")
	}
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, err
	}
	p, err := domain.GasSupplyPosting("gas-supply:"+key, asset, amount, decimals, reason)
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
			Target: "system:" + domain.AccountGasSupply, Action: "ledger.gas_supply", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"asset":%q,"amount":%q,"from":%q,"journal_id":%q}`, asset, amount.String(), domain.AccountFeeRevenue,
				res.JournalID),
		}, "actor", actor)
	})
	return res, err
}

// ResetCustody takes amount of simulated deposits out of what the ledger
// expects the custodians to hold, or puts it back (reverse), with an audit
// event (CustodyResetPosting); the key makes a repeat harmless. An
// operator's posting like the insurance fund's, it needs
// ledger.manual_adjustment on (ADR-0005), and never takes the expectation
// below zero.
func (s *Service) ResetCustody(ctx context.Context, key, asset string, amount decimal.Decimal, reverse bool, actor, reason string) (Result, error) {
	if !s.Flags.Enabled(flags.KeyManualAdjustment, flags.Subject{}) {
		return Result{}, apperr.New(apperr.KindForbidden, "LEDGER_ADJUSTMENT_DISABLED", "manual adjustments are switched off (ledger.manual_adjustment)")
	}
	if err := requireKey(key); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return Result{}, apperr.Invalid("an actor and a reason are required")
	}
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, err
	}
	p, err := domain.CustodyResetPosting("custody-reset:"+key, asset, amount, decimals, reverse, reason)
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		pending, err := r.Accounts().Lock(ctx, []domain.AccountKey{domain.SystemAccount(domain.AccountDepositPending, asset)})
		if err != nil {
			return err
		}
		// DEPOSIT_PENDING is minus what the custodians hold for the
		// platform: the reset cannot take that below nothing (a replay of
		// one that did not is checked by its key below).
		if !reverse && pending[0].Available.Add(amount).IsPositive() {
			if prior, err := r.Journals().ByIdemKey(ctx, p.IdemKey); err != nil || prior == nil {
				return errors.Join(err, apperr.Invalid(fmt.Sprintf("the custodians are expected to hold %s %s, less than %s",
					pending[0].Available.Neg(), asset, amount)))
			}
		}
		if res, err = s.post(ctx, r, p); err != nil || res.Replayed {
			return err
		}
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "system:" + domain.AccountDepositPending, Action: "ledger.custody_reset", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"asset":%q,"amount":%q,"reverse":%t,"to":%q,"journal_id":%q}`, asset, amount.String(), reverse,
				domain.AccountAdjustment, res.JournalID),
		}, "actor", actor)
	})
	return res, err
}

// FundSystemAccount books the platform's own transfer into its wallet as
// a deposit to a system account.
func (s *Service) FundSystemAccount(ctx context.Context, key, accountType, asset string, amount decimal.Decimal, reference string) (Result, error) {
	if err := requireKey(key); err != nil {
		return Result{}, err
	}
	return s.postWallet(ctx, func(decimals int32) (domain.Posting, error) {
		return domain.FundingPosting("fund:"+key, accountType, asset, amount, decimals, reference)
	}, asset)
}

// SystemBalances returns the system accounts in asset, of every asset
// when it is "" (HOUSE's liquidity reads all its MARKET_MAKER accounts at
// once).
func (s *Service) SystemBalances(ctx context.Context, asset string) ([]domain.Account, error) {
	all, err := s.Store.Read().Accounts().ByOwner(ctx, domain.OwnerSystem, "")
	if err != nil {
		return nil, err
	}
	var out []domain.Account
	for _, a := range all {
		if asset == "" || a.Key.Asset == asset {
			out = append(out, a)
		}
	}
	return out, nil
}
