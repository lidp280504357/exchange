// Package ports declares what user-service's application layer needs.
package ports

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/user/domain"
)

// Store is the unit of work over the users schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Users() UserRepo
	// Emit queues an event on topic, keyed by aggregateID.
	Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error
}

// UserRepo stores profiles, consents and status history.
type UserRepo interface {
	// Create inserts the profile and its consents unless the user exists,
	// and reports whether it did.
	Create(ctx context.Context, u domain.User, consents []domain.Consent) (bool, error)
	// Get returns the profile or domain.ErrUserNotFound.
	Get(ctx context.Context, id string) (domain.User, error)
	// GetForUpdate is Get with a row lock.
	GetForUpdate(ctx context.Context, id string) (domain.User, error)
	// Update stores the mutable fields and bumps the version.
	Update(ctx context.Context, u domain.User) (domain.User, error)
	AddStatusChange(ctx context.Context, c domain.StatusChange) error
	// StatusHistory lists the user's status changes, newest first.
	StatusHistory(ctx context.Context, userID string, limit int) ([]domain.StatusChange, error)
}

// Flags reads the local copy of the feature flags.
type Flags interface {
	Get(key string) (flags.Flag, bool)
}

// StepUps redeems step-up tokens issued by auth-service.
type StepUps interface {
	Consume(ctx context.Context, userID, token string) error
}
