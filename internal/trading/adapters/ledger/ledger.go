// Package ledger freezes order funds through ledger-service.
package ledger

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/trading/domain"
)

// Client implements ports.Ledger.
type Client struct{ c ledgerv1.LedgerServiceClient }

// New wraps a LedgerService client.
func New(c ledgerv1.LedgerServiceClient) *Client { return &Client{c: c} }

// Freeze locks the order's funds in its account (ORDER_FREEZE): SPOT, or
// a margin account, an isolated one scoped to its pair.
func (c *Client) Freeze(ctx context.Context, key string, a domain.Account, asset string, amount decimal.Decimal, orderID string) error {
	_, err := c.c.Freeze(ctx, &ledgerv1.FreezeRequest{
		IdempotencyKey: key, UserId: a.UserID, AccountType: accountType(a), Scope: a.Scope, Asset: asset,
		Amount: amount.String(), EntryType: "ORDER_FREEZE", Reference: orderID,
	})
	return err
}

// Unfreeze releases an order's unused funds (ORDER_UNFREEZE).
func (c *Client) Unfreeze(ctx context.Context, key string, a domain.Account, asset string, amount decimal.Decimal, orderID string) error {
	_, err := c.c.Unfreeze(ctx, &ledgerv1.UnfreezeRequest{
		IdempotencyKey: key, UserId: a.UserID, AccountType: accountType(a), Scope: a.Scope, Asset: asset,
		Amount: amount.String(), EntryType: "ORDER_UNFREEZE", Reference: orderID,
	})
	return err
}

// RepayReleased repays what an order borrowed, up to upTo, as it ends
// (B160; ledger-service RepayReleased).
func (c *Client) RepayReleased(ctx context.Context, a domain.Account, asset string, upTo decimal.Decimal, orderID string) (decimal.Decimal, error) {
	resp, err := c.c.RepayReleased(ctx, &ledgerv1.RepayReleasedRequest{
		OrderId: orderID, UserId: a.UserID, AccountType: accountType(a), Scope: a.Scope, Asset: asset, UpTo: upTo.String(),
	})
	if err != nil {
		return decimal.Zero, err
	}
	repaid, err := decimal.NewFromString(resp.GetRepaid())
	if err != nil {
		return decimal.Zero, fmt.Errorf("the ledger's repayment %q: %w", resp.GetRepaid(), err)
	}
	return repaid, nil
}

// MarginDebt returns what the margin account owes of asset, the principal
// and the interest (GetMarginBalances).
func (c *Client) MarginDebt(ctx context.Context, a domain.Account, asset string) (decimal.Decimal, error) {
	resp, err := c.c.GetMarginBalances(ctx, &ledgerv1.GetMarginBalancesRequest{UserId: a.UserID})
	if err != nil {
		return decimal.Zero, err
	}
	for _, b := range resp.GetBalances() {
		if b.GetAccountType() != accountType(a) || b.GetScope() != a.Scope || b.GetAsset() != asset {
			continue
		}
		return owed(b.GetBorrowed(), b.GetInterest())
	}
	return decimal.Zero, nil
}

// MarginBorrowers counts the margin accounts that owe anything
// (ListMarginDebts).
func (c *Client) MarginBorrowers(ctx context.Context) (int, error) {
	resp, err := c.c.ListMarginDebts(ctx, &ledgerv1.ListMarginDebtsRequest{})
	if err != nil {
		return 0, err
	}
	owing := map[[3]string]bool{}
	for _, d := range resp.GetDebts() {
		debt, err := owed(d.GetBorrowed(), d.GetInterest())
		if err != nil {
			return 0, err
		}
		if debt.IsPositive() {
			owing[[3]string{d.GetUserId(), d.GetAccountType(), d.GetScope()}] = true
		}
	}
	return len(owing), nil
}

// owed adds a debt's principal and interest, empty strings counting as 0.
func owed(borrowed, interest string) (decimal.Decimal, error) {
	sum := decimal.Zero
	for _, v := range []string{borrowed, interest} {
		if v == "" {
			continue
		}
		d, err := decimal.NewFromString(v)
		if err != nil {
			return decimal.Zero, fmt.Errorf("a margin debt of %q: %w", v, err)
		}
		sum = sum.Add(d)
	}
	return sum, nil
}

// accountType is the ledger's name of the account; orders stored before
// margin trading have none and trade from SPOT.
func accountType(a domain.Account) string {
	if a.Type == "" {
		return string(domain.AccountSpot)
	}
	return string(a.Type)
}
