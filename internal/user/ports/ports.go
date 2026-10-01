// Package ports declares what user-service's application layer needs.
package ports

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/user/domain"
)

// Store is the unit of work over the users schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Once is Tx unless consumer already handled the event; it reports
	// whether fn ran.
	Once(ctx context.Context, consumer, eventID string, fn func(Repos) error) (bool, error)
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Users() UserRepo
	Favorites() FavoriteRepo
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
	// Consents lists the document versions the user accepted, newest first.
	Consents(ctx context.Context, userID string) ([]domain.Consent, error)
	// List returns up to f.Limit accounts matching f, newest first, after
	// the account created at f.AfterTime with ID f.AfterID (the previous
	// page's last; zero for the newest).
	List(ctx context.Context, f UserFilter) ([]domain.User, error)
	// Stats counts every account, those created at or after since, and
	// those created on each of the last days (UTC, today included).
	Stats(ctx context.Context, since time.Time, days int) (UserStats, error)
}

// UserFilter selects accounts for the admin console.
type UserFilter struct {
	Status        string
	Region        string
	CreatedFrom   time.Time
	CreatedBefore time.Time
	AfterTime     time.Time
	AfterID       string
	Limit         int
}

// UserStats are the admin console's account counts.
type UserStats struct {
	Total        int64
	CreatedSince int64
	// Days maps a UTC day (YYYY-MM-DD) to its new accounts.
	Days map[string]int64
}

// FavoriteRepo stores each user's favorite markets.
type FavoriteRepo interface {
	// Get returns the user's list, empty with a zero time when never set.
	Get(ctx context.Context, userID string) ([]string, time.Time, error)
	// Set replaces the list and returns when.
	Set(ctx context.Context, userID string, symbols []string) (time.Time, error)
}

// Flags reads the local copy of the feature flags.
type Flags interface {
	Get(key string) (flags.Flag, bool)
}

// StepUps redeems step-up tokens issued by auth-service.
type StepUps interface {
	Consume(ctx context.Context, userID, token string) error
}
