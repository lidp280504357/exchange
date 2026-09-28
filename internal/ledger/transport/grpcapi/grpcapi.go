// Package grpcapi serves ledger-service's gRPC API.
package grpcapi

import (
	"context"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/ledger/application"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Server implements ledgerv1.LedgerServiceServer.
type Server struct {
	ledgerv1.UnimplementedLedgerServiceServer
	svc *application.Service
}

// NewServer returns the gRPC API over svc.
func NewServer(svc *application.Service) *Server { return &Server{svc: svc} }

func amount(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, apperr.Invalid("amount must be a decimal string")
	}
	return d, nil
}

func posting(r application.Result) *ledgerv1.Posting {
	return &ledgerv1.Posting{JournalId: r.JournalID, Replayed: r.Replayed}
}

// Freeze moves available funds to frozen.
func (s *Server) Freeze(ctx context.Context, req *ledgerv1.FreezeRequest) (*ledgerv1.FreezeResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Freeze(ctx, req.GetIdempotencyKey(), req.GetEntryType(), req.GetUserId(), req.GetAccountType(), req.GetAsset(), a, req.GetReference())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.FreezeResponse{Posting: posting(res)}, nil
}

// Unfreeze moves frozen funds back to available.
func (s *Server) Unfreeze(ctx context.Context, req *ledgerv1.UnfreezeRequest) (*ledgerv1.UnfreezeResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Unfreeze(ctx, req.GetIdempotencyKey(), req.GetEntryType(), req.GetUserId(), req.GetAccountType(), req.GetAsset(), a, req.GetReference())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.UnfreezeResponse{Posting: posting(res)}, nil
}

// Transfer moves funds between a user's SPOT and FUTURES accounts.
func (s *Server) Transfer(ctx context.Context, req *ledgerv1.TransferRequest) (*ledgerv1.TransferResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	t, err := s.svc.Transfer(ctx, application.TransferInput{
		UserID: req.GetUserId(), IdemKey: req.GetIdempotencyKey(), Asset: req.GetAsset(), Amount: a,
		From: req.GetFromAccountType(), To: req.GetToAccountType(),
	})
	if err != nil {
		return nil, err
	}
	return &ledgerv1.TransferResponse{Posting: &ledgerv1.Posting{JournalId: t.JournalID}, TransferId: t.ID}, nil
}

// GetBalances lists a user's balances.
func (s *Server) GetBalances(ctx context.Context, req *ledgerv1.GetBalancesRequest) (*ledgerv1.GetBalancesResponse, error) {
	list, err := s.svc.Balances(ctx, req.GetUserId(), req.GetAccountType())
	if err != nil {
		return nil, err
	}
	resp := &ledgerv1.GetBalancesResponse{}
	for _, a := range list {
		resp.Balances = append(resp.Balances, &ledgerv1.Balance{
			AccountType: a.Key.Type, Asset: a.Key.Asset, Available: a.Available.String(), Frozen: a.Frozen.String(),
		})
	}
	return resp, nil
}
