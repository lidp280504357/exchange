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
	"regexp"
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
	// PermUsersNotes writes notes on an account and sets its tags.
	PermUsersNotes = "users.notes"
	// PermUsersSecurity ends an account's sessions, removes its
	// authenticator, gives it a temporary password and decides its
	// identity rebind requests.
	PermUsersSecurity = "users.security"
	// PermUsersContacts shows an account's email and phone unmasked
	// (audited each time).
	PermUsersContacts = "users.contacts"
	// PermLedgerHold freezes part of a user's SPOT balance and releases it
	// (risk control).
	PermLedgerHold = "ledger.hold"
	// PermDepositsReview decides the deposits that need a person (credits
	// an unclaimed one, dismisses one) and requests backfills of those
	// whose callback was lost.
	PermDepositsReview = "deposits.review"
	// PermAdminsManage manages the administrators: creates them, changes
	// their roles, disables and enables them, resets their passwords and
	// authenticators, ends their sessions (ADMIN only).
	PermAdminsManage = "admins.manage"
	// PermInstrumentsTrading changes the trading parameters: a pair's or
	// contract's status, fee rates, risk ladders (and so leverage) and
	// reference symbols; and approves, rejects or cancels such changes
	// (ADMIN only, design 2026-10-02 §2 item 6).
	PermInstrumentsTrading = "instruments.trading"
	// PermContentEdit writes, publishes and archives the announcements and
	// help articles (design 2026-10-02 §4.5).
	PermContentEdit = "content.write"
	// PermNoticesSend sends in-app messages to users: one, a tag's, or all.
	PermNoticesSend = "notices.send"
	// PermSimControl runs the simulated market of the platform coin (ASTRA
	// design §6): its price events and settings, within one operator's
	// share; beyond it a second administrator with it approves.
	PermSimControl = "sim.control"
	// PermWithdrawalsResume lifts an asset's withdrawal suspension (funds
	// missing on the custody checks): its approved withdrawals go out
	// (ADMIN only, C5.5 ⑯).
	PermWithdrawalsResume = "withdrawals.resume"
	// PermAuditExport exports the audit trail as a spreadsheet, with its
	// email addresses and IP addresses (ADMIN and AUDITOR, C5.5 ⑪).
	PermAuditExport = "audit.export"
)

var reads = []string{
	PermUsersRead, PermInstrumentsRead, PermFlagsRead, PermWithdrawalsRead, PermAuditRead, PermReportsRead, PermDerivativesRead,
}

var roles = map[string][]string{
	RoleAdmin: append(slices.Clone(reads), PermUsersStatus, PermOrdersCancel, PermInstrumentsEdit, PermFlagsEdit,
		PermWithdrawalsEdit, PermAdjustRequest, PermAdjustApprove, PermDerivativesEdit, PermSettingsEdit, PermUsersNotes,
		PermUsersSecurity, PermUsersContacts, PermLedgerHold, PermDepositsReview, PermAdminsManage, PermInstrumentsTrading,
		PermContentEdit, PermNoticesSend, PermSimControl, PermWithdrawalsResume, PermAuditExport),
	RoleOperator: append(slices.Clone(reads), PermUsersStatus, PermOrdersCancel, PermInstrumentsEdit, PermFlagsEdit, PermDerivativesEdit,
		PermUsersNotes, PermUsersSecurity, PermUsersContacts, PermLedgerHold, PermContentEdit, PermNoticesSend, PermSimControl),
	RoleFinance: append(slices.Clone(reads), PermWithdrawalsEdit, PermAdjustRequest, PermAdjustApprove, PermUsersNotes, PermUsersContacts,
		PermLedgerHold, PermDepositsReview),
	RoleAuditor: append(slices.Clone(reads), PermAuditExport),
}

// ValidRole reports whether role exists.
func ValidRole(role string) bool { _, ok := roles[role]; return ok }

