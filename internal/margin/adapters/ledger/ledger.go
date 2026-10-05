// Package ledger books margin operations through ledger-service and reads
// the margin accounts' balances and debts there.
package ledger

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

// Client implements ports.Ledger.
type Client struct{ c ledgerv1.LedgerServiceClient }

// New wraps a LedgerService client.
func New(c ledgerv1.LedgerServiceClient) *Client { return &Client{c: c} }

// Post books a margin posting (PostMargin).
func (c *Client) Post(ctx context.Context, p ports.Posting) ([]string, error) {
	req := &ledgerv1.PostMarginRequest{
		IdempotencyKey: p.IdemKey, UserId: p.UserID, AccountType: string(p.Account.Type), Scope: p.Account.Symbol, Reference: p.Reference,
	}
	for _, m := range p.Moves {
		move := &ledgerv1.MarginMove{Type: string(m.Type), Asset: m.Asset, Amount: m.Amount.String()}
		if m.Interest.IsPositive() {
			move.Interest = m.Interest.String()
		}
		req.Moves = append(req.Moves, move)
	}
	resp, err := c.c.PostMargin(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(resp.GetJournalIds()) != len(p.Moves) {
		return nil, fmt.Errorf("margin posting %s: %d journals for %d moves", p.IdemKey, len(resp.GetJournalIds()), len(p.Moves))
	}
	return resp.GetJournalIds(), nil
}

// Accrue books an hour's interest of an asset (AccrueMarginInterest).
func (c *Client) Accrue(ctx context.Context, key, asset, reference string, lines []ports.Accrual) (string, error) {
	req := &ledgerv1.AccrueMarginInterestRequest{IdempotencyKey: key, Asset: asset, Reference: reference}
	for _, l := range lines {
		req.Lines = append(req.Lines, &ledgerv1.MarginInterestLine{
			UserId: l.UserID, AccountType: string(l.Account.Type), Scope: l.Account.Symbol, Amount: l.Amount.String(),
		})
	}
	resp, err := c.c.AccrueMarginInterest(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

func parse(s, what string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("bad %s %q", what, s)
	}
	return d, nil
}

// Holdings returns what the user's margin accounts hold and owe
// (GetMarginBalances).
func (c *Client) Holdings(ctx context.Context, userID string) (map[domain.Account][]domain.Holding, error) {
	resp, err := c.c.GetMarginBalances(ctx, &ledgerv1.GetMarginBalancesRequest{UserId: userID})
	if err != nil {
		return nil, err
	}
	out := map[domain.Account][]domain.Holding{}
	for _, b := range resp.GetBalances() {
		var v [4]decimal.Decimal
		for i, f := range []struct{ s, what string }{
			{b.GetAvailable(), "available"}, {b.GetFrozen(), "frozen"}, {b.GetBorrowed(), "borrowed"}, {b.GetInterest(), "interest"},
		} {
			if v[i], err = parse(f.s, f.what); err != nil {
				return nil, fmt.Errorf("margin balance of %s: %w", b.GetAsset(), err)
			}
		}
		a := domain.Account{Type: domain.AccountType(b.GetAccountType()), Symbol: b.GetScope()}
		out[a] = append(out[a], domain.Holding{Asset: b.GetAsset(), Free: v[0], Locked: v[1], Borrowed: v[2], Interest: v[3]})
	}
	return out, nil
}

// Debts returns what every margin account owes (ListMarginDebts).
func (c *Client) Debts(ctx context.Context) ([]ports.Debt, error) {
	resp, err := c.c.ListMarginDebts(ctx, &ledgerv1.ListMarginDebtsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Debt, 0, len(resp.GetDebts()))
	for _, d := range resp.GetDebts() {
		principal, err := parse(d.GetBorrowed(), "borrowed")
		if err != nil {
			return nil, err
		}
		interest, err := parse(d.GetInterest(), "interest")
		if err != nil {
			return nil, err
		}
		out = append(out, ports.Debt{
			UserID: d.GetUserId(), Account: domain.Account{Type: domain.AccountType(d.GetAccountType()), Symbol: d.GetScope()},
			Asset: d.GetAsset(), Principal: principal, Interest: interest,
		})
	}
	return out, nil
}
