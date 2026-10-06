// Package grpcapi serves derivatives-service's gRPC API to other services.
package grpcapi

import (
	"context"

	"github.com/google/uuid"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Server implements derivativesv1.DerivativesServiceServer.
type Server struct {
	derivativesv1.UnimplementedDerivativesServiceServer
	svc *application.Service
}

// NewServer returns the gRPC API over svc.
func NewServer(svc *application.Service) *Server { return &Server{svc: svc} }

// GetUnrealizedPnL returns the unrealized result of the user's cross
// positions at fresh mark prices.
func (s *Server) GetUnrealizedPnL(ctx context.Context, req *derivativesv1.GetUnrealizedPnLRequest) (*derivativesv1.GetUnrealizedPnLResponse, error) {
	if _, err := uuid.Parse(req.GetUserId()); err != nil {
		return nil, apperr.Invalid("user_id must be a UUID")
	}
	pnl, err := s.svc.CrossUnrealizedPnL(ctx, req.GetUserId(), req.GetAsset())
	if err != nil {
		return nil, err
	}
	return &derivativesv1.GetUnrealizedPnLResponse{CrossUnrealizedPnl: pnl.String()}, nil
}
