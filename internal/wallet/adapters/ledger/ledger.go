// Package ledger books the wallet's journals through ledger-service.
package ledger

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
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

// ReleaseUnclaimed books an unclaimed deposit to its user.
func (c *Client) ReleaseUnclaimed(ctx context.Context, depositID, userID, asset string, amount decimal.Decimal, actor, reason string) (string, error) {
	resp, err := c.c.ReleaseUnclaimed(ctx, &ledgerv1.ReleaseUnclaimedRequest{
		DepositId: depositID, UserId: userID, Asset: asset, Amount: amount.String(), Actor: actor, Reason: reason,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

// UnclaimedRelease returns the journal that released an unclaimed
// deposit, if any, and the user it paid.
func (c *Client) UnclaimedRelease(ctx context.Context, depositID string) (journalID, userID string, err error) {
	resp, err := c.c.GetUnclaimedRelease(ctx, &ledgerv1.GetUnclaimedReleaseRequest{DepositId: depositID})
	if err != nil {
		return "", "", err
	}
	return resp.GetJournalId(), resp.GetUserId(), nil
}

// CreditUnclaimed books a deposit to an address no user has to
// UNCLAIMED_DEPOSIT.
func (c *Client) CreditUnclaimed(ctx context.Context, depositID, asset string, amount decimal.Decimal, network, txHash, reason string) (string, error) {
	resp, err := c.c.CreditUnclaimed(ctx, &ledgerv1.CreditUnclaimedRequest{
		DepositId: depositID, Asset: asset, Amount: amount.String(), Network: network, TxHash: txHash, Reason: reason,
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

// Freeze moves a withdrawal's amount and fee to frozen (WITHDRAW_FREEZE).
func (c *Client) Freeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) (string, error) {
	resp, err := c.c.Freeze(ctx, &ledgerv1.FreezeRequest{
		IdempotencyKey: key, UserId: userID, AccountType: "SPOT", Asset: asset, Amount: amount.String(),
		EntryType: "WITHDRAW_FREEZE", Reference: reference,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

// Unfreeze releases a refused withdrawal's funds (WITHDRAW_UNFREEZE).
func (c *Client) Unfreeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) (string, error) {
	resp, err := c.c.Unfreeze(ctx, &ledgerv1.UnfreezeRequest{
		IdempotencyKey: key, UserId: userID, AccountType: "SPOT", Asset: asset, Amount: amount.String(),
		EntryType: "WITHDRAW_UNFREEZE", Reference: reference,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

// Settle books a broadcast withdrawal (WITHDRAW_SETTLE).
func (c *Client) Settle(ctx context.Context, key, userID, asset string, amount, fee decimal.Decimal, reference string) (string, error) {
	resp, err := c.c.SettleWithdrawal(ctx, &ledgerv1.SettleWithdrawalRequest{
		IdempotencyKey: key, UserId: userID, Asset: asset, Amount: amount.String(), Fee: fee.String(), Reference: reference,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

// TransferInternal completes a withdrawal to another user (INTERNAL_TRANSFER).
func (c *Client) TransferInternal(ctx context.Context, key, fromUser, toUser, asset string, amount decimal.Decimal, reference string) (string, error) {
	resp, err := c.c.TransferInternal(ctx, &ledgerv1.TransferInternalRequest{
		IdempotencyKey: key, FromUserId: fromUser, ToUserId: toUser, Asset: asset, Amount: amount.String(), Reference: reference,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}
