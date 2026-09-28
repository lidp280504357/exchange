// Package ledger freezes order funds through ledger-service.
package ledger

import (
	"context"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
)

// Client implements ports.Ledger.
type Client struct{ c ledgerv1.LedgerServiceClient }

// New wraps a LedgerService client.
func New(c ledgerv1.LedgerServiceClient) *Client { return &Client{c: c} }

// Freeze locks the order's funds in the user's SPOT account (ORDER_FREEZE).
func (c *Client) Freeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, orderID string) error {
	_, err := c.c.Freeze(ctx, &ledgerv1.FreezeRequest{
		IdempotencyKey: key, UserId: userID, AccountType: "SPOT", Asset: asset, Amount: amount.String(),
		EntryType: "ORDER_FREEZE", Reference: orderID,
	})
	return err
}

// Unfreeze releases an order's unused funds (ORDER_UNFREEZE).
func (c *Client) Unfreeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, orderID string) error {
	_, err := c.c.Unfreeze(ctx, &ledgerv1.UnfreezeRequest{
		IdempotencyKey: key, UserId: userID, AccountType: "SPOT", Asset: asset, Amount: amount.String(),
		EntryType: "ORDER_UNFREEZE", Reference: orderID,
	})
	return err
}
