// Package ports declares what auth-service's application layer needs from
// the outside world.
package ports

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
)

// Store is the unit of work over auth-service's schema.
type Store interface {
	// Tx runs fn in one transaction: the repositories passed to fn write
	// through it, and the events fn emits commit with it.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Challenges() ChallengeRepo
	Tickets() TicketRepo
	Identities() IdentityRepo
	// Emit queues an auth.events event, keyed by aggregateID.
	Emit(ctx context.Context, msg proto.Message, aggregateType, aggregateID string) error
}

// ChallengeRepo stores OTP challenges.
type ChallengeRepo interface {
	Create(ctx context.Context, c *domain.Challenge) error
	// GetForUpdate locks the challenge; it returns (nil, nil) when unknown.
	GetForUpdate(ctx context.Context, id string) (*domain.Challenge, error)
	Update(ctx context.Context, c *domain.Challenge) error
}

// TicketRepo stores OTP tickets.
type TicketRepo interface {
	Create(ctx context.Context, t domain.Ticket) error
	// Consume redeems an unexpired, unconsumed ticket for scene and device
	// and returns its challenge ID, or "" when there is no such ticket.
	Consume(ctx context.Context, hash []byte, scene domain.Scene, deviceID string, now time.Time) (string, error)
}

// IdentityRepo reads and writes bound identities.
type IdentityRepo interface {
	// Find returns the identity with kind and value, or nil.
	Find(ctx context.Context, kind, value string) (*domain.Identity, error)
	// ByUser returns the user's identities.
	ByUser(ctx context.Context, userID string) ([]domain.Identity, error)
}

// OTPDelivery is a code on its way to the user.
type OTPDelivery struct {
	ChallengeID string
	Channel     domain.Channel
	Target      string
	Code        string
	Scene       domain.Scene
	Language    string
	UserID      string
	TTL         time.Duration
}

// Notifier hands codes to notification-service.
type Notifier interface {
	SendOTP(ctx context.Context, d OTPDelivery) error
}

// Captcha verifies human-verification tokens.
type Captcha interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// Limiter enforces quotas.
type Limiter interface {
	Allow(ctx context.Context, checks ...ratelimit.Check) (ratelimit.Result, error)
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
