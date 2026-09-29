// Package grpcapi serves user-service's gRPC API.
package grpcapi

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/user/application"
	"github.com/lidp280504357/exchange/internal/user/domain"
)

// Server implements userv1.UserServiceServer.
type Server struct {
	userv1.UnimplementedUserServiceServer
	svc *application.Service
}

// NewServer returns the gRPC API over svc.
func NewServer(svc *application.Service) *Server { return &Server{svc: svc} }

func toProto(u domain.User) *userv1.User {
	return &userv1.User{
		Id: u.ID, Status: u.Status, Region: u.Region, Language: u.Language, Timezone: u.Timezone,
		KycLevel:         int32(u.KYCLevel), //nolint:gosec // small level number
		AntiPhishingCode: u.AntiPhishingCode, CreatedAt: timestamppb.New(u.CreatedAt),
	}
}

// CreateUser creates a profile idempotently.
func (s *Server) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	u, err := s.svc.Create(ctx, application.CreateInput{
		UserID: req.GetUserId(), Region: req.GetRegion(), Language: req.GetLanguage(), Timezone: req.GetTimezone(),
		TermsVersion: req.GetTermsVersion(), RiskVersion: req.GetRiskDisclosureVersion(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.CreateUserResponse{User: toProto(u)}, nil
}

// GetUser returns a profile.
func (s *Server) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	u, err := s.svc.Get(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &userv1.GetUserResponse{User: toProto(u)}, nil
}

// CheckEligibility decides whether a user may use a feature now.
func (s *Server) CheckEligibility(ctx context.Context, req *userv1.CheckEligibilityRequest) (*userv1.CheckEligibilityResponse, error) {
	allowed, reason, err := s.svc.CheckEligibility(ctx, req.GetUserId(), req.GetFeature(), req.GetAsset(), req.GetSymbol())
	if err != nil {
		return nil, err
	}
	return &userv1.CheckEligibilityResponse{Allowed: allowed, ReasonCode: reason}, nil
}

// ChangeStatus moves an account to another status for an operator.
func (s *Server) ChangeStatus(ctx context.Context, req *userv1.ChangeStatusRequest) (*userv1.ChangeStatusResponse, error) {
	c, err := s.svc.ChangeStatus(ctx, req.GetUserId(), req.GetToStatus(), req.GetReasonCode(), req.GetActor(), req.GetNote())
	if err != nil {
		return nil, err
	}
	return &userv1.ChangeStatusResponse{FromStatus: c.From, ToStatus: c.To}, nil
}
