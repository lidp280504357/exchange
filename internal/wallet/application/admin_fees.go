package application

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

// CustodyFees pages through the custodians' withdrawal fees for the
// console, newest first, as exchangectl wallet custody-fees lists the held
// ones: of a status (HELD, BOOKABLE, WRITTEN_OFF; "" any), after a fee
// (the cursor is its transaction), at most limit (1 to 200, default 50).
// It returns the cursor of the next page, "" at the end.
func (s *Service) CustodyFees(ctx context.Context, status, after string, limit int) ([]domain.CustodyFee, string, error) {
	status = strings.ToUpper(strings.TrimSpace(status))
	switch status {
	case "", domain.FeeHeld, domain.FeeBookable, domain.FeeWrittenOff:
	default:
		return nil, "", apperr.Invalid("status is HELD, BOOKABLE or WRITTEN_OFF")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := s.Store.Read().ChainFees().Page(ctx, status, after, limit+1)
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
		return domain.ChainFee{}, apperr.NotFound("no such withdrawal with a custodian")
	}
	return ResolveCustodyFee(ctx, s.Store, s.Custodied, d, s.Now())
}
