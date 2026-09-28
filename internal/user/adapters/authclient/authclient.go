// Package authclient redeems step-up tokens at auth-service.
package authclient

import (
	"context"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
)

// StepUps implements ports.StepUps over AuthService.ConsumeStepUp.
type StepUps struct{ c authv1.AuthServiceClient }

// New wraps an AuthService client.
func New(c authv1.AuthServiceClient) *StepUps { return &StepUps{c: c} }

// Consume redeems token for userID; a missing or used token fails with
// AUTH_STEP_UP_REQUIRED, which crosses the gRPC boundary unchanged.
func (s *StepUps) Consume(ctx context.Context, userID, token string) error {
	_, err := s.c.ConsumeStepUp(ctx, &authv1.ConsumeStepUpRequest{UserId: userID, Token: token})
	return err
}
