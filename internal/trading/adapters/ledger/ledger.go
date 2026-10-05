// Package ledger freezes order funds through ledger-service.
package ledger

import (
	"context"

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

// accountType is the ledger's name of the account; orders stored before
// margin trading have none and trade from SPOT.
func accountType(a domain.Account) string {
	if a.Type == "" {
		return string(domain.AccountSpot)
	}
	return string(a.Type)
}
