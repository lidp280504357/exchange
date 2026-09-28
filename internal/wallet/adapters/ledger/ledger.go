// Package ledger books the wallet's journals through ledger-service.
package ledger

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
)

// Client implements ports.Ledger.
type Client struct{ c ledgerv1.LedgerServiceClient }

// New wraps a LedgerService client.
func New(c ledgerv1.LedgerServiceClient) *Client { return &Client{c: c} }

// BookChainFee books gas the platform paid.
func (c *Client) BookChainFee(ctx context.Context, key, asset string, amount decimal.Decimal, reference string) (string, error) {
	resp, err := c.c.BookChainFee(ctx, &ledgerv1.BookChainFeeRequest{
		IdempotencyKey: key, Asset: asset, Amount: amount.String(), Reference: reference,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

// Fund books a platform funding to a system account.
func (c *Client) Fund(ctx context.Context, key, accountType, asset string, amount decimal.Decimal, reference string) (string, error) {
	resp, err := c.c.FundSystemAccount(ctx, &ledgerv1.FundSystemAccountRequest{
		IdempotencyKey: key, AccountType: accountType, Asset: asset, Amount: amount.String(), Reference: reference,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

// SystemBalances returns the available balance of each system account.
func (c *Client) SystemBalances(ctx context.Context, asset string) (map[string]decimal.Decimal, error) {
	resp, err := c.c.GetSystemBalances(ctx, &ledgerv1.GetSystemBalancesRequest{Asset: asset})
	if err != nil {
		return nil, err
	}
	out := map[string]decimal.Decimal{}
	for _, b := range resp.GetBalances() {
		v, err := decimal.NewFromString(b.GetAvailable())
		if err != nil {
			return nil, fmt.Errorf("balance of %s: %w", b.GetAccountType(), err)
		}
		out[b.GetAccountType()] = v
	}
	return out, nil
}
