// Package ledger moves contract margin and settles contract trading
// through ledger-service, on the users' FUTURES accounts.
package ledger

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
)

// Client implements ports.Ledger.
type Client struct{ c ledgerv1.LedgerServiceClient }

// New wraps a LedgerService client.
func New(c ledgerv1.LedgerServiceClient) *Client { return &Client{c: c} }

// Freeze reserves an order's margin and fee (ORDER_FREEZE on FUTURES).
func (c *Client) Freeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) error {
	_, err := c.c.Freeze(ctx, &ledgerv1.FreezeRequest{
		IdempotencyKey: key, UserId: userID, AccountType: "FUTURES", Asset: asset, Amount: amount.String(),
		EntryType: "ORDER_FREEZE", Reference: reference,
	})
	return err
}

// Unfreeze releases a finished order's unused reservation.
func (c *Client) Unfreeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) error {
	_, err := c.c.Unfreeze(ctx, &ledgerv1.UnfreezeRequest{
		IdempotencyKey: key, UserId: userID, AccountType: "FUTURES", Asset: asset, Amount: amount.String(),
		EntryType: "ORDER_UNFREEZE", Reference: reference,
	})
	return err
}

// Settle books a settlement step (SettleFutures).
func (c *Client) Settle(ctx context.Context, r ports.SettleRequest) ([]domain.Outcome, error) {
	req := &ledgerv1.SettleFuturesRequest{IdempotencyKey: r.IdemKey, UserId: r.UserID, Asset: r.Asset, Reference: r.Reference}
	for _, m := range r.Moves {
		move := &ledgerv1.FuturesMove{Type: m.Type, Amount: m.Amount.String(), Partial: m.Partial, EntryType: m.EntryType}
		if m.Frozen {
			move.BalanceKind = "FROZEN"
		}
		if m.Limit != nil {
			move.Limit = m.Limit.String()
		}
		req.Moves = append(req.Moves, move)
	}
	resp, err := c.c.SettleFutures(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Outcome, 0, len(resp.GetOutcomes()))
	for _, o := range resp.GetOutcomes() {
		var v [3]decimal.Decimal
		for i, s := range []string{o.GetUserAmount(), o.GetInsuranceAmount(), o.GetWaivedAmount()} {
			d, err := decimal.NewFromString(s)
			if err != nil {
				return nil, fmt.Errorf("settlement %s: bad outcome amount %q", r.IdemKey, s)
			}
			v[i] = d
		}
		out = append(out, domain.Outcome{User: v[0], Insurance: v[1], Waived: v[2]})
	}
	if len(out) != len(r.Moves) {
		return nil, fmt.Errorf("settlement %s: %d outcomes for %d moves", r.IdemKey, len(out), len(r.Moves))
	}
	return out, nil
}

// Balance returns the user's FUTURES balance in asset.
func (c *Client) Balance(ctx context.Context, userID, asset string) (ports.Balance, error) {
	resp, err := c.c.GetBalances(ctx, &ledgerv1.GetBalancesRequest{UserId: userID, AccountType: "FUTURES"})
	if err != nil {
		return ports.Balance{}, err
	}
	out := ports.Balance{Available: decimal.Zero, Frozen: decimal.Zero}
	for _, b := range resp.GetBalances() {
		if b.GetAsset() != asset {
			continue
		}
		a, err1 := decimal.NewFromString(b.GetAvailable())
		f, err2 := decimal.NewFromString(b.GetFrozen())
		if err1 != nil || err2 != nil {
			return ports.Balance{}, fmt.Errorf("bad balance of %s", asset)
		}
		out = ports.Balance{Available: a, Frozen: f}
	}
	return out, nil
}

// PnLClearing returns the PNL_CLEARING balance in asset.
func (c *Client) PnLClearing(ctx context.Context, asset string) (decimal.Decimal, error) {
	resp, err := c.c.GetSystemBalances(ctx, &ledgerv1.GetSystemBalancesRequest{Asset: asset})
	if err != nil {
		return decimal.Zero, err
	}
	for _, b := range resp.GetBalances() {
		if b.GetAccountType() == "PNL_CLEARING" {
			return decimal.NewFromString(b.GetAvailable())
		}
	}
	return decimal.Zero, nil
}
