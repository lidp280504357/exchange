package grpcapi

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// PostMargin books one operation of margin-service on a margin account.
func (s *Server) PostMargin(ctx context.Context, req *ledgerv1.PostMarginRequest) (*ledgerv1.PostMarginResponse, error) {
	r := domain.MarginRequest{
		IdemKey: req.GetIdempotencyKey(), Reference: req.GetReference(),
		Account: domain.MarginRef{UserID: req.GetUserId(), AccountType: req.GetAccountType(), Scope: req.GetScope()},
	}
	for i, m := range req.GetMoves() {
		a, err := amount(m.GetAmount())
		if err != nil {
			return nil, apperr.Invalid(fmt.Sprintf("move %d: amount must be a decimal string", i+1))
		}
		move := domain.MarginMove{Type: m.GetType(), Asset: m.GetAsset(), Amount: a, Interest: decimal.Zero}
		if m.GetInterest() != "" {
			if move.Interest, err = amount(m.GetInterest()); err != nil {
				return nil, apperr.Invalid(fmt.Sprintf("move %d: interest must be a decimal string", i+1))
			}
		}
		r.Moves = append(r.Moves, move)
	}
	res, err := s.svc.PostMargin(ctx, r)
	if err != nil {
		return nil, err
	}
	return &ledgerv1.PostMarginResponse{JournalIds: res.Journals, Replayed: res.Replayed}, nil
}

// AccrueMarginInterest books an hour's interest of an asset.
func (s *Server) AccrueMarginInterest(ctx context.Context, req *ledgerv1.AccrueMarginInterestRequest) (*ledgerv1.AccrueMarginInterestResponse, error) {
	r := domain.InterestRequest{IdemKey: req.GetIdempotencyKey(), Asset: req.GetAsset(), Reference: req.GetReference()}
	for i, l := range req.GetLines() {
		a, err := amount(l.GetAmount())
		if err != nil {
			return nil, apperr.Invalid(fmt.Sprintf("line %d: amount must be a decimal string", i+1))
		}
		r.Lines = append(r.Lines, domain.InterestLine{
			Account: domain.MarginRef{UserID: l.GetUserId(), AccountType: l.GetAccountType(), Scope: l.GetScope()}, Amount: a,
		})
	}
	res, err := s.svc.AccrueInterest(ctx, r)
	if err != nil {
		return nil, err
	}
	return &ledgerv1.AccrueMarginInterestResponse{Posting: posting(res)}, nil
}

// marginRow is a margin account's three rows of an asset together.
type marginRow struct {
	user, accountType, scope, asset string
}

// accountOf names the margin account a row belongs to and whether the row
// is its assets, debt or interest.
func accountOf(a domain.Account) (marginRow, string) {
	t, part := a.Key.Type, "assets"
	for _, base := range []string{domain.AccountMarginCross, domain.AccountMarginIsolated} {
		switch t {
		case base + "_DEBT":
			t, part = base, "debt"
		case base + "_INTEREST":
			t, part = base, "interest"
		}
	}
	return marginRow{user: a.Key.OwnerID, accountType: t, scope: a.Key.Scope, asset: a.Key.Asset}, part
}

// GetMarginBalances returns what a user's margin accounts hold and owe.
func (s *Server) GetMarginBalances(ctx context.Context, req *ledgerv1.GetMarginBalancesRequest) (*ledgerv1.GetMarginBalancesResponse, error) {
	list, err := s.svc.MarginBalances(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	var order []marginRow
	rows := map[marginRow]*ledgerv1.MarginBalance{}
	for _, a := range list {
		k, part := accountOf(a)
		b, ok := rows[k]
		if !ok {
			b = &ledgerv1.MarginBalance{
				AccountType: k.accountType, Scope: k.scope, Asset: k.asset, Available: "0", Frozen: "0", Borrowed: "0", Interest: "0",
			}
			rows[k] = b
			order = append(order, k)
		}
		switch part {
		case "assets":
			b.Available, b.Frozen = a.Available.String(), a.Frozen.String()
		case "debt":
			b.Borrowed = a.Available.Neg().String()
		case "interest":
			b.Interest = a.Available.Neg().String()
		}
	}
	resp := &ledgerv1.GetMarginBalancesResponse{}
	for _, k := range order {
		resp.Balances = append(resp.Balances, rows[k])
	}
	return resp, nil
}

// RepayReleased repays what an order on a margin account borrowed, up to
// up_to, as the order ends (B160).
func (s *Server) RepayReleased(ctx context.Context, req *ledgerv1.RepayReleasedRequest) (*ledgerv1.RepayReleasedResponse, error) {
	upTo, err := amount(req.GetUpTo())
	if err != nil {
		return nil, err
	}
	filled := decimal.Zero
	if req.GetFilledQuantity() != "" {
		if filled, err = amount(req.GetFilledQuantity()); err != nil {
			return nil, err
		}
	}
	res, repaid, err := s.svc.RepayReleased(ctx, req.GetOrderId(), req.GetUserId(), req.GetAccountType(), req.GetScope(), req.GetAsset(), upTo, filled)
	if err != nil {
		return nil, err
	}
	resp := &ledgerv1.RepayReleasedResponse{Repaid: repaid.String()}
	if res.JournalID != "" {
		resp.Posting = posting(res)
	}
	return resp, nil
}

// ListMarginDebts returns every margin account's debt of each asset it
// owes.
func (s *Server) ListMarginDebts(ctx context.Context, _ *ledgerv1.ListMarginDebtsRequest) (*ledgerv1.ListMarginDebtsResponse, error) {
	list, err := s.svc.MarginDebts(ctx)
	if err != nil {
		return nil, err
	}
	var order []marginRow
	debts := map[marginRow]*ledgerv1.MarginDebt{}
	for _, a := range list {
		k, part := accountOf(a)
		d, ok := debts[k]
		if !ok {
			d = &ledgerv1.MarginDebt{UserId: k.user, AccountType: k.accountType, Scope: k.scope, Asset: k.asset, Borrowed: "0", Interest: "0"}
			debts[k] = d
			order = append(order, k)
		}
		if part == "debt" {
			d.Borrowed = a.Available.Neg().String()
		} else {
			d.Interest = a.Available.Neg().String()
		}
	}
	resp := &ledgerv1.ListMarginDebtsResponse{}
	for _, k := range order {
		resp.Debts = append(resp.Debts, debts[k])
	}
	return resp, nil
}
