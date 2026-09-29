// Package domain holds the admin console's model (requirements §5.12):
// administrators who sign in with a password and an authenticator code,
// roles that grant permissions, sessions, and requests that a second
// administrator must approve (manual ledger adjustments).
package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Roles.
const (
	RoleAdmin    = "ADMIN"    // everything
	RoleOperator = "OPERATOR" // users, orders, instruments, flags
	RoleFinance  = "FINANCE"  // withdrawals, ledger adjustments
	RoleAuditor  = "AUDITOR"  // read-only, audit logs
)

// Permissions.
const (
	PermUsersRead       = "users.read"
	PermUsersStatus     = "users.status"
	PermOrdersCancel    = "orders.cancel"
	PermInstrumentsRead = "instruments.read"
	PermInstrumentsEdit = "instruments.write"
	PermFlagsRead       = "flags.read"
	PermFlagsEdit       = "flags.write"
	PermWithdrawalsRead = "withdrawals.read"
	PermWithdrawalsEdit = "withdrawals.review"
	PermAdjustRequest   = "ledger.adjust.request"
	PermAdjustApprove   = "ledger.adjust.approve"
	PermAuditRead       = "audit.read"
)

var reads = []string{PermUsersRead, PermInstrumentsRead, PermFlagsRead, PermWithdrawalsRead, PermAuditRead}

var roles = map[string][]string{
	RoleAdmin: append(slices.Clone(reads), PermUsersStatus, PermOrdersCancel, PermInstrumentsEdit, PermFlagsEdit,
		PermWithdrawalsEdit, PermAdjustRequest, PermAdjustApprove),
	RoleOperator: append(slices.Clone(reads), PermUsersStatus, PermOrdersCancel, PermInstrumentsEdit, PermFlagsEdit),
	RoleFinance:  append(slices.Clone(reads), PermWithdrawalsEdit, PermAdjustRequest, PermAdjustApprove),
	RoleAuditor:  slices.Clone(reads),
}

// ValidRole reports whether role exists.
func ValidRole(role string) bool { _, ok := roles[role]; return ok }

// Permissions returns a role's permissions.
func Permissions(role string) []string { return slices.Clone(roles[role]) }

// Allows reports whether role grants perm.
func Allows(role, perm string) bool { return slices.Contains(roles[role], perm) }

// Admin statuses.
const (
	StatusActive   = "ACTIVE"
	StatusDisabled = "DISABLED"
)

// Lockout: five wrong logins lock the account for 15 minutes.
const (
	MaxFailures  = 5
	LockDuration = 15 * time.Minute
	// SessionTTL bounds a session; SessionIdle ends one left alone.
	SessionTTL  = 8 * time.Hour
	SessionIdle = time.Hour
)

// Errors (appendix C: ADMIN_).
var (
	ErrLoginFailed  = apperr.New(apperr.KindUnauthenticated, "ADMIN_LOGIN_FAILED", "wrong email, password or authenticator code")
	ErrLocked       = apperr.New(apperr.KindForbidden, "ADMIN_LOCKED", "too many failures; try again later")
	ErrUnauthorized = apperr.New(apperr.KindUnauthenticated, "ADMIN_UNAUTHORIZED", "sign in to the admin console")
	ErrForbidden    = apperr.New(apperr.KindForbidden, "ADMIN_FORBIDDEN", "your role does not allow this")
	ErrSelfApproval = apperr.New(apperr.KindForbidden, "ADMIN_SELF_APPROVAL", "another administrator must approve your own request")
	ErrNotPending   = apperr.New(apperr.KindConflict, "ADMIN_APPROVAL_DECIDED", "the request was already decided")
)

// Admin is an administrator.
type Admin struct {
	ID             string
	Email          string
	Name           string
	Role           string
	PasswordHash   string
	TOTPSealed     []byte
	TOTPLastStep   int64
	Status         string
	FailedAttempts int
	LockedUntil    time.Time
	LastLoginAt    time.Time
	CreatedAt      time.Time
}

// NewAdmin validates a new administrator; the caller sets the hash and
// the sealed secret.
func NewAdmin(id, email, name, role string, now time.Time) (Admin, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		return Admin{}, apperr.Invalid("a valid email address is required")
	}
	if !ValidRole(role) {
		return Admin{}, apperr.Invalid(fmt.Sprintf("unknown role %q (ADMIN, OPERATOR, FINANCE, AUDITOR)", role))
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Admin{}, apperr.Invalid("a name of at most 100 characters is required")
	}
	return Admin{ID: id, Email: email, Name: name, Role: role, Status: StatusActive, CreatedAt: now}, nil
}

// Locked reports whether the account refuses logins at now.
func (a *Admin) Locked(now time.Time) bool { return now.Before(a.LockedUntil) }

// Failed counts a wrong login, locking the account at MaxFailures.
func (a *Admin) Failed(now time.Time) {
	a.FailedAttempts++
	if a.FailedAttempts >= MaxFailures {
		a.FailedAttempts, a.LockedUntil = 0, now.Add(LockDuration)
	}
}

// Succeeded records a login with the authenticator step it used.
func (a *Admin) Succeeded(step int64, now time.Time) {
	a.FailedAttempts, a.LockedUntil, a.TOTPLastStep, a.LastLoginAt = 0, time.Time{}, step, now
}

// Session is a signed-in administrator's session; only the token's hash
// is stored.
type Session struct {
	TokenHash  []byte
	AdminID    string
	IP         string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// NewToken returns a session token and its hash.
func NewToken() (string, []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

// HashToken is how session tokens are stored.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Live reports whether the session still holds at now.
func (s *Session) Live(now time.Time) bool {
	return now.Before(s.ExpiresAt) && now.Sub(s.LastSeenAt) < SessionIdle
}

// Approval kinds and statuses.
const (
	KindLedgerAdjustment = "LEDGER_ADJUSTMENT"

	ApprovalPending  = "PENDING"
	ApprovalExecuted = "EXECUTED"
	ApprovalRejected = "REJECTED"
	ApprovalFailed   = "FAILED"
)

// Approval is a request a second administrator must approve (§5.12:
// manual ledger adjustments need two people).
type Approval struct {
	ID          string
	Kind        string
	Payload     map[string]string
	Reason      string
	Status      string
	RequestedBy string // admin ID
	DecidedBy   string
	Result      string
	CreatedAt   time.Time
	DecidedAt   time.Time
}

// Decide checks that decider may decide the request.
func (a *Approval) Decide(decider string) error {
	switch {
	case a.Status != ApprovalPending:
		return ErrNotPending
	case a.RequestedBy == decider:
		return ErrSelfApproval
	}
	return nil
}
