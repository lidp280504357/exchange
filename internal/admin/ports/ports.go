// Package ports declares what the admin console's application layer
// needs: its own storage and the services it acts on.
package ports

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/flags"
)

// Store is the unit of work over the admin schema.
type Store interface {
	Tx(ctx context.Context, fn func(Repos) error) error
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Admins() AdminRepo
	Sessions() SessionRepo
	Approvals() ApprovalRepo
	Settings() SettingsRepo
	Notes() NoteRepo
	Tags() TagRepo
	Changes() ChangeRepo
	Keys() IdempotencyRepo
	Access() AccessRepo
	// Audit queues an administrator's action on audit.events.
	Audit(ctx context.Context, msg proto.Message, actor string) error
}

// AccessRepo stores the console's access switches, one row (N1).
type AccessRepo interface {
	// Get returns them, or nil before they were first stored;
	// GetForUpdate locks them for a change.
	Get(ctx context.Context) (*domain.ConsoleAccess, error)
	GetForUpdate(ctx context.Context) (*domain.ConsoleAccess, error)
	// Init stores them unless they were stored already, and reports
	// whether it did.
	Init(ctx context.Context, a domain.ConsoleAccess) (bool, error)
	Put(ctx context.Context, a domain.ConsoleAccess) error
}

// ChangeRepo stores the changes of trading parameters (design 2026-10-02
// §2 item 6).
type ChangeRepo interface {
	Create(ctx context.Context, c domain.InstrumentChange) error
	// Get and GetForUpdate read a change with the emails of its
	// administrators; nil when unknown.
	Get(ctx context.Context, id string) (*domain.InstrumentChange, error)
	GetForUpdate(ctx context.Context, id string) (*domain.InstrumentChange, error)
	Update(ctx context.Context, c domain.InstrumentChange) error
	// List returns changes, newest first, of a status when set, after the
	// one created at afterTime with ID afterID (zero for the newest).
	List(ctx context.Context, status string, afterTime time.Time, afterID string, limit int) ([]domain.InstrumentChange, error)
	// Due locks the scheduled changes whose time has come, oldest first,
	// skipping those another transaction holds and those a round claimed
	// within domain.ClaimHold.
	Due(ctx context.Context, now time.Time, limit int) ([]domain.InstrumentChange, error)
	// ByConfirmation returns the change a preview's confirmation (its
	// hash) confirmed; nil when none.
	ByConfirmation(ctx context.Context, hash string) (*domain.InstrumentChange, error)
	// Open counts the changes waiting for approval or their time.
	Open(ctx context.Context) (int, error)
}

// NoteRepo stores administrators' notes on accounts.
type NoteRepo interface {
	Insert(ctx context.Context, n domain.Note) error
	// List returns an account's notes, newest first, with their authors'
	// emails, after the one created at afterTime with ID afterID (zero for
	// the newest).
	List(ctx context.Context, userID string, afterTime time.Time, afterID string, limit int) ([]domain.Note, error)
}

// TagRepo stores accounts' tags.
type TagRepo interface {
	// Of returns the tags of each account (absent: none).
	Of(ctx context.Context, userIDs []string) (map[string][]string, error)
	// Set replaces an account's tags.
	Set(ctx context.Context, userID string, tags []string, adminID string, now time.Time) error
	// Users returns the accounts with a tag, at most limit of them.
	Users(ctx context.Context, tag string, limit int) ([]string, error)
}

// SettingsRepo stores the console's settings (one row).
type SettingsRepo interface {
	// Get returns the settings, or nil while nobody changed them.
	Get(ctx context.Context) (*domain.Settings, error)
	Put(ctx context.Context, s domain.Settings) error
}

// AdminRepo stores administrators.
type AdminRepo interface {
	Insert(ctx context.Context, a domain.Admin) error
	Update(ctx context.Context, a domain.Admin) error
	// ByEmail returns the administrator with email, or nil.
	ByEmail(ctx context.Context, email string) (*domain.Admin, error)
	// ByEmailForUpdate is ByEmail with the row locked.
	ByEmailForUpdate(ctx context.Context, email string) (*domain.Admin, error)
	// Get returns an administrator, or nil.
	Get(ctx context.Context, id string) (*domain.Admin, error)
	// GetForUpdate is Get with the row locked.
	GetForUpdate(ctx context.Context, id string) (*domain.Admin, error)
	// BySetupForUpdate returns the administrator a setup token's hash
	// belongs to, the row locked, or nil.
	BySetupForUpdate(ctx context.Context, hash []byte) (*domain.Admin, error)
	List(ctx context.Context) ([]domain.Admin, error)
	// LockRoster serializes, until the transaction ends, the changes that
	// could leave no active ADMIN.
	LockRoster(ctx context.Context) error
}

// SessionRepo stores sessions by token hash.
type SessionRepo interface {
	Insert(ctx context.Context, s domain.Session) error
	// Get returns a session that is not revoked, or nil.
	Get(ctx context.Context, hash []byte) (*domain.Session, error)
	Touch(ctx context.Context, hash []byte, now time.Time) error
	Revoke(ctx context.Context, hash []byte, now time.Time) error
	// RevokeAll ends every session of an administrator; RevokeOthers
	// every one but keep (the one a change of their own came from).
	RevokeAll(ctx context.Context, adminID string, now time.Time) error
	RevokeOthers(ctx context.Context, adminID string, keep []byte, now time.Time) error
	// Live returns an administrator's sessions still live at now, the
	// latest first.
	Live(ctx context.Context, adminID string, now time.Time) ([]domain.Session, error)
}

// ApprovalRepo stores fund operations: two-person requests and
// single-person operations.
type ApprovalRepo interface {
	Insert(ctx context.Context, a domain.Approval) error
	Update(ctx context.Context, a domain.Approval) error
	GetForUpdate(ctx context.Context, id string) (*domain.Approval, error)
	// Get returns one with the administrators' emails, or nil.
	Get(ctx context.Context, id string) (*domain.Approval, error)
	// List returns up to limit requests in a status ("": all), newest
	// first, after the one created at afterTime with ID afterID (zero for
	// the newest), with the administrators' emails.
	List(ctx context.Context, status string, afterTime time.Time, afterID string, limit int) ([]domain.Approval, error)
	// SingleUsage sums the worth of an administrator's single-person
	// operations requested since then that were not refused (pending ones
	// count: their outcome may be booked).
	SingleUsage(ctx context.Context, adminID string, since time.Time) (decimal.Decimal, error)
	// CountPending counts the requests waiting for a decision.
	CountPending(ctx context.Context) (int, error)
	// LockRequests takes, until the transaction ends, the requests of a
	// kind by one administrator: a check of what waits and the insert that
	// follows it see each other's (review BH ①).
	LockRequests(ctx context.Context, kind, requestedBy string) error
	// PendingOf returns every pending request of a kind by one
	// administrator, oldest first.
	PendingOf(ctx context.Context, kind, requestedBy string) ([]domain.Approval, error)
	// PendingOfKind returns every pending request of a kind, whoever asked,
	// oldest first.
	PendingOfKind(ctx context.Context, kind string) ([]domain.Approval, error)
	// MarkAttempted records that an attempt to carry a pending one out
	// began (it keeps the first time); a note says how the last attempt
	// ended when it did not finish ("" keeps the note).
	MarkAttempted(ctx context.Context, id string, at time.Time, note string) error
}

// IdempotencyRepo keeps the administrators' Idempotency-Keys of requests
// that move money (the platform's idempotency_keys table).
type IdempotencyRepo interface {
	// Claim records the ID of what a request makes under scope and key
	// with the request's fingerprint, unless the key is known: then it
	// returns the first request's fingerprint and ID (claimed false).
	Claim(ctx context.Context, scope, key string, hash []byte, ref string, now time.Time) (storedHash []byte, storedRef string, claimed bool,
		err error)
	// Purge deletes the keys claimed before cutoff.
	Purge(ctx context.Context, cutoff time.Time) (int64, error)
}

// User is an account as the console shows it.
type User struct {
	ID        string
	Status    string
	Region    string
	Language  string
	Timezone  string
	KYCLevel  int32
	CreatedAt time.Time
	// Username and the uploaded avatar's addresses (design 2026-10-07,
	// avatars and usernames; empty for the default avatar).
	Username       string
	AvatarURL      string
	AvatarThumbURL string
	// Kind is HUMAN, BOT, TEST or SYSTEM (L0): what the console shows and
	// filters by.
	Kind string
	// PurgedAt is when a test account was cleared out and closed for good
	// (L4); nil for the others.
	PurgedAt *time.Time
	// Tags are the console's tags on it (filled by the console).
	Tags []string
}

// Balance is one of a user's balances.
type Balance struct {
	AccountType string
	Asset       string
	Available   string
	Frozen      string
}

// UserQuery selects accounts; empty fields match everything.
type UserQuery struct {
	Status string
	Region string
	// Q keeps the accounts whose username contains it, or that are among
	// UserIDs: those whose email address or phone number contains it
	// (Users.Search; B167, A93).
	Q       string
	UserIDs []string
	// Kinds keeps the accounts of these kinds (L1); empty, all of them.
	Kinds []string
	// IncludePurged lists the test accounts cleared out too (L4), which
	// user-service leaves out by default.
	IncludePurged bool
	CreatedFrom   time.Time
	CreatedBefore time.Time
	Cursor        string
	Limit         int
}

