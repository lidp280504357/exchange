// Package grpcapi serves auth-service's gRPC API to other services.
package grpcapi

import (
	"context"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	"github.com/lidp280504357/exchange/internal/auth/application"
	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Server implements authv1.AuthServiceServer.
type Server struct {
	authv1.UnimplementedAuthServiceServer
	accounts *application.AccountService
}

// NewServer returns the gRPC API over accounts.
func NewServer(accounts *application.AccountService) *Server { return &Server{accounts: accounts} }

// GetContacts returns a user's verified identities.
func (s *Server) GetContacts(ctx context.Context, req *authv1.GetContactsRequest) (*authv1.GetContactsResponse, error) {
	if req.GetUserId() == "" {
		return nil, apperr.Invalid("user_id is required")
	}
	ids, err := s.accounts.Contacts(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	resp := &authv1.GetContactsResponse{}
	for _, id := range ids {
		ch := domain.ChannelEmail
		if id.Kind == domain.ChannelSMS.Kind() {
			ch = domain.ChannelSMS
		}
		resp.Contacts = append(resp.Contacts, &authv1.Contact{Channel: string(ch), Value: id.Value})
	}
	return resp, nil
}

// ConsumeStepUp redeems a step-up token for another service.
func (s *Server) ConsumeStepUp(ctx context.Context, req *authv1.ConsumeStepUpRequest) (*authv1.ConsumeStepUpResponse, error) {
	if req.GetUserId() == "" {
		return nil, apperr.Invalid("user_id is required")
	}
	sid, err := s.accounts.ConsumeStepUp(ctx, req.GetUserId(), req.GetToken())
	if err != nil {
		return nil, err
	}
	return &authv1.ConsumeStepUpResponse{SessionId: sid}, nil
}
