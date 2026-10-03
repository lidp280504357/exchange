package application

import (
	"context"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

var (
	custodianRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,15}$`)
	assetRE     = regexp.MustCompile(`^[A-Z0-9]{1,16}$`)
)

// custodian normalizes a custodian's name (UDUN, UDUNMOCK): empty stays
// empty, wallet-service's default.
func custodian(provider string) (string, error) {
	provider = strings.ToUpper(strings.TrimSpace(provider))
	if provider != "" && !custodianRE.MatchString(provider) {
		return "", apperr.Invalid("provider is a custodian's name, e.g. UDUN")
	}
	return provider, nil
}

// CustodyFees returns a page of the custodians' withdrawal fees: those a
// person must decide (HELD), those booked or waiting to be (BOOKABLE), and
// those written off (C6).
func (s *Service) CustodyFees(ctx context.Context, p Principal, q ports.FeeQuery) ([]byte, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	q.Status, q.Limit = strings.ToUpper(strings.TrimSpace(q.Status)), pageLimit(q.Limit)
	switch q.Status {
	case "", "HELD", "BOOKABLE", "WRITTEN_OFF":
	default:
		return nil, apperr.Invalid("status is HELD, BOOKABLE or WRITTEN_OFF")
	}
	return s.Wallet.Fees(ctx, q)
}

// BookCustodyFee books a custodian's fee held for a person from
// GAS_SUPPLY, as reported (asset empty, amount zero) or in the asset and
// amount found charged; wallet-service audits it with this administrator
// as the actor (wallet.custody.fee.book), as exchangectl wallet
// custody-fee does (C6). Done once: a fee that waits for no one is a
// conflict, so a repeated request changes nothing.
func (s *Service) BookCustodyFee(ctx context.Context, p Principal, b ports.FeeBooking) ([]byte, error) {
	if err := feeDecision(p, b.WithdrawalID, b.Reason); err != nil {
		return nil, err
	}
	b.Asset = strings.ToUpper(strings.TrimSpace(b.Asset))
	if b.Asset != "" && !assetRE.MatchString(b.Asset) {
		return nil, apperr.Invalid("asset is an asset's code, e.g. TRX")
	}
	if b.Amount.IsNegative() || !b.Amount.Equal(b.Amount.Truncate(18)) || b.Amount.GreaterThan(decimal.New(1, 12)) {
		return nil, apperr.Invalid("the amount is positive, below 10^12, in at most 18 decimals")
	}
	b.Actor, b.Reason = p.Admin.Email, strings.TrimSpace(b.Reason)
	return s.Wallet.BookFee(ctx, b)
}

// WriteOffCustodyFee writes off a custodian's fee held for a person (not
// taken from the coin balances, or reported in another unit), audited by
// wallet-service for this administrator (C6).
func (s *Service) WriteOffCustodyFee(ctx context.Context, p Principal, withdrawalID, reason string) ([]byte, error) {
	if err := feeDecision(p, withdrawalID, reason); err != nil {
		return nil, err
	}
	return s.Wallet.WriteOffFee(ctx, withdrawalID, p.Admin.Email, strings.TrimSpace(reason))
}

// feeDecision checks a decision on a fee: it moves GAS_SUPPLY, a
// platform account, so it needs ledger.adjust.approve, a reason and a
// withdrawal.
func feeDecision(p Principal, withdrawalID, reason string) error {
	if err := p.require(domain.PermAdjustApprove); err != nil {
		return err
	}
	if err := needReason(reason); err != nil {
		return err
	}
	if _, err := uuid.Parse(withdrawalID); err != nil {
		return apperr.NotFound("no such withdrawal with a custodian")
	}
	return nil
}