// Roles lists the roles, the strongest first.
func Roles() []string { return []string{RoleAdmin, RoleOperator, RoleFinance, RoleAuditor} }

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
	// ErrSelf refuses changing one's own account from the console
	// (another administrator, or exchangectl, does it).
	ErrSelf = apperr.New(apperr.KindForbidden, "ADMIN_SELF", "another administrator must change your own account")
	// ErrLastAdmin refuses leaving the console without an active ADMIN.
	ErrLastAdmin = apperr.New(apperr.KindConflict, "ADMIN_LAST_ADMIN", "at least one active ADMIN must remain")
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
	// SetupKind, with SetupHash (of the one-time token), SetupTOTPSealed
	// (the authenticator it binds) and SetupExpiresAt, is a setup waiting
	// for the administrator (setup.go).
	SetupKind       string
	SetupHash       []byte
	SetupTOTPSealed []byte
	SetupExpiresAt  time.Time
	// MustChangePassword holds an administrator whose password was
	// generated for them (exchangectl admin create) until they change it.
	MustChangePassword bool
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

// NoOwner is wallet-service's owner of a deposit of nobody (B7a): the
// nil UUID, never a user's.
const NoOwner = "00000000-0000-0000-0000-000000000000"

// Approval kinds and statuses.
const (
	KindLedgerAdjustment = "LEDGER_ADJUSTMENT"
	// KindInsuranceFund adds simulated funds to the contracts' insurance
	// fund (ledger FundInsurance).
	KindInsuranceFund = "INSURANCE_FUND"
	// KindDepositBackfill books a custodian deposit whose callback was
	// lost (wallet-service, design 2026-10-02 §4.3).
	KindDepositBackfill = "DEPOSIT_BACKFILL"
	// KindSimEvent and KindSimParams are a simulated market's price event
	// and settings beyond one operator's share (ASTRA design §6.2): the
	// second administrator's approval goes to market-sim as approved_by.
	KindSimEvent  = "SIM_EVENT"
	KindSimParams = "SIM_PARAMS"
	// KindSimMint is more of an asset for the simulated market's bots: one
	// manual adjustment per bot, a fund operation like any other (ASTRA
	// design §4: no transfers between bots; the pool grows this way).
	KindSimMint = "SIM_MINT"
	// KindDepositAssign credits a deposit of nobody (a custodian's deposit
	// to an address no user has, B7a) to the user an administrator names:
	// wallet-service sets its owner and releases it from UNCLAIMED_DEPOSIT
	// (C5.5 ㉑).
	KindDepositAssign = "DEPOSIT_ASSIGN"

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
	EscalationSimShare  = "SIM_SHARE"       // a price move beyond one operator's share (market-sim)
	// EscalationNotHolder: a deposit of nobody credited to a user other
	// than its address's holder, now or before the address was retired,
	// whatever its worth (C5.5 ㉑, the coordinator's 10-04 decision).
	EscalationNotHolder = "NOT_ADDRESS_HOLDER"
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
	// AttemptedAt is when an attempt to carry it out began (zero before
	// any). Pending with it set, the attempt did not finish (the ledger did
	// not answer, a mint was refused part way) and may have booked: it is
	// finished, never rejected, and Result says how the last attempt ended.
	AttemptedAt time.Time
	// The administrators' emails, for display (read only).
	RequestedByEmail string
	DecidedByEmail   string
}

// ErrDepositAssignOpen refuses a second request to credit a deposit of
// nobody while one waits or once one was carried out (review ㉕).
var ErrDepositAssignOpen = apperr.New(apperr.KindConflict, "ADMIN_DEPOSIT_ASSIGN_OPEN",
	"another request to credit this deposit waits or was carried out: decide or withdraw it first")

// ErrAttempted refuses rejecting an operation that may have booked.
var ErrAttempted = apperr.New(apperr.KindConflict, "ADMIN_APPROVAL_ATTEMPTED",
	"an attempt to carry it out did not finish and may have booked it: finish it instead")

// Decide checks that decider may decide the request: another
// administrator, except that the requester may finish a single-person
// operation whose outcome was unknown and withdraw (reject) their own
// request.
func (a *Approval) Decide(decider string, approve bool) error {
	switch {
	case a.Status != ApprovalPending:
		return ErrNotPending
	case !approve && !a.AttemptedAt.IsZero():
		return ErrAttempted
	case a.RequestedBy == decider && approve && a.Mode != ModeSingle:
		return ErrSelfApproval
	}
	return nil
}

