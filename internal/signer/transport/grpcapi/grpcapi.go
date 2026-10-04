// Package grpcapi serves the signer's gRPC API.
package grpcapi

import (
	"context"
	"math/big"
	"strings"

	signerv1 "github.com/skill/exchange/api/gen/go/exchange/signer/v1"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/signer/application"
	"github.com/skill/exchange/internal/signer/domain"
)

// Server implements signerv1.SignerServiceServer.
type Server struct {
	signerv1.UnimplementedSignerServiceServer
	svc *application.Service
}

// NewServer returns the API over svc.
func NewServer(svc *application.Service) *Server { return &Server{svc: svc} }

// GetHotWallet returns the hot wallet's address.
func (s *Server) GetHotWallet(context.Context, *signerv1.GetHotWalletRequest) (*signerv1.GetHotWalletResponse, error) {
	addr, err := s.svc.HotWallet()
	if err != nil {
		return nil, err
	}
	return &signerv1.GetHotWalletResponse{Address: addr}, nil
}

func integer(name, s string) (*big.Int, error) {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 {
		return nil, apperr.Invalid(name + " must be a non-negative integer string")
	}
	return v, nil
}

// SignTransaction signs a transaction after the policy's checks.
func (s *Server) SignTransaction(ctx context.Context, req *signerv1.SignTransactionRequest) (*signerv1.SignTransactionResponse, error) {
	var nums [3]*big.Int
	for i, f := range []struct{ name, value string }{
		{"value", req.GetValue()}, {"max_fee_per_gas", req.GetMaxFeePerGas()}, {"max_priority_fee_per_gas", req.GetMaxPriorityFeePerGas()},
	} {
		v, err := integer(f.name, f.value)
		if err != nil {
			return nil, err
		}
		nums[i] = v
	}
	sig, err := s.svc.Sign(ctx, domain.Request{
		ID: req.GetRequestId(), Purpose: strings.TrimPrefix(req.GetPurpose().String(), "PURPOSE_"), Reference: req.GetReference(),
		ApprovedBy: req.GetApprovedBy(), ChainID: req.GetChainId(), Index: req.GetAddressIndex(), Nonce: req.GetNonce(), To: req.GetTo(),
		Value: nums[0], Data: req.GetData(), GasLimit: req.GetGasLimit(), MaxFee: nums[1], MaxTip: nums[2],
	})
	if err != nil {
		return nil, err
	}
	return &signerv1.SignTransactionResponse{RawTransaction: sig.Raw, TxHash: sig.TxHash, From: sig.From}, nil
}
