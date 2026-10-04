package domain

import (
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Session and login rules (requirements §5.2, §6.4–6.6).
const (
	RefreshTTL = 30 * 24 * time.Hour
	// RefreshRaceWindow tolerates two tabs refreshing with the same cookie:
	// a token rotated this recently is rejected without revoking the
	// session, and the client retries with the cookie the other tab got.
	RefreshRaceWindow = 5 * time.Second
	MaxSessions       = 10
	// LoginSilence is how long without a successful login triggers the OTP
	// challenge after a correct password (configurable, ADR-0009).
	LoginSilence = 7 * 24 * time.Hour
	// CaptchaAfterFailures and LockAfterFailures count password failures
	// per identifier; the lock lasts LockDuration.
	CaptchaAfterFailures = 3
	LockAfterFailures    = 10
	LockDuration         = 15 * time.Minute
)

// Client types: WEB keeps the refresh token in an HttpOnly cookie, APP
// (Tauri, mobile) sends it in the body (§6.6).
const (
	ClientWeb = "WEB"
	ClientApp = "APP"
)

// Account statuses reported by user-service (§5.4).
const (
	StatusActive     = "ACTIVE"
	StatusRiskReview = "RISK_REVIEW"
	StatusFrozen     = "FROZEN"
	StatusClosed     = "CLOSED"
)

// Revocation reasons.
const (
	RevokeLogout         = "LOGOUT"
	RevokeLogoutAll      = "LOGOUT_ALL"
	RevokeUser           = "USER"
	RevokeReplay         = "REPLAY"
	RevokeLimit          = "LIMIT"
	RevokePasswordReset  = "PASSWORD_RESET"
	RevokePasswordChange = "PASSWORD_CHANGE"
	RevokeClosed         = "ACCOUNT_CLOSED"
	// RevokeAdmin: an administrator ended it from the admin console.
	RevokeAdmin = "ADMIN"
)

// Errors of the login and session flows (appendix C).
var (
	ErrAccountLocked          = apperr.New(apperr.KindForbidden, "AUTH_ACCOUNT_LOCKED", "too many failed logins; try again later")
	ErrLoginChallengeRequired = apperr.New(apperr.KindForbidden, "AUTH_LOGIN_CHALLENGE_REQUIRED", "confirm this login with a verification code")
	ErrLoginChallengeInvalid  = apperr.New(apperr.KindInvalid, "AUTH_LOGIN_CHALLENGE_INVALID", "the login challenge is invalid or expired")
	ErrSessionRevoked         = apperr.New(apperr.KindUnauthenticated, "AUTH_SESSION_REVOKED", "the session has ended; sign in again")
	ErrTokenExpired           = apperr.New(apperr.KindUnauthenticated, "AUTH_TOKEN_EXPIRED", "the token has expired")
	ErrStepUpRequired         = apperr.New(apperr.KindForbidden, "AUTH_STEP_UP_REQUIRED", "confirm this action with a fresh verification")
	ErrIdentityTaken          = apperr.New(apperr.KindConflict, "AUTH_IDENTITY_TAKEN", "this email or phone number belongs to another account")
	ErrIdentityKindBound      = apperr.New(apperr.KindConflict, "AUTH_IDENTITY_KIND_BOUND", "an identity of this kind is already bound; rebind it instead")
	ErrTermsOutdated          = apperr.New(apperr.KindConflict, "AUTH_TERMS_OUTDATED", "the terms have changed; review them again")
	ErrUserClosed             = apperr.New(apperr.KindForbidden, "USER_CLOSED", "the account is closed")
)

// Credential is a user's password record.
type Credential struct {
	UserID            string
	PasswordHash      string
	FailedAttempts    int
	LockedUntil       time.Time
	LastLoginAt       time.Time
	PasswordChangedAt time.Time
	// TOTPChangedAt is when the authenticator app was last unbound or
	// reset by an administrator; zero if never.
	TOTPChangedAt time.Time
}

// Session is a device session.
type Session struct {
	ID         string
	UserID     string
	DeviceID   string
	ClientType string
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	RevokedAt  time.Time
}

// RefreshToken is one generation of a session's refresh token.
type RefreshToken struct {
	Hash       []byte
	SessionID  string
	Generation int
	ExpiresAt  time.Time
	RotatedAt  time.Time
}

// LoginChallenge is the pending second step of a login after 7 silent days.
type LoginChallenge struct {
	ID         string
	UserID     string
	DeviceID   string
	ExpiresAt  time.Time
	ConsumedAt time.Time
}

// StepUp is a proof of re-authentication for one sensitive action.
type StepUp struct {
	Hash      []byte
	UserID    string
	SessionID string
	// Channel is how the user proved it, so rebinding can demand the other
	// identity (§6.4).
	Channel   Channel
	ExpiresAt time.Time
}

// LoginEvent is one line of the login history.
type LoginEvent struct {
	ID           int64
	UserID       string
	Method       string
	Result       string
	IdentityMask string
	DeviceID     string
	UserAgent    string
	IP           string
	NewDevice    bool
	CreatedAt    time.Time
}

// RebindRequest waits for two-person review (§6.4): the user proved the
// new identity, an administrator decides.
type RebindRequest struct {
	ID        string
	UserID    string
	Kind      string
	NewValue  string
	Status    string
	CreatedAt time.Time
	DecidedAt time.Time
	DecidedBy string
	Reason    string
}

// Rebind request statuses.
const (
	RebindPending  = "PENDING_REVIEW"
	RebindApproved = "APPROVED"
	RebindRejected = "REJECTED"
)

// Device is a device a user signed in from.
type Device struct {
	DeviceID    string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// SecurityContext is what the risk rules of sensitive actions weigh
// (§11.6): identities, authenticator (and when the one bound now was
// activated), how new the device is and recent identity, password or
// authenticator changes.
type SecurityContext struct {
	Identities        int
	TOTPEnabled       bool
	TOTPActivatedAt   time.Time
	DeviceID          string
	DeviceFirstSeenAt time.Time
	IdentityChangedAt time.Time
	PasswordChangedAt time.Time
	TOTPChangedAt     time.Time
}
