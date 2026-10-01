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

	"github.com/shopspring/decimal"

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
	PermReportsRead     = "reports.read"
	// Perpetual contracts: the contracts' state, the insurance fund and
	// the positions near liquidation; lifting a contract's reduce-only.
	PermDerivativesRead = "derivatives.read"
	PermDerivativesEdit = "derivatives.write"
	// PermSettingsEdit changes the console's settings: two-person approval
	// and the single-person limits (and the flags of the console itself).
	PermSettingsEdit = "settings.write"
)

var reads = []string{
	PermUsersRead, PermInstrumentsRead, PermFlagsRead, PermWithdrawalsRead, PermAuditRead, PermReportsRead, PermDerivativesRead,
}

var roles = map[string][]string{
	RoleAdmin: append(slices.Clone(reads), PermUsersStatus, PermOrdersCancel, PermInstrumentsEdit, PermFlagsEdit,
		PermWithdrawalsEdit, PermAdjustRequest, PermAdjustApprove, PermDerivativesEdit, PermSettingsEdit),
	RoleOperator: append(slices.Clone(reads), PermUsersStatus, PermOrdersCancel, PermInstrumentsEdit, PermFlagsEdit, PermDerivativesEdit),
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
	// KindInsuranceFund adds simulated funds to the contracts' insurance
	// fund (ledger FundInsurance).
	KindInsuranceFund = "INSURANCE_FUND"

	ApprovalPending  = "PENDING"
	ApprovalExecuted = "EXECUTED"
	ApprovalRejected = "REJECTED"
	ApprovalFailed   = "FAILED"
)

// Approval modes: a second administrator decides, or (with the flag
// admin.two_person_approval off) the requester carries it out alone
// within the single-person limits.
const (
	ModeTwoPerson = "TWO_PERSON"
	ModeSingle    = "SINGLE"
)

// Why a request waits for a second administrator.
const (
	EscalationRequested = "REQUESTED"       // asked for one
	EscalationTwoPerson = "TWO_PERSON_MODE" // the flag admin.two_person_approval is on
	EscalationSingleMax = "SINGLE_LIMIT"    // worth more than one operation may be
	EscalationDailyMax  = "DAILY_LIMIT"     // over the requester's 24-hour total
	EscalationNoPrice   = "NO_PRICE"        // its worth in USDT is unknown
)

// Approval is a fund operation (§5.12: manual ledger adjustments and
// insurance fund contributions): a request a second administrator must
// approve, or one carried out alone in single-person mode.
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
	Mode        string
	// ValueUSDT is the amount's worth when requested (nil: no price).
	ValueUSDT *decimal.Decimal
	// Escalation says why a two-person request waits ("" in single mode).
	Escalation string
	JournalID  string
	// The administrators' emails, for display (read only).
	RequestedByEmail string
	DecidedByEmail   string
}

// Decide checks that decider may decide the request: another
// administrator, except that the requester may finish a single-person
// operation whose outcome was unknown and withdraw (reject) their own
// request.
func (a *Approval) Decide(decider string, approve bool) error {
	switch {
	case a.Status != ApprovalPending:
		return ErrNotPending
	case a.RequestedBy == decider && approve && a.Mode != ModeSingle:
		return ErrSelfApproval
	}
	return nil
}

// Settings are the console's single-person limits (design 2026-10-02 §2):
// with two-person approval off, one administrator may carry out a fund
// operation worth at most SingleMax, at most DailyMax within 24 hours,
// and approve alone a withdrawal worth at most WithdrawalMax; above these
// a second administrator is needed anyway. Amounts are in USDT.
type Settings struct {
	SingleMax     decimal.Decimal
	DailyMax      decimal.Decimal
	WithdrawalMax decimal.Decimal
	UpdatedBy     string
	UpdatedAt     time.Time
}

// DefaultSettings are the limits until an administrator changes them.
func DefaultSettings() Settings {
	return Settings{SingleMax: decimal.NewFromInt(100_000), DailyMax: decimal.NewFromInt(500_000), WithdrawalMax: decimal.NewFromInt(100_000)}
}

// Validate checks the limits: positive, and a day's total no smaller than
// one operation.
func (s Settings) Validate() error {
	for _, v := range []decimal.Decimal{s.SingleMax, s.DailyMax, s.WithdrawalMax} {
		if !v.IsPositive() {
			return apperr.Invalid("the limits must be positive")
		}
	}
	if s.DailyMax.LessThan(s.SingleMax) {
		return apperr.Invalid("the 24-hour limit must be at least the single-operation limit")
	}
	return nil
}
