// Package grpcapi serves ledger-service's gRPC API.
package grpcapi

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/ledger/application"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
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

func amounts(ss ...string) ([]decimal.Decimal, error) {
	out := make([]decimal.Decimal, len(ss))
	for i, s := range ss {
		d, err := amount(s)
		if err != nil {
			return nil, err
		}
		out[i] = d
	}
	return out, nil
}

// SettleWithdrawal books a broadcast withdrawal.
func (s *Server) SettleWithdrawal(ctx context.Context, req *ledgerv1.SettleWithdrawalRequest) (*ledgerv1.SettleWithdrawalResponse, error) {
	a, err := amounts(req.GetAmount(), req.GetFee())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.SettleWithdrawal(ctx, req.GetIdempotencyKey(), req.GetUserId(), req.GetAsset(), a[0], a[1], req.GetReference())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.SettleWithdrawalResponse{Posting: posting(res)}, nil
}

// TransferInternal completes a withdrawal to another user in the ledger.
func (s *Server) TransferInternal(ctx context.Context, req *ledgerv1.TransferInternalRequest) (*ledgerv1.TransferInternalResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.TransferInternal(ctx, req.GetIdempotencyKey(), req.GetFromUserId(), req.GetToUserId(), req.GetAsset(), a, req.GetReference())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.TransferInternalResponse{Posting: posting(res)}, nil
}

// BookChainFee books gas the platform paid.
func (s *Server) BookChainFee(ctx context.Context, req *ledgerv1.BookChainFeeRequest) (*ledgerv1.BookChainFeeResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.BookChainFee(ctx, req.GetIdempotencyKey(), req.GetAsset(), a, req.GetReference())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.BookChainFeeResponse{Posting: posting(res)}, nil
}

// FundSystemAccount books a platform funding transfer.
func (s *Server) FundSystemAccount(ctx context.Context, req *ledgerv1.FundSystemAccountRequest) (*ledgerv1.FundSystemAccountResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.FundSystemAccount(ctx, req.GetIdempotencyKey(), req.GetAccountType(), req.GetAsset(), a, req.GetReference())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.FundSystemAccountResponse{Posting: posting(res)}, nil
}

// GetSystemBalances lists the system accounts of an asset.
func (s *Server) GetSystemBalances(ctx context.Context, req *ledgerv1.GetSystemBalancesRequest) (*ledgerv1.GetSystemBalancesResponse, error) {
	list, err := s.svc.SystemBalances(ctx, req.GetAsset())
	if err != nil {
		return nil, err
	}
	resp := &ledgerv1.GetSystemBalancesResponse{}
	for _, a := range list {
		resp.Balances = append(resp.Balances, &ledgerv1.Balance{
			AccountType: a.Key.Type, Asset: a.Key.Asset, Available: a.Available.String(), Frozen: a.Frozen.String(),
		})
	}
	return resp, nil
}

// Adjust credits or debits a user's SPOT account against ADJUSTMENT.
func (s *Server) Adjust(ctx context.Context, req *ledgerv1.AdjustRequest) (*ledgerv1.AdjustResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.AdjustApproved(ctx, req.GetIdempotencyKey(), req.GetUserId(), req.GetAsset(), a, req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.AdjustResponse{Posting: posting(res)}, nil
}

// SettleFutures books a settlement step of a user's contract trading.
func (s *Server) SettleFutures(ctx context.Context, req *ledgerv1.SettleFuturesRequest) (*ledgerv1.SettleFuturesResponse, error) {
	r := domain.FuturesRequest{
		IdemKey: req.GetIdempotencyKey(), UserID: req.GetUserId(), Asset: req.GetAsset(), Reference: req.GetReference(),
	}
	for i, m := range req.GetMoves() {
		a, err := amount(m.GetAmount())
		if err != nil {
			return nil, apperr.Invalid(fmt.Sprintf("move %d: amount must be a decimal string", i+1))
		}
		move := domain.FuturesMove{
			Type: m.GetType(), Amount: a, Kind: m.GetBalanceKind(), Partial: m.GetPartial(), EntryType: m.GetEntryType(),
		}
		if m.GetLimit() != "" {
			l, err := amount(m.GetLimit())
			if err != nil {
				return nil, apperr.Invalid(fmt.Sprintf("move %d: limit must be a decimal string", i+1))
			}
			move.Limit = &l
		}
		r.Moves = append(r.Moves, move)
	}
	res, err := s.svc.SettleFutures(ctx, r)
	if err != nil {
		return nil, err
	}
	out := &ledgerv1.SettleFuturesResponse{Replayed: res.Replayed}
	for _, o := range res.Outcomes {
		out.Outcomes = append(out.Outcomes, &ledgerv1.FuturesOutcome{
			JournalId: o.JournalID, UserAmount: o.User.String(), InsuranceAmount: o.Insurance.String(), WaivedAmount: o.Waived.String(),
		})
	}
	return out, nil
}

// FundInsurance adds simulated funds to the insurance fund.
func (s *Server) FundInsurance(ctx context.Context, req *ledgerv1.FundInsuranceRequest) (*ledgerv1.FundInsuranceResponse, error) {
	a, err := amount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	res, err := s.svc.FundInsurance(ctx, req.GetIdempotencyKey(), req.GetAsset(), a, req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &ledgerv1.FundInsuranceResponse{Posting: posting(res)}, nil
}

// GetReconciliation returns the latest run of each invariant check and the
// recent runs with mismatches.
func (s *Server) GetReconciliation(ctx context.Context, req *ledgerv1.GetReconciliationRequest) (*ledgerv1.GetReconciliationResponse, error) {
	latest, failing, err := s.svc.Reconciliation(ctx, int(req.GetFailures()))
	if err != nil {
		return nil, err
	}
	conv := func(runs []domain.ReconciliationRun) []*ledgerv1.ReconciliationRun {
		out := make([]*ledgerv1.ReconciliationRun, 0, len(runs))
		for _, r := range runs {
			out = append(out, &ledgerv1.ReconciliationRun{
				Check: r.Check, StartedAt: r.StartedAt.UTC().Format(time.RFC3339Nano), Mismatches: int32(min(r.Mismatches, math.MaxInt32)), //nolint:gosec // capped
				Details: string(r.Details),
			})
		}
		return out
	}
	return &ledgerv1.GetReconciliationResponse{Latest: conv(latest), Failures: conv(failing)}, nil
}
