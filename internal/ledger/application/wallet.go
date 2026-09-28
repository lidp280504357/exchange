package application

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
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

// SystemBalances returns the system accounts in asset.
func (s *Service) SystemBalances(ctx context.Context, asset string) ([]domain.Account, error) {
	if asset == "" {
		return nil, apperr.Invalid("asset is required")
	}
	all, err := s.Store.Read().Accounts().ByOwner(ctx, domain.OwnerSystem, "")
	if err != nil {
		return nil, err
	}
	var out []domain.Account
	for _, a := range all {
		if a.Key.Asset == asset {
			out = append(out, a)
		}
	}
	return out, nil
}
