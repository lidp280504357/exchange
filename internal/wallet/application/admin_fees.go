package application

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/wallet/domain"
)

// CustodyFees pages through the custodians' withdrawal fees for the
// console, newest first, as exchangectl wallet custody-fees lists the held
// ones: of a custodian (UDUN, UDUNMOCK; "" any; one neither configured nor
// known is not found, as by Custody: review AT) and a status (HELD,
// BOOKABLE, WRITTEN_OFF; "" any), after a fee
// (the cursor is its transaction; an unknown one is refused), at most
// limit (default 50, at most 200). It returns the cursor of the next page,
// "" at the end.
func (s *Service) CustodyFees(ctx context.Context, provider, status, after string, limit int) ([]domain.CustodyFee, string, error) {
	provider, status = strings.ToUpper(strings.TrimSpace(provider)), strings.ToUpper(strings.TrimSpace(status))
	if provider != "" && !s.custodian(provider) {
		return nil, "", apperr.NotFound("no such custodian")
	}
	switch status {
	case "", domain.FeeHeld, domain.FeeBookable, domain.FeeWrittenOff:
	default:
		return nil, "", apperr.Invalid("status is HELD, BOOKABLE or WRITTEN_OFF")
	}
	switch {
	case limit <= 0:
		limit = 50
	case limit > 200:
		limit = 200
	}
	list, err := s.Store.Read().ChainFees().Page(ctx, provider, status, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(list) > limit {
		list, next = list[:limit], list[limit-1].TxHash
	}
	return list, next, nil
}

// Custodied is the fee bookings' check against the networks the service
// reads: the decimals of an asset the custodian holds for the platform on
// network, false when it holds none there.
func (s *Service) Custodied(ctx context.Context, provider, asset, network string) (int32, bool, error) {
	n, err := s.Networks.Network(ctx, strings.ToUpper(asset), network)
	if apperr.Is(err, "WALLET_NETWORK_UNKNOWN") {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return n.Decimals, n.Provider == provider, nil
}

// DecideCustodyFee is the console's decision on a custodian's fee held
// for a person, as exchangectl wallet custody-fee makes it (the same
// audit events, the administrator as the actor): book it from GAS_SUPPLY,
// as reported or as found charged, or write it off. An unknown withdrawal
// or one without a fee is not found; a fee that waits for no one is a
// conflict.
func (s *Service) DecideCustodyFee(ctx context.Context, d FeeResolution) (domain.ChainFee, error) {
	if _, err := uuid.Parse(d.WithdrawalID); err != nil {
		return domain.ChainFee{}, ErrFeeNotFound
	}
	return ResolveCustodyFee(ctx, s.Store, s.Custodied, d, s.Now())
}
