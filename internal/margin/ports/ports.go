// Package ports declares what margin-service's application layer needs.
package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/platform/flags"
)

// Store is the unit of work over the margin schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
	// Snapshot runs fn in one read-only transaction that sees a single
	// snapshot of the schema (repeatable read).
	Snapshot(ctx context.Context, fn func(Repos) error) error
}

// Repos groups the repositories of one transaction.
type Repos interface {
	// LockUser serializes the transactions that change a user's margin
	// accounts (a transaction-scoped advisory lock). Take it before any
	// pool row.
	LockUser(ctx context.Context, userID string) error
	Terms() TermsRepo
	Pools() PoolRepo
	Rates() RateRepo
	Accounts() AccountRepo
	Loans() LoanRepo
	Borrows() BorrowRepo
	Repays() RepayRepo
	Interest() InterestRepo
	Transfers() TransferRepo
	Reservations() ReservationRepo
	Runs() RunRepo
	// Emit queues an event on topic, keyed by aggregateID.
	Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error
}

// TermsRepo stores the margin terms of assets, pairs and the cross
// account.
type TermsRepo interface {
	Assets(ctx context.Context) ([]domain.AssetTerms, error)
	// Asset returns an asset's terms; ok is false for an asset off the
	// margin list.
	Asset(ctx context.Context, asset string) (domain.AssetTerms, bool, error)
	// SaveAsset inserts or updates an asset's terms (version bumped).
	SaveAsset(ctx context.Context, t domain.AssetTerms, by string) error
	Pairs(ctx context.Context) ([]domain.Pair, error)
	// Pair returns a pair's terms; ok is false for a pair off the list.
	Pair(ctx context.Context, symbol string) (domain.Pair, bool, error)
	SavePair(ctx context.Context, p domain.Pair, by string) error
	// Cross returns the cross account's terms; ok is false before they
	// were first set.
	Cross(ctx context.Context) (domain.Terms, bool, error)
	SaveCross(ctx context.Context, t domain.Terms, by string) error
	// The terms with who last changed them (the seed file, or an
	// administrator in the console).
	CrossWithSource(ctx context.Context) (domain.Terms, string, bool, error)
	AssetWithSource(ctx context.Context, asset string) (domain.AssetTerms, string, bool, error)
	PairWithSource(ctx context.Context, symbol string) (domain.Pair, string, bool, error)
	// AssetMetas, PairMetas and CrossMeta return the terms' versions and
	// who last changed them.
	AssetMetas(ctx context.Context) (map[string]Meta, error)
	PairMetas(ctx context.Context) (map[string]Meta, error)
	CrossMeta(ctx context.Context) (Meta, bool, error)
	// SaveAssetIf, SavePairIf and SaveCrossIf store terms only over the
	// version read (version 0: only where there are none yet); ok is
	// false when the version moved.
	SaveAssetIf(ctx context.Context, t domain.AssetTerms, by string, version int64) (bool, error)
	SavePairIf(ctx context.Context, p domain.Pair, by string, version int64) (bool, error)
	SaveCrossIf(ctx context.Context, t domain.Terms, by string, version int64) (bool, error)
}

// Meta is a row of terms' version and who last changed it: an
// administrator, or SourceFile for the seed.
type Meta struct {
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
}

// PoolRepo stores what each pool has lent.
type PoolRepo interface {
	// LockLent returns what the asset's pool has lent, its row locked for
	// the rest of the transaction (created at zero).
	LockLent(ctx context.Context, asset string) (decimal.Decimal, error)
	// AddLent changes what the pool has lent by delta.
	AddLent(ctx context.Context, asset string, delta decimal.Decimal) error
	// Lent returns what every pool has lent.
	Lent(ctx context.Context) (map[string]decimal.Decimal, error)
}

// Rate is the rate an hour charged on an asset.
type Rate struct {
	Asset   string
	Hour    time.Time
	Model   domain.InterestModel
	Rate    decimal.Decimal
	Lent    decimal.Decimal
	PoolCap decimal.Decimal
}

// RateRepo stores the hourly rates.
type RateRepo interface {
	// Get returns the asset's rate of an hour; ok is false before it is
	// set.
	Get(ctx context.Context, asset string, hour time.Time) (Rate, bool, error)
	// Set stores the rate unless one is stored and returns the one that
	// counts.
	Set(ctx context.Context, r Rate) (Rate, error)
}