// UserStats are the overview's account counts.
type UserStats struct {
	Total        int64
	CreatedSince int64
	// Days maps a UTC day (YYYY-MM-DD) to its new accounts.
	Days map[string]int64
	// ByKind are the two counts per kind (L0: HUMAN, BOT, TEST, SYSTEM);
	// empty from a user-service before it.
	ByKind []KindCount
}

// KindCount is the accounts of one kind, and those created since.
type KindCount struct {
	Kind         string
	Total        int64
	CreatedSince int64
}

// KindIDs answers the accounts of some kinds (user-service's GET
// /internal/users/ids, L0), which the user-dimension lists keep or leave
// out (L1); a minute old at most.
type KindIDs interface {
	IDs(ctx context.Context, kinds []string) ([]string, error)
}

// KindFilter narrows a user-dimension list by its accounts' kinds (L1):
// Only keeps these accounts (none: nothing), Except leaves these out;
// both nil, every account. Narrowed says the humans' filter leaves out
// the bots and HOUSE only, the test accounts being too many for the
// list's service.
type KindFilter struct {
	Only     []string
	Except   []string
	Narrowed bool
}

// On is whether the filter narrows anything.
func (f KindFilter) On() bool { return f.Only != nil || f.Except != nil }

// Users reads and changes accounts (auth-, user- and ledger-service).
type Users interface {
	// Find returns the user of an email address, a phone number or a
	// username (B167); NOT_FOUND for anything else.
	Find(ctx context.Context, identifier string) (string, error)
	// Search returns the users whose email address or phone number
	// contains q, at most limit of them, newest first (auth-service, B167).
	Search(ctx context.Context, q string, limit int) ([]string, error)
	Get(ctx context.Context, userID string) (User, error)
	Balances(ctx context.Context, userID string) ([]Balance, error)
	// ChangeStatus returns the previous status.
	ChangeStatus(ctx context.Context, userID, to, reason, actor, note string) (string, error)
	// List returns a page of accounts, newest first, and the cursor of
	// the next ("" on the last).
	List(ctx context.Context, q UserQuery) ([]User, string, error)
	// Stats counts all accounts, those created since, and per day for the
	// last days: of the kinds given, every kind's when none (B185, L1).
	Stats(ctx context.Context, since time.Time, days int, kinds []string) (UserStats, error)
	// ResetUsername gives an account a new drawn username and returns the
	// one before; ResetAvatar takes it back to the default avatar and says
	// whether there was one (design 2026-10-07, avatars and usernames §1.6).
	ResetUsername(ctx context.Context, userID, actor, reason string) (User, string, error)
	ResetAvatar(ctx context.Context, userID, actor, reason string) (User, bool, error)
}

// Identity is one of an account's sign-in identities.
type Identity struct {
	// Kind is EMAIL or PHONE.
	Kind string
	// Value is the lower-case email or E.164 number (masked unless the
	// console reveals it).
	Value      string
	VerifiedAt time.Time
	CreatedAt  time.Time
}

