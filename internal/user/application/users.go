// Package application holds user-service's use cases.
package application

import (
	"context"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/user/domain"
	"github.com/lidp280504357/exchange/internal/user/ports"
)

// Service manages profiles.
type Service struct {
	Users ports.Users
}

// CreateInput is a CreateUser call from auth-service.
type CreateInput struct {
	UserID       string
	Region       string
	Language     string
	Timezone     string
	TermsVersion string
	RiskVersion  string
}

// Create creates the profile of a newly registered user; repeating the
// call returns the existing profile.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.User, error) {
	u, err := domain.NewUser(in.UserID, in.Region, in.Language, in.Timezone)
	if err != nil {
		return domain.User{}, err
	}
	if in.TermsVersion == "" || in.RiskVersion == "" {
		return domain.User{}, apperr.Invalid("the accepted terms and risk disclosure versions are required")
	}
	return s.Users.Create(ctx, u, []domain.Consent{
		{Document: domain.DocumentTerms, Version: in.TermsVersion},
		{Document: domain.DocumentRiskDisclosure, Version: in.RiskVersion},
	})
}

// Get returns a profile.
func (s *Service) Get(ctx context.Context, id string) (domain.User, error) {
	return s.Users.Get(ctx, id)
}