// Account is a margin account's state; balances and debts are the
// ledger's.
type Account struct {
	UserID       string
	Account      domain.Account
	Status       domain.Status
	WarnedAt     time.Time
	FrozenReason string
	// FrozenBy is the administrator who froze it, FrozenAt when.
	FrozenBy  string
	FrozenAt  time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AccountFilter narrows a list of accounts; empty fields match all.
type AccountFilter struct {
	UserID string
	Type   domain.AccountType
	Symbol string
	Status domain.Status
}

// AccountRepo stores users' margin accounts.
type AccountRepo interface {
	// Ensure creates the account (NORMAL) unless it exists and returns it.
	Ensure(ctx context.Context, userID string, a domain.Account, at time.Time) (Account, error)
	// Get returns the account; ok is false when the user never opened it.
	Get(ctx context.Context, userID string, a domain.Account) (Account, bool, error)
	// OfUser returns the user's accounts.
	OfUser(ctx context.Context, userID string) ([]Account, error)
	Update(ctx context.Context, a Account) error
	// WithDebt returns the accounts that owe anything, or that are not
	// NORMAL (warned, liquidating or frozen).
	WithDebt(ctx context.Context) ([]Account, error)
	// List returns at most limit accounts the filter matches, oldest
	// first.
	List(ctx context.Context, f AccountFilter, limit int) ([]Account, error)
	// IsolatedCounts returns how many isolated accounts each pair has.
	IsolatedCounts(ctx context.Context) (map[string]int, error)
}

// Loan is what an account owes of an asset.
type Loan struct {
	UserID    string
	Account   domain.Account
	Asset     string
	Principal decimal.Decimal
	Interest  decimal.Decimal
	// OpenedAt is when it last went from owing nothing to owing.
	OpenedAt  time.Time
	UpdatedAt time.Time
}

// LoanTotal is what users owe of an asset.
type LoanTotal struct {
	Principal decimal.Decimal
	Interest  decimal.Decimal
	// Borrowers counts the accounts that owe it.
	Borrowers int
}

// LoanRepo stores the loans.
type LoanRepo interface {
	// Get returns the loan, zero when there is none.
	Get(ctx context.Context, userID string, a domain.Account, asset string) (Loan, error)
	// Add changes a loan's principal and interest by the deltas.
	Add(ctx context.Context, userID string, a domain.Account, asset string, principal, interest decimal.Decimal, at time.Time) error
	// OfUser returns the user's loans with anything owed.
	OfUser(ctx context.Context, userID string) ([]Loan, error)
	// UserOwed is the principal the user owes of asset in all accounts.
	UserOwed(ctx context.Context, userID, asset string) (decimal.Decimal, error)
	// Open returns every loan with anything owed, of one asset when asset
	// is set.
	Open(ctx context.Context, asset string) ([]Loan, error)
	// Totals returns what users owe of each asset.
	Totals(ctx context.Context) (map[string]LoanTotal, error)
	// Changes returns an account's latest borrows, repayments and
	// interest charges, newest first.
	Changes(ctx context.Context, userID string, a domain.Account, limit int) ([]LoanChange, error)
}

// LoanChange is a borrow, a repayment or an interest charge of an account
// (the view loan_changes).
type LoanChange struct {
	ID            string
	Asset         string
	Kind          string
	Status        Op
	Amount        decimal.Decimal
	PrincipalPart decimal.Decimal
	InterestPart  decimal.Decimal
	// Reason is USER, AUTO_BORROW, AUTO_REPAY or LIQUIDATION; "" for a
	// charge.
	Reason        string
	OrderID       string
	LiquidationID string
	// JournalKey is the ledger journal's idempotency key.
	JournalKey string
	CreatedAt  time.Time
}

// Kinds of a loan change.
const (
	ChangeBorrow   = "BORROW"
	ChangeRepay    = "REPAY"
	ChangeInterest = "INTEREST"
)

// Op is a borrow, repayment or transfer's progress.
type Op string

// Progress of the writes that go to the ledger.
const (
	OpPending Op = "PENDING"
	OpDone    Op = "DONE"
	OpFailed  Op = "FAILED"
)

// Borrow is a loan taken.
type Borrow struct {
	ID            string
	UserID        string
	Account       domain.Account
	Asset         string
	Amount        decimal.Decimal
	Model         domain.InterestModel
	Rate          decimal.Decimal
	FirstInterest decimal.Decimal
	OrderID       string
	IdemKey       string
	RequestHash   []byte
	Status        Op
	Failure       string
	CreatedAt     time.Time
	DoneAt        time.Time
}

// BorrowRepo stores the borrows.
type BorrowRepo interface {
	Insert(ctx context.Context, b Borrow) error
	// ByKey returns the user's borrow made under idemKey; ok is false
	// without one.
	ByKey(ctx context.Context, userID, idemKey string) (Borrow, bool, error)
	// GetForUpdate returns a borrow with its row locked.
	GetForUpdate(ctx context.Context, id string) (Borrow, error)
	// Finish stores the outcome of a PENDING borrow.
	Finish(ctx context.Context, b Borrow) error
	// Pending returns the borrows created before cutoff still PENDING.
	Pending(ctx context.Context, cutoff time.Time, limit int) ([]Borrow, error)
	// PendingOf returns the user's PENDING borrows.
	PendingOf(ctx context.Context, userID string) ([]Borrow, error)
}

// Repay is a repayment.
type Repay struct {
	ID            string
	UserID        string
	Account       domain.Account
	Asset         string
	Interest      decimal.Decimal
	Principal     decimal.Decimal
	Reason        string
	OrderID       string
	LiquidationID string
	IdemKey       string
	RequestHash   []byte
	Status        Op
	Failure       string
	CreatedAt     time.Time
	DoneAt        time.Time
}

// Reasons of a repayment.
const (
	RepayUser        = "USER"
	RepayAuto        = "AUTO_REPAY"
	RepayLiquidation = "LIQUIDATION"
)

// RepayRepo stores the repayments.
type RepayRepo interface {
	Insert(ctx context.Context, r Repay) error
	ByKey(ctx context.Context, userID, idemKey string) (Repay, bool, error)
	GetForUpdate(ctx context.Context, id string) (Repay, error)
	Finish(ctx context.Context, r Repay) error
	Pending(ctx context.Context, cutoff time.Time, limit int) ([]Repay, error)
}

// Charge is an hour's interest on a loan.
type Charge struct {
	ID        string
	UserID    string
	Account   domain.Account
	Asset     string
	Hour      time.Time
	Principal decimal.Decimal
	Model     domain.InterestModel
	Rate      decimal.Decimal
	Interest  decimal.Decimal
	// BorrowID is set on a borrow's first hour.
	BorrowID string
	Status   Op
	// JournalKey is the ledger journal that booked an hourly charge.
	JournalKey string
	CreatedAt  time.Time
	DoneAt     time.Time
}

// LoanSince is what changed a loan's principal after an instant.
type LoanSince struct {
	Borrowed decimal.Decimal
	Repaid   decimal.Decimal
	// Unsettled is set while a borrow or repayment made before the
	// instant is still PENDING: the principal on it is not known yet.
	Unsettled bool
}

// InterestRepo stores the interest charges.
type InterestRepo interface {
	// Insert stores a charge; an hourly charge stored before for the same
	// loan and hour is left, and ok is false.
	Insert(ctx context.Context, c Charge) (ok bool, err error)
	// Hourly returns the charge of a loan's hour; ok is false without one.
	Hourly(ctx context.Context, userID string, a domain.Account, asset string, hour time.Time) (Charge, bool, error)
	GetForUpdate(ctx context.Context, id string) (Charge, error)
	Finish(ctx context.Context, c Charge) error
	// Pending returns the charges created before cutoff still PENDING.
	Pending(ctx context.Context, cutoff time.Time, limit int) ([]Charge, error)
	// PendingOf returns the user's PENDING charges.
	PendingOf(ctx context.Context, userID string) ([]Charge, error)
	// LockHour serializes the transactions that store an asset's hourly
	// charges of an hour (a transaction-scoped advisory lock).
	LockHour(ctx context.Context, asset string, hour time.Time) error
	// OfHour returns the hourly charges of an asset and hour, in the
	// order of their accounts (user, account type, pair).
	OfHour(ctx context.Context, asset string, hour time.Time) ([]Charge, error)
	// Since returns, per loan with borrows or repayments at or after the
	// instant, what they changed (DONE ones) and whether any made before
	// it is still PENDING.
	Since(ctx context.Context, at time.Time) (map[LoanKey]LoanSince, error)
	// OfUser returns a page of the user's DONE charges, newest first,
	// optionally of one account and asset; before is the previous page's
	// last interest ID.
	OfUser(ctx context.Context, userID string, a *domain.Account, asset, before string, limit int) ([]Charge, error)
	// OfAccount returns an account's latest charges, PENDING ones too,
	// newest first.
	OfAccount(ctx context.Context, userID string, a domain.Account, limit int) ([]Charge, error)
	// Run returns the state of an hour's run; ok is false before it
	// started. StartRun records it RUNNING unless it exists.
	Run(ctx context.Context, hour time.Time) (status string, ok bool, err error)
	StartRun(ctx context.Context, hour time.Time) error
	FinishRun(ctx context.Context, hour time.Time, loans int) error
	// LastDone returns the latest finished hour, FirstRunning the earliest
	// unfinished one; ok is false without one.
	LastDone(ctx context.Context) (hour time.Time, ok bool, err error)
	FirstRunning(ctx context.Context) (hour time.Time, ok bool, err error)
}

// LoanKey names a loan.
type LoanKey struct {
	UserID  string
	Account domain.Account
	Asset   string
}

// Transfer moves an asset between SPOT and a margin account.
type Transfer struct {
	ID          string
	UserID      string
	Direction   domain.Direction
	Account     domain.Account
	Asset       string
	Amount      decimal.Decimal
	IdemKey     string
	RequestHash []byte
	Status      Op
	Failure     string
	CreatedAt   time.Time
	DoneAt      time.Time
}

// TransferRepo stores the transfers.
type TransferRepo interface {
	Insert(ctx context.Context, t Transfer) error
	ByKey(ctx context.Context, userID, idemKey string) (Transfer, bool, error)
	GetForUpdate(ctx context.Context, id string) (Transfer, error)
	Finish(ctx context.Context, t Transfer) error
	Pending(ctx context.Context, cutoff time.Time, limit int) ([]Transfer, error)
	// PendingOf returns the user's PENDING transfers.
	PendingOf(ctx context.Context, userID string) ([]Transfer, error)
}

// Reservation is what ReserveOrder answered for an order.
type Reservation struct {
	OrderID     string
	UserID      string
	AccountType domain.AccountType
	// Symbol is the order's pair (an isolated account's own).
	Symbol      string
	SideEffect  domain.SideEffect
	Borrowed    decimal.Decimal
	BorrowID    string
	MarginLevel *decimal.Decimal
	CreatedAt   time.Time
}

// ReservationRepo stores the reservations of orders.
type ReservationRepo interface {
	// Get returns an order's reservation; ok is false without one.
	Get(ctx context.Context, orderID string) (Reservation, bool, error)
	// Insert stores a reservation unless the order has one, and returns
	// the one that counts.
	Insert(ctx context.Context, r Reservation) (Reservation, error)
}

// RunRepo records the reconciliation runs.
type RunRepo interface {
	Record(ctx context.Context, started time.Time, check string, mismatches int, details []byte) error
}

// Move is one step of a margin posting in the ledger.
type Move struct {
	Type   domain.MoveType
	Asset  string
	Amount decimal.Decimal
	// Interest is the part of a REPAY that pays interest.
	Interest decimal.Decimal
}

// Accrual is one account's interest of an hour.
type Accrual struct {
	UserID  string
	Account domain.Account
	Amount  decimal.Decimal
}

// Posting is what the ledger books for one margin operation, all of it
// or nothing, once per key.
type Posting struct {
	IdemKey   string
	UserID    string
	Account   domain.Account
	Reference string
	Moves     []Move
}

// Ledger is ledger-service, which holds the margin accounts' balances and
// debts (ADR-0001).
type Ledger interface {
	// Post books a margin posting and returns the journal of each move
	// ("" for one that moved nothing); a repeated key returns the first
	// result. A refusal (insufficient funds, a debt overpaid) is an
	// apperr of kind Unprocessable or Invalid and books nothing.
	Post(ctx context.Context, p Posting) ([]string, error)
	// Accrue books an hour's interest of one asset on many accounts in one
	// journal (key margin-interest:<key>) and returns it; a repeat replays.
	Accrue(ctx context.Context, key, asset, reference string, lines []Accrual) (string, error)
	// Holdings returns what each of the user's margin accounts holds and
	// owes, per asset.
	Holdings(ctx context.Context, userID string) (map[domain.Account][]domain.Holding, error)
	// Debts returns what every margin account owes, per asset (invariant
	// 7).
	Debts(ctx context.Context) ([]Debt, error)
}

// Debt is what one margin account owes of an asset, in the ledger.
type Debt struct {
	UserID    string
	Account   domain.Account
	Asset     string
	Principal decimal.Decimal
	Interest  decimal.Decimal
}

// Prices gives the assets' values in USDT, refreshed in the background.
type Prices interface {
	Prices() domain.Prices
}

// PairInfo is what margin-service needs of a trading pair.
type PairInfo struct {
	Symbol string
	Base   string
	Quote  string
	Status string
	// TickDecimals is the price's precision.
	TickDecimals int32
}

// Instruments reads assets and pairs (instrument-service gRPC).
type Instruments interface {
	// Decimals returns an asset's precision; unknown assets fail with
	// COMMON_NOT_FOUND.
	Decimals(ctx context.Context, asset string) (int32, error)
	Pair(ctx context.Context, symbol string) (PairInfo, error)
}

// Eligibility asks user-service whether a user may use a feature.
type Eligibility interface {
	Check(ctx context.Context, userID, feature, symbol string) (allowed bool, reason string, err error)
}

// Features answers feature-flag checks.
type Features interface {
	Enabled(key string, s flags.Subject) bool
}