// Note is an administrator's note on an account (append-only).
type Note struct {
	ID         string
	UserID     string
	AdminID    string
	AdminEmail string // read only
	Body       string
	CreatedAt  time.Time
}

// MaxNote bounds a note's length in characters.
const MaxNote = 2000

// NewNote validates a note.
func NewNote(id, userID, adminID, body string, now time.Time) (Note, error) {
	body = strings.TrimSpace(body)
	if n := len([]rune(body)); n == 0 || n > MaxNote {
		return Note{}, apperr.Invalid(fmt.Sprintf("a note has 1 to %d characters", MaxNote))
	}
	return Note{ID: id, UserID: userID, AdminID: adminID, Body: body, CreatedAt: now}, nil
}

// MaxTags bounds an account's tags.
const MaxTags = 10

var tagPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)

// Tags normalizes an account's tags: upper case codes (VIP, SUSPICIOUS,
// TEST, ...), each once, sorted.
func Tags(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.ToUpper(strings.TrimSpace(t))
		if !tagPattern.MatchString(t) {
			return nil, apperr.Invalid(fmt.Sprintf("tag %q: letters, digits and _ (up to 32), starting with a letter", t))
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if len(out) > MaxTags {
		return nil, apperr.Invalid(fmt.Sprintf("at most %d tags", MaxTags))
	}
	slices.Sort(out)
	return out, nil
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
	// ChangeDelay is how long a confirmed change of trading parameters
	// waits before it takes effect (design 2026-10-02 §2 item 6).
	ChangeDelay time.Duration
	UpdatedBy   string
	UpdatedAt   time.Time
}

// The bounds of Settings.ChangeDelay. DefaultChangeDelayFloor is the
// least an ADMIN may set it to unless admin-service is configured with
// another (C5.5 ⑩: one ADMIN alone must not cut the wait to a minute);
// MinChangeDelay is the least any configuration allows.
const (
	MinChangeDelay          = time.Minute
	MaxChangeDelay          = 24 * time.Hour
	DefaultChangeDelay      = 5 * time.Minute
	DefaultChangeDelayFloor = 10 * time.Minute
)

// DefaultSettings are the limits until an administrator changes them.
func DefaultSettings() Settings {
	return Settings{
		SingleMax: decimal.NewFromInt(100_000), DailyMax: decimal.NewFromInt(500_000), WithdrawalMax: decimal.NewFromInt(100_000),
		ChangeDelay: DefaultChangeDelay,
	}
}

// maxSettingsFactor bounds each limit at ten times its default: one PUT
// must not lift the guard altogether (the C1 review).
const maxSettingsFactor = 10

// Validate checks the limits: positive, at most ten times their defaults,
// and a day's total no smaller than one operation; and the delay of
// trading parameters' changes.
func (s Settings) Validate() error {
	def, factor := DefaultSettings(), decimal.NewFromInt(maxSettingsFactor)
	for _, l := range []struct{ v, def decimal.Decimal }{{s.SingleMax, def.SingleMax}, {s.DailyMax, def.DailyMax}, {s.WithdrawalMax, def.WithdrawalMax}} {
		if !l.v.IsPositive() {
			return apperr.Invalid("the limits must be positive")
		}
		if l.v.GreaterThan(l.def.Mul(factor)) {
			return apperr.Invalid("a limit is at most ten times its default (single 1,000,000, 24 hours 5,000,000, withdrawal 1,000,000 USDT)")
		}
	}
	if s.DailyMax.LessThan(s.SingleMax) {
		return apperr.Invalid("the 24-hour limit must be at least the single-operation limit")
	}
	if s.ChangeDelay < MinChangeDelay || s.ChangeDelay > MaxChangeDelay || s.ChangeDelay%time.Second != 0 {
		return apperr.Invalid("change_delay_seconds must be 60 to 86400")
	}
	return nil
}
