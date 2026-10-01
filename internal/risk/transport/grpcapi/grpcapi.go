// Package grpcapi serves risk-service's read-only gRPC API for the admin
// console.
package grpcapi

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	riskv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/risk/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/risk/application"
)

// Server implements riskv1.RiskServiceServer.
type Server struct {
	riskv1.UnimplementedRiskServiceServer
	svc *application.Service
}

// NewServer returns the gRPC API over svc.
func NewServer(svc *application.Service) *Server { return &Server{svc: svc} }

// ListAssessments returns the newest stored assessments.
func (s *Server) ListAssessments(ctx context.Context, req *riskv1.ListAssessmentsRequest) (*riskv1.ListAssessmentsResponse, error) {
	if id := req.GetUserId(); id != "" {
		if _, err := uuid.Parse(id); err != nil {
			return nil, apperr.Invalid("user_id must be a UUID")
		}
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	list, err := s.svc.Assessments(ctx, req.GetUserId(), min(limit, 200))
	if err != nil {
		return nil, err
	}
	resp := &riskv1.ListAssessmentsResponse{Assessments: make([]*riskv1.Assessment, len(list))}
	for i, a := range list {
		hits := make([]*riskv1.RuleHit, len(a.Hits))
		for j, h := range a.Hits {
			hits[j] = &riskv1.RuleHit{Rule: h.Rule, Score: int32(h.Score), Detail: h.Detail} //nolint:gosec // scores are 0..100
		}
		resp.Assessments[i] = &riskv1.Assessment{
			Id: a.ID, UserId: a.UserID, SourceEventType: a.SourceEventType,
			Score: int32(a.Score), Action: a.Action.String(), Hits: hits, Enforced: a.Enforced, //nolint:gosec // 0..100
			CreatedAt: timestamppb.New(a.CreatedAt),
		}
	}
	return resp, nil
}
