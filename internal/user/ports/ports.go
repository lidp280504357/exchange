// Package ports declares what user-service's application layer needs.
package ports

import (
	"context"

	"github.com/lidp280504357/exchange/internal/user/domain"
)

// Users stores profiles.
type Users interface {
	// Create inserts the profile and its consents unless the user exists,
	// and returns the stored profile either way.
	Create(ctx context.Context, u domain.User, consents []domain.Consent) (domain.User, error)
	// Get returns the profile or domain.ErrUserNotFound.
	Get(ctx context.Context, id string) (domain.User, error)
}
