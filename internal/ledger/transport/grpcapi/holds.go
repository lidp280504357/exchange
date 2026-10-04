package grpcapi

import (
	"context"
	"time"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/ledger/domain"
)

func holdProto(h domain.Hold) *ledgerv1.Hold {
	out := &ledgerv1.Hold{
		HoldId: h.ID, UserId: h.UserID, AccountType: h.AccountType, Asset: h.Asset, Amount: h.Amount.String(), Reason: h.Reason,
		Actor: h.Actor, JournalId: h.JournalID, CreatedAt: h.CreatedAt.UTC().Format(time.RFC3339Nano), ReleasedBy: h.ReleasedBy,
		ReleaseReason: h.ReleaseReason, ReleaseJournalId: h.ReleaseJournalID,
	}
	if !h.ReleasedAt.IsZero() {
		out.ReleasedAt = h.ReleasedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

// PlaceHold freezes part of a user's SPOT balance for an administrator.
func (s *Server) PlaceHold(ctx context.Context, req *ledgerv1.PlaceHoldRequest) (*ledgerv1.PlaceHoldResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	h, err := s.svc.PlaceHold(ctx, req.GetHoldId(), req.GetUserId(), req.GetAsset(), a, req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.PlaceHoldResponse{Hold: holdProto(h)}, nil
}

// ReleaseHold releases a hold.
func (s *Server) ReleaseHold(ctx context.Context, req *ledgerv1.ReleaseHoldRequest) (*ledgerv1.ReleaseHoldResponse, error) {
	h, err := s.svc.ReleaseHold(ctx, req.GetHoldId(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.ReleaseHoldResponse{Hold: holdProto(h)}, nil
}

// ReleaseUnclaimed books an unclaimed deposit to its user.
func (s *Server) ReleaseUnclaimed(ctx context.Context, req *ledgerv1.ReleaseUnclaimedRequest) (*ledgerv1.ReleaseUnclaimedResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.ReleaseUnclaimed(ctx, req.GetDepositId(), req.GetUserId(), req.GetAsset(), a, req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.ReleaseUnclaimedResponse{Posting: posting(res)}, nil
}

// CreditUnclaimed books a deposit to an address no user has to
// UNCLAIMED_DEPOSIT.
func (s *Server) CreditUnclaimed(ctx context.Context, req *ledgerv1.CreditUnclaimedRequest) (*ledgerv1.CreditUnclaimedResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.CreditUnclaimed(ctx, req.GetDepositId(), req.GetAsset(), a, req.GetNetwork(), req.GetTxHash(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.CreditUnclaimedResponse{Posting: posting(res)}, nil
}

// GetUnclaimedRelease reports the journal that released an unclaimed
// deposit, if any, and the user it paid.
func (s *Server) GetUnclaimedRelease(ctx context.Context, req *ledgerv1.GetUnclaimedReleaseRequest) (*ledgerv1.GetUnclaimedReleaseResponse, error) {
	journal, user, err := s.svc.UnclaimedRelease(ctx, req.GetDepositId())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.GetUnclaimedReleaseResponse{JournalId: journal, UserId: user}, nil
}

// ListHolds lists a user's holds.
func (s *Server) ListHolds(ctx context.Context, req *ledgerv1.ListHoldsRequest) (*ledgerv1.ListHoldsResponse, error) {
	list, err := s.svc.Holds(ctx, req.GetUserId(), req.GetActiveOnly())
	if err != nil {
		return nil, err
	}
	out := &ledgerv1.ListHoldsResponse{Holds: make([]*ledgerv1.Hold, 0, len(list))}
	for _, h := range list {
		out.Holds = append(out.Holds, holdProto(h))
	}
	return out, nil
}
