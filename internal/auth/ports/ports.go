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
	// Once runs fn in a transaction unless consumer already handled the
	// event, and reports whether it ran (the inbox, requirements §8.2).
	Once(ctx context.Context, consumer, eventID string, fn func(Repos) error) (bool, error)
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Challenges() ChallengeRepo
	Tickets() TicketRepo
	Identities() IdentityRepo
	Credentials() CredentialRepo
	Sessions() SessionRepo
	LoginChallenges() LoginChallengeRepo
	StepUps() StepUpRepo
	Devices() DeviceRepo
	History() HistoryRepo
	RebindRequests() RebindRepo
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
	// ByUser returns the user's identities, EMAIL before PHONE.
	ByUser(ctx context.Context, userID string) ([]domain.Identity, error)
	// Create binds a new identity; a taken one yields domain.ErrIdentityTaken.
	Create(ctx context.Context, id domain.Identity, verifiedAt time.Time) error
	// UpdateValue replaces the value of an identity (rebinding).
	UpdateValue(ctx context.Context, id, value string, verifiedAt time.Time) error
}

// CredentialRepo stores password records.
type CredentialRepo interface {
	Create(ctx context.Context, userID, hash string, now time.Time) error
	// Get returns the credential, or nil.
	Get(ctx context.Context, userID string) (*domain.Credential, error)
	// RecordLogin sets the last successful login and clears failures.
	RecordLogin(ctx context.Context, userID string, now time.Time) error
	// RecordFailure counts a failure and, when lockedUntil is set, locks.
	RecordFailure(ctx context.Context, userID string, lockedUntil time.Time) error
	SetPassword(ctx context.Context, userID, hash string, now time.Time) error
}

// SessionRepo stores device sessions and their refresh tokens.
type SessionRepo interface {
	Create(ctx context.Context, s domain.Session) error
	// Get returns the session, or nil.
	Get(ctx context.Context, id string) (*domain.Session, error)
	// Active lists the user's live sessions, most recently seen first.
	Active(ctx context.Context, userID string) ([]domain.Session, error)
	Touch(ctx context.Context, id, ip, userAgent string, now time.Time) error
	// Revoke ends a live session and reports whether it was live.
	Revoke(ctx context.Context, id, reason string, now time.Time) (bool, error)
	CreateRefresh(ctx context.Context, t domain.RefreshToken) error
	// RefreshForUpdate locks a refresh token by hash; nil when unknown.
	RefreshForUpdate(ctx context.Context, hash []byte) (*domain.RefreshToken, error)
	MarkRotated(ctx context.Context, hash []byte, now time.Time) error
}

// LoginChallengeRepo stores pending login challenges.
type LoginChallengeRepo interface {
	Create(ctx context.Context, lc domain.LoginChallenge) error
	// Get returns the challenge, or nil.
	Get(ctx context.Context, id string) (*domain.LoginChallenge, error)
	// Consume marks a live challenge used and reports whether it was live.
	Consume(ctx context.Context, id string, now time.Time) (bool, error)
}

// StepUpRepo stores step-up tokens.
type StepUpRepo interface {
	Create(ctx context.Context, s domain.StepUp) error
	// Consume redeems a live token of userID; nil when there is none.
	Consume(ctx context.Context, hash []byte, userID string, now time.Time) (*domain.StepUp, error)
}

// DeviceRepo remembers the devices a user logged in from.
type DeviceRepo interface {
	// Seen records a login from device and reports whether it is new.
	Seen(ctx context.Context, userID, deviceID string, now time.Time) (bool, error)
}

// HistoryRepo stores the login history.
type HistoryRepo interface {
	Add(ctx context.Context, e domain.LoginEvent) error
	// List returns up to limit entries older than beforeID (0: newest).
	List(ctx context.Context, userID string, beforeID int64, limit int) ([]domain.LoginEvent, error)
}

// RebindRepo stores rebind requests awaiting review.
type RebindRepo interface {
	Create(ctx context.Context, r domain.RebindRequest) error
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

// OTPMetrics counts OTP traffic for the operators (§12.2: OTP 发送量).
type OTPMetrics interface {
	// Requested counts an otp/request by parsed scene and channel and its
	// outcome: queued, decoy, captcha_rejected, rate_limited,
	// channel_unavailable, invalid or error.
	Requested(scene, channel, outcome string)
	// Verified counts an otp/verify outcome: verified, invalid, expired,
	// attempts_exceeded or error.
	Verified(outcome string)
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}

// NewUser is the profile user-service creates at registration.
type NewUser struct {
	ID           string
	Region       string
	Language     string
	Timezone     string
	TermsVersion string
	RiskVersion  string
}

// UserInfo is what login needs from user-service.
type UserInfo struct {
	ID       string
	Status   string
	Region   string
	Language string
}

// Users talks to user-service.
type Users interface {
	// Create is idempotent on the user ID.
	Create(ctx context.Context, u NewUser) error
	Get(ctx context.Context, userID string) (UserInfo, error)
}

// Revocations tells the gateway that a session ended, so its access tokens
// stop working before they expire, or that a user's tokens carry an
// outdated scope and must be refreshed.
type Revocations interface {
	Revoke(ctx context.Context, sessionIDs ...string) error
	// MarkStale makes the user's tokens issued up to upTo stale.
	MarkStale(ctx context.Context, userID string, upTo time.Time) error
}

// LoginGuard counts password failures per identifier, known or not, so
// that the captcha and lock rules reveal nothing about accounts.
type LoginGuard interface {
	Failures(ctx context.Context, key string) (int, time.Duration, error)
	Fail(ctx context.Context, key string) (int, error)
	Clear(ctx context.Context, key string) error
}

// Tokens issues access tokens.
type Tokens interface {
	Issue(userID, sessionID, scope string, now time.Time) (string, time.Time, error)
}