// LiveSession is one of an account's sessions that has not ended; IP is
// masked.
type LiveSession struct {
	ID         string
	DeviceID   string
	ClientType string
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// Device is a device an account signed in from.
type Device struct {
	ID          string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// Security is an account's sign-in security.
type Security struct {
	Identities []Identity
	// TOTP is ACTIVE, PENDING (set up, not confirmed) or "" (none).
	TOTP            string
	TOTPActivatedAt time.Time
	// TOTPChangedAt is the app's latest removal (by the user or an
	// administrator): withdrawals wait for review for a day after it.
	TOTPChangedAt     time.Time
	PasswordChangedAt time.Time
	LastLoginAt       time.Time
	// LockedSeconds is how long password sign-in stays locked, 0 when it
	// is not.
	LockedSeconds int
	Sessions      []LiveSession
	Devices       []Device
	// PendingIdentityRequests counts its rebind requests waiting.
	PendingIdentityRequests int
}

// LoginEntry is a sign-in attempt; IP is masked.
type LoginEntry struct {
	ID           int64
	Method       string
	Result       string
	IdentityMask string
	DeviceID     string
	UserAgent    string
	IP           string
	NewDevice    bool
	CreatedAt    time.Time
}

// IdentityRequest is a request to move an identity to a new value, which
// an administrator decides when the account has no other identity.
type IdentityRequest struct {
	ID     string
	UserID string
	Kind   string
	// NewValue and CurrentValue are masked unless revealed; CurrentValue
	// is "" when the identity is gone.
	NewValue     string
	CurrentValue string
	// Status is PENDING_REVIEW, APPROVED or REJECTED.
	Status    string
	CreatedAt time.Time
	DecidedAt time.Time
	DecidedBy string
	Reason    string
}

// IdentityRequestQuery selects rebind requests; empty fields match all.
type IdentityRequestQuery struct {
	Status string
	UserID string
	Cursor string
	Limit  int
}

// AccountSecurity reads and changes an account's sign-in security
// (auth-service); actor names the administrator, who has been checked.
type AccountSecurity interface {
	Get(ctx context.Context, userID string) (Security, error)
	// LoginHistory returns sign-ins newest first before the one with ID
	// beforeID (0: the newest) and the beforeID of the next page (0 on the
	// last).
	LoginHistory(ctx context.Context, userID string, beforeID int64, limit int) ([]LoginEntry, int64, error)
	// RevokeSessions ends one live session, or all when sessionID is "",
	// and returns how many ended.
	RevokeSessions(ctx context.Context, userID, sessionID, actor, reason string) (int, error)
	// ResetTOTP removes the authenticator app; false when there was none.
	ResetTOTP(ctx context.Context, userID, actor, reason string) (bool, error)
	// TemporaryPassword sets a random password, ends every session and
	// returns the password and how many sessions ended.
	TemporaryPassword(ctx context.Context, userID, actor, reason string) (string, int, error)
	IdentityRequests(ctx context.Context, q IdentityRequestQuery) ([]IdentityRequest, string, error)
	DecideIdentityRequest(ctx context.Context, id string, approve bool, actor, reason string) (IdentityRequest, error)
}

// StatusChange is a move of an account to another status.
type StatusChange struct {
	From   string
	To     string
	Reason string
	// Actor is an administrator's email, cli:<os user> or a service.
	Actor string
	At    time.Time
}

// Consent is a document version an account accepted.
type Consent struct {
	// Document is TERMS or RISK_DISCLOSURE.
	Document   string
	Version    string
	AcceptedAt time.Time
}

// AccountHistory reads an account's status changes, newest first, and its
// consents (user-service).
type AccountHistory interface {
	History(ctx context.Context, userID string) ([]StatusChange, []Consent, error)
}

// RuleHit is a risk rule that matched.
type RuleHit struct {
	Rule   string
	Score  int
	Detail string
}

// Assessment is an assessment of the risk rules with at least one hit.
type Assessment struct {
	ID              string
	SourceEventType string
	Score           int
	// Action is NONE, STEP_UP, REVIEW or REJECT.
	Action string
	Hits   []RuleHit
	// Enforced tells whether the action was carried out (risk.enforce).
	Enforced  bool
	CreatedAt time.Time
}

// Risk reads risk-service's assessments of an account, newest first.
type Risk interface {
	Assessments(ctx context.Context, userID string, limit int) ([]Assessment, error)
}

// Orders cancels a user's orders (spot-trading-service).
type Orders interface {
	CancelAll(ctx context.Context, userID string) error
	// Cancel asks the engine to cancel one of a user's spot orders; the
	// answer is the order.
	Cancel(ctx context.Context, userID, orderID string) (json.RawMessage, error)
}

// WithdrawalQuery selects withdrawals: a status (PENDING_REVIEW when
// empty, ALL for every status), a user and an asset, a page.
type WithdrawalQuery struct {
	Status  string
	UserID  string
	Asset   string
	Network string
	Cursor  string
	Limit   int
	// Order is asc (oldest first, the review queue's default) or desc.
	Order string
	// Held is "true" or "false" ("" for both); MinValue and MaxValue bound
	// the worth in USDT, MinRisk the risk score ("" or 0: unbounded).
	Held     string
	MinValue string
	MaxValue string
	MinRisk  int
	// Kinds are the kinds asked for (L1), ByKind their accounts (the
	// application's, at most 5,000 for the service's POST .../list).
	Kinds  []string
	ByKind KindFilter
}

// Suspension is an asset whose withdrawals are suspended: new requests are
// refused, approved ones wait until a person lifts it.
type Suspension struct {
	Asset string `json:"asset"`
	// Shortfall is what the custody checks found missing (0 for an
	// operator's suspension).
	Shortfall   string    `json:"shortfall"`
	Reason      string    `json:"reason"`
	SuspendedBy string    `json:"suspended_by"`
	SuspendedAt time.Time `json:"suspended_at"`
}

// DepositReviewQuery selects deposits in wallet-service: a user, a status, a
// network, those waiting for a decision (Attention), the backfilled ones
// without a callback yet (ManualPending), a page.
type DepositReviewQuery struct {
	UserID        string
	Status        string
	Network       string
	Attention     bool
	ManualPending bool
	Cursor        string
	Limit         int
	// Kinds and ByKind as a WithdrawalQuery's (L1).
	Kinds  []string
	ByKind KindFilter
}

// ManualDeposit is a backfill of a custodian deposit whose callback was
// lost, as an administrator enters it (design 2026-10-02 §4.3).
type ManualDeposit struct {
	Network string
	TradeID string
	Address string
	TxHash  string
	Amount  decimal.Decimal
	// Actor is the administrator who entered it.
	Actor string
}

// ManualCheck is what wallet-service would book for a backfill.
type ManualCheck struct {
	UserID string `json:"user_id"`
	Asset  string `json:"asset"`
	// Unclaimed is true below the network's minimum (booked to
	// UNCLAIMED_DEPOSIT).
	Unclaimed bool `json:"unclaimed"`
}

// Deposits handles the deposits that need a person (wallet-service's
// internal API; the items pass through as it renders them).
type Deposits interface {
	List(ctx context.Context, q DepositReviewQuery) (json.RawMessage, error)
	Get(ctx context.Context, id string) (json.RawMessage, error)
	// Credit gives an unclaimed deposit's funds to its user; Dismiss closes
	// a deposit that waited for a decision.
	Credit(ctx context.Context, id, actor, reason string) (json.RawMessage, error)
	Dismiss(ctx context.Context, id, actor, reason string) (json.RawMessage, error)
	// Assign credits a deposit of nobody (B7a) to userID: its owner set
	// and its funds released from UNCLAIMED_DEPOSIT, audited by
	// wallet-service; the same assignment again replays it.
	Assign(ctx context.Context, id, userID, actor, reason string) (json.RawMessage, error)
	// CheckManual checks a backfill without booking it; BookManual books
	// it (the same backfill again returns its deposit).
	CheckManual(ctx context.Context, m ManualDeposit) (ManualCheck, error)
	BookManual(ctx context.Context, m ManualDeposit, reason string) (json.RawMessage, error)
}

// CallbackQuery selects the custodians' callbacks: a custodian (any when
// empty), an outcome, a kind, a trade/withdrawal ID, transaction hash or
// address, a page.
type CallbackQuery struct {
	Provider string
	Result   string
	Kind     string
	Query    string
	Cursor   string
	Limit    int
}

// FeeQuery selects the custodians' withdrawal fees: a custodian (any when
// empty), a status (HELD, BOOKABLE, WRITTEN_OFF; any when empty), a page.
type FeeQuery struct {
	Provider string
	Status   string
	Cursor   string
	Limit    int
	// Kinds and ByKind as a WithdrawalQuery's (L1): the withdrawals'
	// users.
	Kinds  []string
	ByKind KindFilter
}

// FeeBooking is an administrator's decision to book a custodian's fee
// held for a person: as reported (Asset empty, Amount zero) or in the
// asset and amount found charged.
type FeeBooking struct {
	WithdrawalID string
	Asset        string
	Amount       decimal.Decimal
	Actor        string
	Reason       string
}

// Withdrawals lists and reviews withdrawals and shows the custody wallet
// (wallet-service); answers pass through as the wallet renders them.
type Withdrawals interface {
	List(ctx context.Context, q WithdrawalQuery) (json.RawMessage, error)
	// Review approves or rejects a withdrawal in review; a positive
	// soleMax lets this approval complete one worth at most that much in
	// USDT however many reviewers it needs (single-person mode), and a
	// positive atLeast raises the reviewers it needs to that many.
	Review(ctx context.Context, id string, approve bool, reviewer, reason string, soleMax decimal.Decimal, atLeast int) (json.RawMessage,
		error)
	// Custody describes a custodian (provider; wallet-service's default,
	// UDUN, when empty): coins, checks, what is with it.
	Custody(ctx context.Context, provider string) (json.RawMessage, error)
	Callbacks(ctx context.Context, q CallbackQuery) (json.RawMessage, error)
	Callback(ctx context.Context, id string) (json.RawMessage, error)
	// Replay applies a stored callback again for actor.
	Replay(ctx context.Context, id, actor, reason string) (json.RawMessage, error)
	// Fees pages through the custodians' withdrawal fees, newest first.
	Fees(ctx context.Context, q FeeQuery) (json.RawMessage, error)
	// BookFee books a fee held for a person from GAS_SUPPLY; WriteOffFee
	// writes one off. wallet-service audits both for the actor, as
	// exchangectl wallet custody-fee does; a fee that waits for no one is
	// a conflict.
	BookFee(ctx context.Context, b FeeBooking) (json.RawMessage, error)
	WriteOffFee(ctx context.Context, withdrawalID, actor, reason string) (json.RawMessage, error)
	// Detail returns a withdrawal with its address-book entry and its
	// user's withdrawals' worth today and this month.
	Detail(ctx context.Context, id string) (json.RawMessage, error)
	// Hold puts a withdrawal in review on hold with a note, or off hold.
	Hold(ctx context.Context, id string, hold bool, reviewer, note string) (json.RawMessage, error)
	// Suspensions lists the assets whose withdrawals are suspended (funds
	// missing on two custody checks, or an operator); Resume lifts one
	// for actor, as exchangectl wallet withdrawals-resume does (the
	// wallet audits it).
	Suspensions(ctx context.Context) ([]Suspension, error)
	Resume(ctx context.Context, asset, actor, reason string) (Suspension, error)
}

// Instruments lists and changes reference data (instrument-service).
type Instruments interface {
	// List returns the assets, pairs and contracts as JSON.
	List(ctx context.Context) (json.RawMessage, error)
	// SetPairStatus returns the previous status.
	SetPairStatus(ctx context.Context, symbol, to, reason, actor string) (string, error)
	// SetContractStatus returns the previous status.
	SetContractStatus(ctx context.Context, symbol, to, reason, actor string) (string, error)
	// Export returns the reference data as a config document (the shape
	// of deploy/instruments/<env>.json).
	Export(ctx context.Context) (json.RawMessage, error)
	// Apply applies a config document for the console: creates and
	// updates, statuses left alone, nothing deleted; dryRun changes
	// nothing.
	Apply(ctx context.Context, config json.RawMessage, dryRun bool, actor, reason string) (ConfigResult, error)
	// AssetProfile returns an asset's profile (the name, introductions,
	// links and logo the sites show, ASTRA design §5.3) as JSON.
	AssetProfile(ctx context.Context, code string) (json.RawMessage, error)
	// UpdateAssetProfile replaces an asset's profile and returns it.
	UpdateAssetProfile(ctx context.Context, w ProfileWrite) (json.RawMessage, error)
}

// ProfileWrite is an asset's profile as the console writes it: Logo (with
// LogoMIME) replaces the logo, ClearLogo removes it, neither keeps it.
type ProfileWrite struct {
	Code        string
	DisplayName string
	Description map[string]string
	Links       map[string]string
	Logo        []byte
	LogoMIME    string
	ClearLogo   bool
	Actor       string
	Reason      string
}

// ConfigResult is what applying a config document does.
type ConfigResult struct {
	Changes   []ConfigChange `json:"changes"`
	Unchanged int            `json:"unchanged"`
	// Warnings are the console's notes on the changes (HOUSE will not
	// quote a pair, the reference streams reconnect).
	Warnings []ConfigWarning `json:"warnings"`
}

// ConfigWarning is a note on a change: a code, the pair or contract it is
// about and a detail (a reference symbol, an index pair).
type ConfigWarning struct {
	Code   string `json:"code"`
	Symbol string `json:"symbol"`
	Detail string `json:"detail"`
}

// The notes of a config document's preview.
const (
	// WarnReferenceUnchecked: the reference market could not be asked.
	WarnReferenceUnchecked = "REFERENCE_UNCHECKED"
	// WarnHouseNotListed: HOUSE quotes it only once it is on the
	// market.house_liquidity flag's symbol list.
	WarnHouseNotListed = "HOUSE_NOT_LISTED"
	// NoteHouseQuotes: HOUSE will quote it on the reference market's book
	// (it is on the flag's symbol list).
	NoteHouseQuotes = "HOUSE_QUOTES"
	// WarnNoIndexReference: the contract's index pair follows no
	// reference market; the platform's own market prices it.
	WarnNoIndexReference = "NO_INDEX_REFERENCE"
	// WarnNoFutures: the reference market has no futures on the symbol,
	// so HOUSE gives the contract no book.
	WarnNoFutures = "NO_FUTURES"
	// WarnStreamsReconnect: market-data-service reconnects every
	// reference stream; reference books are empty for about 20 seconds.
	WarnStreamsReconnect = "STREAMS_RECONNECT"
	// WarnStatusIgnored: the document gives an existing pair or contract
	// another status than it has; a document never moves one (a status
	// change does), so it is left as it is (C5.5 ⑩).
	WarnStatusIgnored = "STATUS_IGNORED"
)

// ConfigChange is an item a config document creates or updates: before
// (null when created) and after, at its version after the change.
type ConfigChange struct {
	Entity  string          `json:"entity"`
	Key     string          `json:"key"`
	Action  string          `json:"action"`
	Version int64           `json:"version"`
	Before  json.RawMessage `json:"before"`
	After   json.RawMessage `json:"after"`
}

// ReferenceMarket checks symbols of the reference market (Binance,
// through market-data-service).
type ReferenceMarket interface {
	// Listed reports whether it lists a symbol (BTCUSDT) on its spot
	// market and on USDⓈ-M futures.
	Listed(ctx context.Context, symbol string) (spot, futures bool, err error)
}

// Derivatives watches the perpetual contracts (derivatives-service's
// internal API); the items pass through as it renders them.
type Derivatives interface {
	// Contracts returns each contract's status, reduce-only state, mark
	// price and open interest.
	Contracts(ctx context.Context) (json.RawMessage, error)
	// LiftReduceOnly ends a contract's reduce-only; the answer says
	// whether it was on.
	LiftReduceOnly(ctx context.Context, symbol, actor string) (json.RawMessage, error)
	// Risk returns the positions warned, taken over or close to it, of
	// the accounts a kind filter keeps (L1).
	Risk(ctx context.Context, f KindFilter) (json.RawMessage, error)
	// OpenPositions returns every user's open positions, riskiest first:
	// {positions, truncated}.
	OpenPositions(ctx context.Context, q PositionQuery) (json.RawMessage, error)
	// Positions returns a user's open positions as derivatives-service
	// renders them.
	Positions(ctx context.Context, userID string) (json.RawMessage, error)
	// OpenOrders returns a user's active contract orders.
	OpenOrders(ctx context.Context, userID string) (json.RawMessage, error)
	// CancelOrder asks the engine to cancel one of a user's contract
	// orders; the answer is the order.
	CancelOrder(ctx context.Context, userID, orderID string) (json.RawMessage, error)
	// ClosePosition closes a user's position at the market with an order
	// of kind ADMIN (DERIV_CLOSE_PENDING while the user's orders on the
	// contract are being canceled); clientOrderID makes it idempotent.
	ClosePosition(ctx context.Context, userID, symbol, positionSide, clientOrderID string) (json.RawMessage, error)
	// Order returns one of a user's contract orders as the user's API
	// renders it.
	Order(ctx context.Context, userID, orderID string) (json.RawMessage, error)
	// CrossMargin measures a user's cross margin account in an asset (its
	// FUTURES account of that settlement asset; empty: USDT) and what a
	// debit of its available balance would leave (equity, maintenance,
	// states), as JSON.
	CrossMargin(ctx context.Context, userID, asset string, debit decimal.Decimal) (json.RawMessage, error)
	// TierImpact measures a contract's new risk ladder (the config
	// document's risk_tiers) against its open positions, changing nothing.
	TierImpact(ctx context.Context, symbol string, tiers json.RawMessage) (TierImpact, error)
	// PriceImpact measures a contract's open positions at a mark price
	// (those it would liquidate, their accounts, what the insurance fund
	// would bear), changing nothing; the answer is its JSON.
	PriceImpact(ctx context.Context, symbol, price string) (json.RawMessage, error)
}

// Content is notification-service's announcements, help articles and
// in-app messages (design 2026-10-02 §4.5); the bodies are its JSON, the
// writes carry the administrator as actor.
type Content interface {
	// Articles returns {articles} of a section (ANNOUNCEMENT, HELP) in
	// every status.
	Articles(ctx context.Context, section string) (json.RawMessage, error)
	Article(ctx context.Context, id string) (json.RawMessage, error)
	CreateArticle(ctx context.Context, a ArticleWrite) (json.RawMessage, error)
	UpdateArticle(ctx context.Context, id string, a ArticleWrite) (json.RawMessage, error)
	// PublishArticle shows it from publishAt on (now when nil).
	PublishArticle(ctx context.Context, id string, version int, publishAt *time.Time, actor string) (json.RawMessage, error)
	ArchiveArticle(ctx context.Context, id string, version int, actor string) (json.RawMessage, error)
	// Broadcasts returns {items, next_cursor}.
	Broadcasts(ctx context.Context, cursor string, limit int) (json.RawMessage, error)
	Broadcast(ctx context.Context, id string) (json.RawMessage, error)
	SendBroadcast(ctx context.Context, b BroadcastWrite) (json.RawMessage, error)
	// ResumeBroadcast sends a FAILED message again from where it stopped
	// (C5.5 ⑫).
	ResumeBroadcast(ctx context.Context, id, actor string) (json.RawMessage, error)
}

// ArticleWrite is an article as the console writes it.
type ArticleWrite struct {
	Section string `json:"section,omitempty"`
	Slug    string `json:"slug"`
	// Modes is TEST, FORMAL or BOTH (design 2026-10-04 §4.4): left out, a
	// draft is for both modes and an edit keeps the article's own.
	Modes    string        `json:"modes,omitempty"`
	Category string        `json:"category"`
	Pinned   bool          `json:"pinned"`
	Order    int           `json:"order"`
	Texts    []ArticleText `json:"texts"`
	Version  int           `json:"version,omitempty"`
	Actor    string        `json:"actor"`
}

// ArticleText is an article in one language (Markdown body).
type ArticleText struct {
	Locale  string `json:"locale"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Body    string `json:"body"`
}

// BroadcastWrite is an in-app message to some users (their IDs) or all,
// under the ID the console chose (sending it again returns it).
type BroadcastWrite struct {
	ID       string            `json:"id"`
	Audience string            `json:"audience"`
	UserIDs  []string          `json:"user_ids"`
	Title    map[string]string `json:"title"`
	Body     map[string]string `json:"body"`
	Link     string            `json:"link"`
	Email    bool              `json:"email"`
	Actor    string            `json:"actor"`
}

// TierImpact is what a new risk ladder would do to a contract's open
// positions at the mark prices (derivatives-service measures it as its
// margin monitor does; HOUSE aside).
type TierImpact struct {
	Symbol string `json:"symbol"`
	// Positions counts the open positions; Liquidated those the monitor
	// would take over that it does not now (a cross account whole), with
	// their notional and their accounts.
	Positions  int    `json:"positions"`
	Liquidated int    `json:"liquidated"`
	Notional   string `json:"notional"`
	Accounts   int    `json:"accounts"`
	// Warned are newly within 1.2 times their maintenance margin;
	// OverLimit above the risk limit of their leverage; Unmeasured without
	// a fresh mark price.
	Warned     int             `json:"warned"`
	OverLimit  int             `json:"over_limit"`
	Unmeasured int             `json:"unmeasured"`
	Examples   json.RawMessage `json:"examples"`
}

// PositionQuery selects open positions across users: a contract, a user,
// only those under watch (warned, taken over, margin ratio ≥ 0.5), at
// most Limit.
type PositionQuery struct {
	Symbol string
	UserID string
	Watch  bool
	Limit  int
	// Kinds and ByKind as a WithdrawalQuery's (L1).
	Kinds  []string
	ByKind KindFilter
}

// Flag is a feature switch.
type Flag struct {
	Key         string          `json:"key"`
	Enabled     bool            `json:"enabled"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
	Version     int64           `json:"version"`
	UpdatedBy   string          `json:"updated_by"`
	// UpdatedAt is nil for a flag never set (off).
	UpdatedAt *time.Time `json:"updated_at"`
}

// FlagChange is one change of a flag: on or off after it, when and by whom.
type FlagChange struct {
	Enabled bool
	At      time.Time
	By      string
}

// Flags reads and switches feature flags (the shared config schema); a
// change is recorded with its audit event.
type Flags interface {
	List(ctx context.Context) ([]Flag, error)
	Switch(ctx context.Context, key string, enabled bool, actor, reason string) (Flag, error)
	// History returns a flag's latest changes, newest first (the config
	// schema's flag_changes).
	History(ctx context.Context, key string, limit int) ([]FlagChange, error)
}

// Features evaluates the feature flags that change the console's own
// behavior (a *flags.Client).
type Features interface {
	Enabled(key string, s flags.Subject) bool
}

// Ledger books manual adjustments and insurance fund contributions and
// reads the platform's system accounts (ledger-service).
type Ledger interface {
	// Adjust credits or debits a user's SPOT or FUTURES account.
	Adjust(ctx context.Context, key, userID, accountType, asset string, amount decimal.Decimal, actor, reason string) (journalID string, err error)
	FundInsurance(ctx context.Context, key, asset string, amount decimal.Decimal, actor, reason string) (journalID string, err error)
	// SystemBalances returns the system accounts in an asset.
	SystemBalances(ctx context.Context, asset string) ([]Balance, error)
	// PlaceHold freezes part of a user's SPOT balance under the hold ID
	// (repeating it returns the hold); ReleaseHold returns it; the ledger
	// audits both with actor.
	PlaceHold(ctx context.Context, id, userID, asset string, amount decimal.Decimal, actor, reason string) (Hold, error)
	ReleaseHold(ctx context.Context, id, actor, reason string) (Hold, error)
	// Holds lists a user's holds, newest first.
	Holds(ctx context.Context, userID string, activeOnly bool) ([]Hold, error)
}

// Hold is an administrator's hold on part of a user's SPOT balance.
type Hold struct {
	ID          string
	UserID      string
	AccountType string
	Asset       string
	Amount      string
	Reason      string
	Actor       string
	JournalID   string
	CreatedAt   time.Time
	// ReleasedAt is zero while the hold is active.
	ReleasedAt       time.Time
	ReleasedBy       string
	ReleaseReason    string
	ReleaseJournalID string
}

// AuditEntry is a line of the audit trail.
type AuditEntry struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	Actor      string          `json:"actor"`
	Target     string          `json:"target"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// AuditQuery selects audit entries: an exact actor, target and event
// type, a time range, a page.
type AuditQuery struct {
	Actor     string
	Target    string
	EventType string
	From      time.Time
	To        time.Time
	Cursor    string
	Limit     int
}

// AuditLog reads the audit trail (ClickHouse audit_logs).
type AuditLog interface {
	// Search returns a page of entries, newest first, and the cursor of
	// the next ("" on the last).
	Search(ctx context.Context, q AuditQuery) ([]AuditEntry, string, error)
	// Count counts the entries matching q (its cursor and limit aside).
	Count(ctx context.Context, q AuditQuery) (int, error)
}

// TradingDay is one symbol's trading on one day (UTC); amounts are
// decimal strings.
type TradingDay struct {
	Day         string `json:"day"`
	Symbol      string `json:"symbol"`
	Trades      uint64 `json:"trades"`
	Volume      string `json:"volume"`
	QuoteVolume string `json:"quote_volume"`
	Orders      uint64 `json:"orders"`
	Rejected    uint64 `json:"rejected"`
}

// WalletDay is one asset's credited deposits and confirmed withdrawals on
// one day (UTC).
type WalletDay struct {
	Day              string `json:"day"`
	Asset            string `json:"asset"`
	Deposits         uint64 `json:"deposits"`
	DepositAmount    string `json:"deposit_amount"`
	Withdrawals      uint64 `json:"withdrawals"`
	WithdrawalAmount string `json:"withdrawal_amount"`
	WithdrawalFees   string `json:"withdrawal_fees"`
}

// Candle is a candle of the trades read model.
type Candle struct {
	OpenTime    time.Time `json:"open_time"`
	Open        string    `json:"open"`
	High        string    `json:"high"`
	Low         string    `json:"low"`
	Close       string    `json:"close"`
	Volume      string    `json:"volume"`
	QuoteVolume string    `json:"quote_volume"`
	Trades      uint64    `json:"trades"`
}

// DerivativesDay is one contract's trading, funding and liquidations on
// one day (UTC); amounts are decimal strings in the settlement asset.
type DerivativesDay struct {
	Day    string `json:"day"`
	Symbol string `json:"symbol"`
	// Fills counts the settled sides of trades (two per trade).
	Fills       uint64 `json:"fills"`
	Volume      string `json:"volume"`
	Notional    string `json:"notional"`
	Fees        string `json:"fees"`
	RealizedPnL string `json:"realized_pnl"`
	// FundingPaid and FundingReceived are the positions' payments at the
	// day's settlements.
	FundingPaid     string `json:"funding_paid"`
	FundingReceived string `json:"funding_received"`
	Liquidations    uint64 `json:"liquidations"`
	ADL             uint64 `json:"adl"`
	InsurancePaid   string `json:"insurance_paid"`
	// SettleAsset is the contract's settlement asset (the application's,
	// from the listing); empty while the listing cannot be read.
	SettleAsset string `json:"settle_asset"`
}

// OpenInterest is a contract's open positions in the read model: base
// asset, or whole contracts of an inverse contract.
type OpenInterest struct {
	Symbol      string `json:"symbol"`
	Long        string `json:"long"`
	Short       string `json:"short"`
	Positions   uint64 `json:"positions"`
	SettleAsset string `json:"settle_asset"`
}

// LiquidationStep is a row of the liquidation read model. Its amounts are
// in SettleAsset, the contract's (the application's, from the listing):
// empty for a cross account's warning, which names no contract, and while
// the listing cannot be read.
type LiquidationStep struct {
	EventID           string    `json:"event_id"`
	Kind              string    `json:"kind"`
	UserID            string    `json:"user_id"`
	Symbol            string    `json:"symbol"`
	PositionSide      string    `json:"position_side"`
	Cross             bool      `json:"cross"`
	ADL               bool      `json:"adl"`
	TradeID           string    `json:"trade_id"`
	Price             string    `json:"price"`
	Quantity          string    `json:"quantity"`
	RealizedPnL       string    `json:"realized_pnl"`
	InsurancePaid     string    `json:"insurance_paid"`
	MarkPrice         string    `json:"mark_price"`
	BankruptcyPrice   string    `json:"bankruptcy_price"`
	MarginBalance     string    `json:"margin_balance"`
	MaintenanceMargin string    `json:"maintenance_margin"`
	OccurredAt        time.Time `json:"occurred_at"`
	SettleAsset       string    `json:"settle_asset"`
}

// The buckets a report sums its figures in (design 2026-10-02 §4.6).
const (
	BucketDay   = "day"
	BucketWeek  = "week"
	BucketMonth = "month"
)

// ReportRange is a report's period: whole UTC days from From to To (both
// midnights, To included), summed per day, per week (from Monday) or per
// month; a row's day is its bucket's first.
type ReportRange struct {
	From   time.Time
	To     time.Time
	Bucket string
	// ByKind keeps the figures of the accounts of some kinds (L1, the
	// humans' by default): a spot trade is theirs when one of its sides
	// is.
	ByKind KindFilter
}

// UsersBucket is the users' activity in one bucket: the accounts
// registered, those who signed in, traded (spot or contracts) and got a
// deposit credited, each counted once; Total is every account registered
// by its end. HOUSE and the simulated market's bots are left out.
type UsersBucket struct {
	Day        string `json:"day"`
	Registered uint64 `json:"registered"`
	SignedIn   uint64 `json:"signed_in"`
	Traders    uint64 `json:"traders"`
	Depositors uint64 `json:"depositors"`
	Total      uint64 `json:"total"`
}

// HouseSpotDay is HOUSE's spot trading on one pair and day (ADR-0015):
// the base it bought less sold, the quote it got less paid, and the
// pair's last price that day (any trade's, HOUSE's or not; empty without
// one).
type HouseSpotDay struct {
	Day          time.Time
	Symbol       string
	QuoteAsset   string
	NetBase      decimal.Decimal
	NetQuote     decimal.Decimal
	Close        decimal.Decimal
	HasClose     bool
	BeforePeriod bool
}

// HouseContractDay is HOUSE's result on the contracts on one day: the
// realized results of its fills less their fees, and the funding it got
// (negative when it paid).
type HouseContractDay struct {
	Day      time.Time
	Realized decimal.Decimal
	Funding  decimal.Decimal
}

// Reports reads the ClickHouse read models (trades, orders, wallet,
// candles, contracts).
type Reports interface {
	Trading(ctx context.Context, r ReportRange) ([]TradingDay, error)
	Wallet(ctx context.Context, r ReportRange) ([]WalletDay, error)
	Candles(ctx context.Context, symbol string, seconds uint32, limit int) ([]Candle, error)
	Derivatives(ctx context.Context, r ReportRange) ([]DerivativesDay, error)
	// Users returns the users' activity per bucket, of the accounts the
	// range's kind filter keeps, and how many of them registered before
	// the period.
	Users(ctx context.Context, r ReportRange) ([]UsersBucket, uint64, error)
	// HouseSpot returns HOUSE's spot trading per pair and day in the
	// period, and per pair before it (BeforePeriod, its Day zero: the
	// sums and the last price up to the period).
	HouseSpot(ctx context.Context, r ReportRange) ([]HouseSpotDay, error)
	// HouseContracts returns HOUSE's (houseUser's) contract results per
	// day in the period.
	HouseContracts(ctx context.Context, r ReportRange, houseUser string) ([]HouseContractDay, error)
	// OpenInterest is of the accounts the kind filter keeps (L1).
	OpenInterest(ctx context.Context, f KindFilter) ([]OpenInterest, error)
	// Liquidations returns a page of the liquidation steps, newest first,
	// and the cursor of the next ("" on the last).
	Liquidations(ctx context.Context, q LiquidationQuery) ([]LiquidationStep, string, error)
	// Holdings sums who holds an asset from the ledger's lines: the bots
	// (in bots) apart from the other users, the system accounts, and the
	// top largest holders.
	Holdings(ctx context.Context, asset string, bots []string, top int) (Holdings, error)
}

// MarketMaker is market-maker's internal API (review C45): HOUSE's caps at
// run time - a level's, a pair's and all spot positions', a contract's
// position (USDT), the backed inventory kept back (USDT) and the contracts'
// leverage on HOUSE's contract equity - as decimal strings with their
// version.
type MarketMaker interface {
	HouseCaps(ctx context.Context) (json.RawMessage, error)
	// SetHouseCaps changes the caps given, of the version read
	// (HOUSE_CAPS_VERSION, 409, when it moved).
	SetHouseCaps(ctx context.Context, w HouseCapsWrite) (json.RawMessage, error)
	// HouseCapsChanges lists the latest changes, newest first.
	HouseCapsChanges(ctx context.Context, limit int) (json.RawMessage, error)
	// HouseRooms is what HOUSE may still buy and sell of a pair or
	// contract as last published (J0 contract §4.2); false while it does
	// not quote it.
	HouseRooms(ctx context.Context, symbol string) (HouseRooms, bool, error)
}

// HouseRooms is what HOUSE may still buy and sell of a pair or contract
// (in the base asset, or contracts), what one unit is worth in USDT, and
// whether it is an inverse (COIN-M) contract.
type HouseRooms struct {
	Buy, Sell, UnitValue decimal.Decimal
	Inverse              bool
}

// HouseCapsWrite is a change of HOUSE's caps: the caps that change, by
// their names (level, symbol, total, contract, safety, contract_leverage),
// of Version, asked by Actor and approved by Approver in ApprovalID.
type HouseCapsWrite struct {
	Caps       map[string]string
	Version    int64
	Actor      string
	Approver   string
	ApprovalID string
	Reason     string
}

// Margin is margin-service's internal API for the console
// (/internal/margin/* on its HTTP port, the compose network only; margin
// design 2026-10-06 §8, C5). The answers pass through as it renders them,
// with its column names; writes carry the administrator (X-Admin-Id: their
// email, its updated_by and frozen_by) and margin-service checks its own
// rules (MARGIN_PARAMS_CHANGED for a version moved).
type Margin interface {
	// Assets returns {items}: each asset's terms with what is lent.
	Assets(ctx context.Context) (json.RawMessage, error)
	// SetAsset replaces an asset's terms (body: its asset_terms columns)
	// as of expectedVersion; the answer is the asset.
	SetAsset(ctx context.Context, asset string, terms json.RawMessage, expectedVersion int64, admin string) (json.RawMessage, error)
	// Settings returns the cross account's terms, the thresholds by
	// isolated leverage and the version.
	Settings(ctx context.Context) (json.RawMessage, error)
	// SetCross replaces the cross account's terms as of expectedVersion;
	// the answer is the settings.
	SetCross(ctx context.Context, cross json.RawMessage, expectedVersion int64, admin string) (json.RawMessage, error)
	// Pairs returns {items}: each pair's isolated terms.
	Pairs(ctx context.Context) (json.RawMessage, error)
	// SetPair replaces a pair's isolated terms (isolated, leverage, levels,
	// fee) as of expectedVersion; the answer is the pair.
	SetPair(ctx context.Context, symbol string, terms json.RawMessage, expectedVersion int64, admin string) (json.RawMessage, error)
	// Accounts returns {items, truncated}: the accounts riskiest first.
	Accounts(ctx context.Context, q MarginAccountQuery) (json.RawMessage, error)
	// Account returns one account in full; account is MARGIN_CROSS or
	// MARGIN_ISOLATED:<symbol>.
	Account(ctx context.Context, userID, account string) (json.RawMessage, error)
	// Freeze and Unfreeze set and lift an administrator's freeze; the
	// answer is the account as listed.
	Freeze(ctx context.Context, userID, account, admin, reason string) (json.RawMessage, error)
	Unfreeze(ctx context.Context, userID, account, admin string) (json.RawMessage, error)
	// Liquidate starts the liquidation an approved MARGIN_LIQUIDATE asks
	// for (margin-service's E3: MANUAL, once per approval, the same
	// approval again returns it); the answer is the liquidation
	// ({liquidation_id, ...}).
	Liquidate(ctx context.Context, userID, account, approvalID, admin string) (json.RawMessage, error)
}

// MarginAccountQuery selects margin accounts; empty fields match every
// one, Limit is 1 to 500.
type MarginAccountQuery struct {
	Status  string
	Account string
	Symbol  string
	UserID  string
	Limit   int
	// Kinds and ByKind as a WithdrawalQuery's (L1).
	Kinds  []string
	ByKind KindFilter
}

// MarginReports reads margin trading's read models (ClickHouse
// margin_liquidations, margin_interest, and the ledger's interest rows in
// ledger_entries), a few seconds behind.
type MarginReports interface {
	// MarginLiquidations returns a page of liquidations, newest first, and
	// the cursor of the next ("" on the last).
	MarginLiquidations(ctx context.Context, q MarginLiquidationQuery) ([]MarginLiquidation, string, error)
	// MarginInterest returns the interest per bucket and asset of the
	// period (one asset unless empty), oldest first.
	MarginInterest(ctx context.Context, r ReportRange, asset string) ([]MarginInterestBucket, error)
}

// MarginLiquidationQuery selects liquidations started in the last Days:
// of one account type, pair, trigger or user unless empty.
type MarginLiquidationQuery struct {
	Days    int
	Account string
	Symbol  string
	Trigger string
	UserID  string
	Cursor  string
	Limit   int
	// Kinds are the accounts' kinds asked for (L1), ByKind their
	// accounts.
	Kinds  []string
	ByKind KindFilter
}

// MarginAmount is an amount of an asset.
type MarginAmount struct {
	Asset  string `json:"asset"`
	Amount string `json:"amount"`
}

// MarginLiquidation is a margin account's liquidation as the read model
// merges its two events; what an event published before it was known
// leaves out is empty (Trigger, ApprovalID, the started fields of a row
// seen completed only).
type MarginLiquidation struct {
	ID               string         `json:"liquidation_id"`
	UserID           string         `json:"user_id"`
	Account          string         `json:"account"`
	Symbol           *string        `json:"symbol"`
	Trigger          *string        `json:"trigger"`
	ApprovalID       *string        `json:"approval_id"`
	Status           string         `json:"status"`
	MarginLevel      *string        `json:"margin_level"`
	TotalAsset       *string        `json:"total_asset"`
	TotalLiability   *string        `json:"total_liability"`
	Repaid           []MarginAmount `json:"repaid"`
	Remaining        []MarginAmount `json:"remaining"`
	Fee              *string        `json:"fee"`
	InsuranceCovered *string        `json:"insurance_covered"`
	StartedAt        *time.Time     `json:"started_at"`
	CompletedAt      *time.Time     `json:"completed_at"`
}

// MarginInterestBucket is one bucket's interest of an asset: charged and
// repaid (the ledger's interest rows), owed at its end, the principal
// averaged over the hours charged, the rate that gives, the accounts
// charged, and the two amounts in USDT at the bucket's last price (nil
// without one).
type MarginInterestBucket struct {
	Day           string          `json:"day"`
	Asset         string          `json:"asset"`
	Charged       decimal.Decimal `json:"charged"`
	Repaid        decimal.Decimal `json:"repaid"`
	Owed          decimal.Decimal `json:"owed"`
	PrincipalAvg  decimal.Decimal `json:"principal_avg"`
	HourlyRateAvg decimal.Decimal `json:"hourly_rate_avg"`
	Accounts      uint64          `json:"accounts"`
	ChargedUSDT   *string         `json:"charged_usdt"`
	RepaidUSDT    *string         `json:"repaid_usdt"`
}

// Holder is a user's holding of an asset, in all its accounts.
type Holder struct {
	UserID string
	Amount decimal.Decimal
}

// Holdings is how an asset is held (ClickHouse ledger_entries, a few
// seconds behind the ledger): the users' and the bots' balances with how
// many of each hold some, the system accounts' by type (ADJUSTMENT owes
// what manual adjustments created) and the largest holders.
type Holdings struct {
	Users, Bots             decimal.Decimal
	UserHolders, BotHolders uint64
	System                  map[string]decimal.Decimal
	Top                     []Holder
}

// LiquidationQuery selects liquidation steps of the last Days: of one
// kind, contract or user unless empty.
type LiquidationQuery struct {
	Days   int
	Kind   string
	Symbol string
	UserID string
	Cursor string
	Limit  int
	// Kinds are the accounts' kinds asked for (L1; the query's
	// user_kind, as kind is a step's), ByKind their accounts.
	Kinds  []string
	ByKind KindFilter
}

// The accounts a list of orders or trades keeps (ASTRA design §8 item 6):
// the simulated market's bots, or everyone else.
const (
	AccountsBots  = "bots"
	AccountsUsers = "users"
)

// OrderQuery selects spot orders; empty fields match everything.
type OrderQuery struct {
	UserID  string
	OrderID string
	Symbol  string
	Status  string
	Side    string
	From    time.Time
	To      time.Time
	// Accounts keeps the orders of the Bots (AccountsBots) or of everyone
	// else (AccountsUsers); "" keeps all.
	Accounts string
	Bots     []string
	// Kinds are the kinds asked for (L1), ByKind their accounts (the
	// application's).
	Kinds  []string
	ByKind KindFilter
	Cursor string
	Limit  int
}

// Order is a spot order in its latest state (ClickHouse orders_current).
type Order struct {
	OrderID        string    `json:"order_id"`
	ClientOrderID  string    `json:"client_order_id"`
	UserID         string    `json:"user_id"`
	Symbol         string    `json:"symbol"`
	Side           string    `json:"side"`
	Type           string    `json:"type"`
	TimeInForce    string    `json:"time_in_force"`
	Price          *string   `json:"price"`
	Quantity       *string   `json:"quantity"`
	QuoteAmount    *string   `json:"quote_amount"`
	Status         string    `json:"status"`
	FilledQuantity string    `json:"filled_quantity"`
	FilledQuote    string    `json:"filled_quote"`
	Reason         string    `json:"reason"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	// Bot marks a simulated market's bot's order.
	Bot bool `json:"bot"`
}

// TradeQuery selects spot trades: a symbol, a user on either side, a time
// range, a page.
type TradeQuery struct {
	Symbol string
	UserID string
	From   time.Time
	To     time.Time
	// Accounts keeps the trades between two of the Bots (AccountsBots) or
	// those with anyone else on a side (AccountsUsers); "" keeps all.
	Accounts string
	Bots     []string
	// Kinds are the kinds asked for (L1), ByKind their accounts: a trade
	// is kept when one of its sides is.
	Kinds  []string
	ByKind KindFilter
	Cursor string
	Limit  int
}

// Trade is a spot trade (ClickHouse trades).
type Trade struct {
	TradeID       string    `json:"trade_id"`
	Symbol        string    `json:"symbol"`
	TradeNumber   uint64    `json:"trade_number"`
	Price         string    `json:"price"`
	Quantity      string    `json:"quantity"`
	QuoteQuantity string    `json:"quote_quantity"`
	TakerSide     string    `json:"taker_side"`
	BuyerUserID   string    `json:"buyer_user_id"`
	BuyerOrderID  string    `json:"buyer_order_id"`
	SellerUserID  string    `json:"seller_user_id"`
	SellerOrderID string    `json:"seller_order_id"`
	BuyerIsMaker  bool      `json:"buyer_is_maker"`
	BuyerFee      string    `json:"buyer_fee"`
	SellerFee     string    `json:"seller_fee"`
	ExecutedAt    time.Time `json:"executed_at"`
	// HouseSide is the side HOUSE took (ADR-0015), "" between users.
	HouseSide string `json:"house_side"`
	// BuyerBot and SellerBot mark the simulated market's bots.
	BuyerBot  bool `json:"buyer_bot"`
	SellerBot bool `json:"seller_bot"`
	// SettleAsset is a contract trade's settlement asset (an inverse one's
	// quantity is whole contracts); empty on a spot trade and on a
	// contract's from before the coin-margined contracts (USDT).
	SettleAsset string `json:"settle_asset"`
}

// DepositQuery selects deposits; empty fields match everything.
type DepositQuery struct {
	UserID  string
	Asset   string
	Network string
	Status  string
	TxHash  string
	// Kinds are the kinds asked for (L1), ByKind their accounts.
	Kinds  []string
	ByKind KindFilter
	Cursor string
	Limit  int
}

// Deposit is a deposit in its latest state (ClickHouse wallet_deposits).
type Deposit struct {
	DepositID             string    `json:"deposit_id"`
	UserID                string    `json:"user_id"`
	Asset                 string    `json:"asset"`
	Network               string    `json:"network"`
	Kind                  string    `json:"kind"`
	Address               string    `json:"address"`
	TxHash                string    `json:"tx_hash"`
	Amount                string    `json:"amount"`
	Status                string    `json:"status"`
	Unclaimed             bool      `json:"unclaimed"`
	Reason                string    `json:"reason"`
	Confirmations         uint32    `json:"confirmations"`
	RequiredConfirmations uint32    `json:"required_confirmations"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// Turnover is a day's (or a period's) traded value in one quote asset.
type Turnover struct {
	QuoteAsset string `json:"quote_asset"`
	Amount     string `json:"amount"`
}

// Activity is the overview's trading and wallet figures from the read
// models: the last 24 hours, and per UTC day for the last days.
type Activity struct {
	Trades24h         uint64
	ActiveTraders24h  uint64
	Turnover24h       []Turnover
	PendingDeposits   uint64
	PendingWithdraws  uint64
	RiskEvents24h     uint64
	TradesByDay       map[string]uint64
	TurnoverUSDTByDay map[string]string
	// OtherTrades24h are the last 24 hours' trades without a side of the
	// kinds kept, by the other kind on a side (TEST before BOT);
	// OtherTraders24h the traders of each other kind (L1).
	OtherTrades24h  map[string]uint64
	OtherTraders24h map[string]uint64
}

// ActivityKinds are the accounts the overview's figures are of (L1): Keep
// filters them (the humans' by default); Others are the other kinds'
// accounts by kind, counted apart for the cards' small print. Both empty
// while the kinds cannot be read: every account, nothing apart.
type ActivityKinds struct {
	Keep   KindFilter
	Others map[string][]string
}

// Records pages through the read models' orders, trades and deposits and
// sums the overview's activity (ClickHouse).
type Records interface {
	// Each returns a page, newest first, and the cursor of the next (""
	// on the last).
	Orders(ctx context.Context, q OrderQuery) ([]Order, string, error)
	Trades(ctx context.Context, q TradeQuery) ([]Trade, string, error)
	Deposits(ctx context.Context, q DepositQuery) ([]Deposit, string, error)
	Activity(ctx context.Context, days int, k ActivityKinds) (Activity, error)
	// OpenOrders counts the orders resting on a pair or contract (spot
	// and contract orders share the read model).
	OpenOrders(ctx context.Context, symbol string) (int, error)
}

// FeedStatus is market-data-service's reference feed state.
type FeedStatus struct {
	State      string          `json:"state"`
	ReceivedAt *time.Time      `json:"received_at"`
	Followed   []string        `json:"followed"`
	Halted     json.RawMessage `json:"halted"`
}

// Market reads market-data-service's internal state.
type Market interface {
	Feed(ctx context.Context) (FeedStatus, error)
}

// Prices are the last prices of the listed symbols (market-data-service's
// tickers), by symbol.
type Prices map[string]decimal.Decimal

// MarketPrices reads the last prices of every listed symbol; a positive
// maxAge leaves out those not updated within it (a reference feed that
// stopped).
type MarketPrices interface {
	Prices(ctx context.Context, maxAge time.Duration) (Prices, error)
}

// ProductLines are the services that run the product lines (design
// 2026-10-07, product switches): spot-trading-service spot,
// derivatives-service usdt_m and coin_m. Line counts what closing a line
// touches now (GET /internal/products/{line}: its open orders, conditional
// ones included, and positions, HOUSE's and the market makers' left out);
// CancelOpen cancels its open orders (POST
// /internal/products/{line}/cancel-open; the service audits each cancel),
// with an error what it did before failing. ErrProductLineMissing while a
// service has no such endpoint yet (deployed before K1a or K1b); a cancel
// that did not answer in time is ErrProductCancelTimeout, one that could
// not reach its service ErrProductCancelUnreachable (each wrapping the
// cause).
type ProductLines interface {
	Line(ctx context.Context, product string) (ProductLine, error)
	CancelOpen(ctx context.Context, product, actor, reason string) (ProductCanceled, error)
}

// ProductCanceled is what a line's cancel-open did: the orders it canceled
// and, failing part way, the users it could not cancel for (both services'
// 503 names them: C60, A86, A87).
type ProductCanceled struct {
	Orders, FailedUsers int
}

// The ways a cancel-open fails without the service's own answer.
var (
	ErrProductCancelTimeout     = errors.New("the service did not answer in time")
	ErrProductCancelUnreachable = errors.New("the service could not be reached")
)

// ProductLine is what closing a product line touches now.
type ProductLine struct {
	OpenOrders, OpenPositions int
}

// ErrProductLineMissing tells a service without the product line's
// endpoints.
var ErrProductLineMissing = errors.New("the service has no product line endpoints yet")

// HousePair is HOUSE's spot trading on one pair in the trades read model
// (ADR-0015): the base it bought and sold, the quote it paid and got.
type HousePair struct {
	Symbol     string    `json:"symbol"`
	Trades     uint64    `json:"trades"`
	BoughtBase string    `json:"bought_base"`
	SoldBase   string    `json:"sold_base"`
	PaidQuote  string    `json:"paid_quote"`
	GotQuote   string    `json:"got_quote"`
	LastAt     time.Time `json:"last_at"`
}

// SimBots names the simulated market's bots (market-sim), which the
// users' figures leave out.
type SimBots interface {
	BotUsers(ctx context.Context) ([]string, error)
}

// Sim is market-sim's management API (the simulated market of the
// platform coin, ASTRA design §5.1, docs/runbook/market-sim.md): reads as
// it renders them; writes signed with the console's key, carrying the
// administrator who asks and the one who approved (never taken from the
// browser).
type Sim interface {
	SimBots
	// Status is the market's state: target and last price, the band, the
	// bots, the running events.
	Status(ctx context.Context) (json.RawMessage, error)
	// History is the target and last price every 10 seconds over the last
	// minutes (at most a day).
	History(ctx context.Context, minutes int) (json.RawMessage, error)
	// Events lists the running and scheduled events, or with all the
	// latest of every status.
	Events(ctx context.Context, all bool, limit int) (json.RawMessage, error)
	// CreateEvent creates a price event: event is its fields as market-sim
	// takes them, without actor and approved_by.
	CreateEvent(ctx context.Context, event map[string]any, actor, approvedBy string) (json.RawMessage, error)
	// EndEvent cancels a scheduled event or ends a running one.
	EndEvent(ctx context.Context, id, actor, reason string) (json.RawMessage, error)
	// UpdateParams replaces the settings (every field).
	UpdateParams(ctx context.Context, params json.RawMessage, actor, approvedBy string) (json.RawMessage, error)
	// Plan is a threshold target's plan (ASTRA A6): its envelope minute
	// by minute, its spikes and where the market is against it now.
	Plan(ctx context.Context, id string) (json.RawMessage, error)
	// TargetPreview says whether a threshold target is feasible from the
	// current target, the shortest window that is, the move it plans and
	// whether a second administrator must approve it, with its envelope.
	TargetPreview(ctx context.Context, q SimTargetQuery) (json.RawMessage, error)
}

// SimTargetQuery is a threshold target to preview: a direction (ABOVE,
// BELOW; inferred when empty), a level, a window, a start (now when
// empty).
type SimTargetQuery struct {
	Direction       string
	Price           string
	DurationSeconds int
	StartsAt        string
}

// HouseTrades sums HOUSE's spot trades per pair (ClickHouse).
type HouseTrades interface {
	HousePairs(ctx context.Context) ([]HousePair, error)
}

// HousePositions reads HOUSE's perpetual contract positions as
// derivatives-service renders a user's positions.
type HousePositions interface {
	Positions(ctx context.Context, userID string) (json.RawMessage, error)
}

// ReconciliationRun is one invariant check of one ledger reconciliation.
type ReconciliationRun struct {
	Check      string          `json:"check"`
	StartedAt  time.Time       `json:"started_at"`
	Mismatches int             `json:"mismatches"`
	Details    json.RawMessage `json:"details"`
}

// Reconciliation is the latest run of every check and the recent runs
// that found mismatches.
type Reconciliation struct {
	Latest   []ReconciliationRun `json:"latest"`
	Failures []ReconciliationRun `json:"failures"`
}

// Reconciler reads the ledger's reconciliation runs (ledger-service).
type Reconciler interface {
	Reconciliation(ctx context.Context, failures int) (Reconciliation, error)
}

// ServiceHealth is a service's readiness as its ops endpoint answers;
// with details also what its metrics say: its version, how far its Kafka
// consumers lag and how many records they parked in a DLQ since it
// started (nil without consumers).
type ServiceHealth struct {
	Service   string `json:"service"`
	Ready     bool   `json:"ready"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	Version   string `json:"version,omitempty"`
	KafkaLag  *int64 `json:"kafka_lag,omitempty"`
	DLQ       *int64 `json:"dlq,omitempty"`
	// ConfigPresent is whether the third parties the service uses are
	// configured, as it reports them (exchange_config_present{item}, the
	// launch checklist's; never their values).
	ConfigPresent map[string]bool `json:"config_present,omitempty"`
}

// Platform is the platform's profile (instrument-service) and the welcome
// credits (ledger-service), design 2026-10-04 §4.1–4.2; the answers pass
// through as the services render them.
type Platform interface {
	Profile(ctx context.Context) (json.RawMessage, error)
	// UpdateProfile replaces the profile but its images and the welcome
	// credits: write carries expected_version, a stale one is refused.
	UpdateProfile(ctx context.Context, write json.RawMessage, actor, reason string) (json.RawMessage, error)
	// PutImage uploads one of the images (kind: logo_light, logo_dark,
	// favicon, apple_touch_icon), data in base64; DeleteImage removes it.
	PutImage(ctx context.Context, kind, data, mime, actor, reason string) (json.RawMessage, error)
	DeleteImage(ctx context.Context, kind, actor, reason string) (json.RawMessage, error)
	WelcomeCredits(ctx context.Context) (json.RawMessage, error)
	// SetWelcomeCredits replaces the welcome credits as of expectedVersion.
	SetWelcomeCredits(ctx context.Context, credits []WelcomeCredit, expectedVersion int64, actor, reason string) (json.RawMessage, error)
}

// PlatformApps reads and changes the apps to download on
// instrument-service's internal API (design 2026-10-07, App download page
// §7 #9); its answers are the console's (admin.yaml's PlatformAppAdmin).
type PlatformApps interface {
	// Apps returns both platforms, Android first, and the download
	// entries' switch: {"apps": [...], "entry": {...}}.
	Apps(ctx context.Context) (json.RawMessage, error)
	// SetAppEntry shows or hides the sites' download entries (H5): the
	// switch as saved and as it was under its lock, {"entry": ...,
	// "previous": ...} (each admin.yaml's AppEntryAdmin; A89).
	SetAppEntry(ctx context.Context, visible bool, actor, reason string) (json.RawMessage, error)
	// SetApp changes a platform's mode, link, notes and switch; write
	// carries expected_version, a stale one is refused.
	SetApp(ctx context.Context, platform string, write json.RawMessage, actor, reason string) (json.RawMessage, error)
	// AddAppFile keeps a file stored on the disk: {"app": ..., "replaced":
	// the configuration profile it replaced, or null}.
	AddAppFile(ctx context.Context, platform string, f StoredAppFile, actor, reason string) (json.RawMessage, error)
	// DeleteAppFile forgets a file: {"app": ..., "removed": the file}.
	DeleteAppFile(ctx context.Context, platform, fileID, actor, reason string) (json.RawMessage, error)
}

// StoredAppFile is a file stored under /downloads/ as instrument-service
// keeps it: where (StoredAs, an .ipa's Manifest), the site its manifest
// names (Origin, https://<host>) and what the package says.
type StoredAppFile struct {
	FileID     string `json:"file_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	StoredAs   string `json:"stored_as"`
	Manifest   string `json:"manifest"`
	Origin     string `json:"origin"`
	Package    string `json:"package"`
	Version    string `json:"version"`
	Build      string `json:"build"`
	MinOS      string `json:"min_os"`
	UploadedAt string `json:"uploaded_at"`
	UploadedBy string `json:"uploaded_by"`
}

// AppFiles keeps the apps' files on the server's disk (design 2026-10-07
// §7 #4): the parts of the uploads in progress, and the files nginx serves
// under /downloads/.
type AppFiles interface {
	// PutPart stores part n of an upload: exactly size bytes of body,
	// replacing the part sent before.
	PutPart(ctx context.Context, uploadID string, n int, body io.Reader, size int64) error
	// Store joins an upload's parts, checks them against its size and
	// SHA-256 and as the package it claims to be (PLATFORM_APP_FILE_INVALID
	// otherwise), and stores the file as fileID, an .ipa with the
	// manifest.plist for its install from origin (title names an app
	// whose package does not).
	Store(ctx context.Context, u domain.AppUpload, fileID, origin, title string) (StoredAppFile, error)
	// DropUpload removes an upload's parts.
	DropUpload(uploadID string) error
	// Remove deletes files stored under /downloads/ (their paths there);
	// those gone already are no error.
	Remove(paths ...string) error
	// Stored lists the files under /downloads/ with when they were last
	// written.
	Stored() ([]StoredPath, error)
	// UploadDirs lists the uploads' directories (their upload IDs as Path)
	// with when they were last written.
	UploadDirs() ([]StoredPath, error)
	// Free is the room left on the downloads' disk, in bytes.
	Free() (uint64, error)
}

// StoredPath is a file under /downloads/ (its path there) and when it was
// last written.
type StoredPath struct {
	Path    string
	ModTime time.Time
}

// AppUploads keeps the uploads in progress (admin 00017 app_uploads; design
// 2026-10-07 §7 #12): what each is and which parts came.
type AppUploads interface {
	Create(ctx context.Context, u domain.AppUpload) error
	// Get returns an upload; nil when unknown.
	Get(ctx context.Context, id string) (*domain.AppUpload, error)
	// Received adds part n to the parts an upload has and returns it; nil
	// when unknown.
	Received(ctx context.Context, id string, n int) (*domain.AppUpload, error)
	// Claim holds an upload for a completion, a part being written or a
	// drop until until; false while another holds it.
	Claim(ctx context.Context, id string, now, until time.Time) (bool, error)
	// Release lets another part, completion or drop take an upload.
	Release(ctx context.Context, id string) error
	// Busy reports whether a completion holds an upload at now.
	Busy(ctx context.Context, id string, now time.Time) (bool, error)
	Delete(ctx context.Context, id string) error
	// Open counts the uploads not expired at now.
	Open(ctx context.Context, now time.Time) (int, error)
	// Expired returns the uploads expired at now.
	Expired(ctx context.Context, now time.Time) ([]domain.AppUpload, error)
}

// WelcomeCredit is what a new account gets of an asset.
type WelcomeCredit struct {
	Asset  string          `json:"asset"`
	Amount decimal.Decimal `json:"amount"`
}

// Health probes every service's readiness.
type Health interface {
	// Check probes every service; details reads their metrics too.
	Check(ctx context.Context, details bool) []ServiceHealth
}
