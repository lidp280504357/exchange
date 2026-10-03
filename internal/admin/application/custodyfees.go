package application

import (
	"context"
	"encoding/json"
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
	var err error
	if q.Provider, err = custodian(q.Provider); err != nil {
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
	if b.Asset != "" || b.Amount.IsPositive() {
		if err := s.withinReported(ctx, b); err != nil {
			return nil, err
		}
	}
	b.Actor, b.Reason = p.Admin.Email, strings.TrimSpace(b.Reason)
	return s.Wallet.BookFee(ctx, b)
}

// feeBound is how many times what the custodian reported a fee booked as
// found charged may be: in the reported asset by amount, in another by its
// worth in USDT at the last prices. One administrator books no more from
// GAS_SUPPLY alone; beyond it exchangectl wallet custody-fee does
// (review ㉖).
const feeBound = 5

// Fees booked as found charged beyond the bound, or not comparable.
var (
	ErrFeeAboveReported = apperr.New(apperr.KindUnprocessable, "ADMIN_FEE_ABOVE_REPORTED",
		"a fee is booked at most 5 times what the custodian reported (in USDT at the last prices for another asset): book more with exchangectl wallet custody-fee")
	ErrFeeUnpriced = apperr.New(apperr.KindUnprocessable, "ADMIN_FEE_UNPRICED",
		"a fee in another asset than reported is compared in USDT, and one of the two has no fresh price: book it with exchangectl wallet custody-fee")
)

// withinReported checks a fee booked as found charged against what the
// custodian reported (feeBound). A withdrawal whose fee is not held is
// left to wallet-service, which says why it cannot be booked.
func (s *Service) withinReported(ctx context.Context, b ports.FeeBooking) error {
	f, err := s.heldFee(ctx, b.WithdrawalID)
	if err != nil || f == nil {
		return err
	}
	asset, amount := f.Asset, f.Amount
	if b.Asset != "" {
		asset = b.Asset
	}
	if b.Amount.IsPositive() {
		amount = b.Amount
	}
	bound := decimal.NewFromInt(feeBound)
	refuse := func() error {
		return ErrFeeAboveReported.WithDetail("reported", f.Amount.String()+" "+f.Asset).WithDetail("booked", amount.String()+" "+asset)
	}
	if asset == f.Asset {
		if amount.GreaterThan(f.Amount.Mul(bound)) {
			return refuse()
		}
		return nil
	}
	value := s.valuer(ctx, priceMaxAge)
	booked, ok := value(asset, amount)
	reported, rok := value(f.Asset, f.Amount)
	if !ok || !rok {
		return ErrFeeUnpriced.WithDetail("reported", f.Amount.String()+" "+f.Asset).WithDetail("booked", amount.String()+" "+asset)
	}
	if booked.GreaterThan(reported.Mul(bound)) {
		return refuse()
	}
	return nil
}

// heldFeeRow is what the bound needs of a fee held for a person.
type heldFeeRow struct {
	Asset  string
	Amount decimal.Decimal
}

// heldFee finds a withdrawal's fee among those held for a person (the
// first 10,000); nil when it is not one of them.
func (s *Service) heldFee(ctx context.Context, withdrawalID string) (*heldFeeRow, error) {
	cursor := ""
	for range 50 {
		raw, err := s.Wallet.Fees(ctx, ports.FeeQuery{Status: "HELD", Cursor: cursor, Limit: 200})
		if err != nil {
			return nil, err
		}
		var page struct {
			Items []struct {
				WithdrawalID string          `json:"withdrawal_id"`
				Asset        string          `json:"asset"`
				Amount       decimal.Decimal `json:"amount"`
			} `json:"items"`
			NextCursor *string `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "wallet-service answered badly")
		}
		for _, f := range page.Items {
			if strings.EqualFold(f.WithdrawalID, withdrawalID) {
				return &heldFeeRow{Asset: f.Asset, Amount: f.Amount}, nil
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return nil, nil
		}
		cursor = *page.NextCursor
	}
	return nil, nil
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
