// Package ports declares what ledger-service's application layer needs.
package ports

import (
	"context"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/flags"
)

// Store is the unit of work over the ledger schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Accounts() AccountRepo
	Journals() JournalRepo
	Transfers() TransferRepo
	Trades() TradeRepo
	Futures() FuturesRepo
	Holds() HoldRepo
	Settings() SettingsRepo
	Margins() MarginRepo
	// Emit queues an event on topic, keyed by aggregateID.
	Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error
}

// AccountRepo stores accounts and their running balances.
type AccountRepo interface {
	// Lock returns the accounts for keys, creating missing ones at zero,
	// each locked for the rest of the transaction; keys come in lock order.
	Lock(ctx context.Context, keys []domain.AccountKey) ([]domain.Account, error)
	// Save stores new balances and version.
	Save(ctx context.Context, a domain.Account) error
	// ByOwner lists an owner's accounts, optionally of one type.
	ByOwner(ctx context.Context, ownerID, accountType string) ([]domain.Account, error)
	// Margin lists the rows of a user's margin accounts.
	Margin(ctx context.Context, userID string) ([]domain.Account, error)
	// MarginDebts lists every margin debt and interest row that owes
	// something.
	MarginDebts(ctx context.Context) ([]domain.Account, error)
}

// MarginRepo stores the margin postings of margin-service.
type MarginRepo interface {
	// ByKey returns the posting booked under key, or nil.
	ByKey(ctx context.Context, key string) (*domain.MarginPosting, error)
	Insert(ctx context.Context, p domain.MarginPosting) error
}

// JournalRepo stores journals and their lines; both are append-only.
type JournalRepo interface {
	// ByIdemKey returns the journal posted under key, or nil.
	ByIdemKey(ctx context.Context, key string) (*domain.Journal, error)
	// Insert writes a journal with its lines and returns its sequence.
	Insert(ctx context.Context, j domain.Journal, lines []domain.PostedLine) (int64, error)
	// Entries returns up to limit lines of the owner's accounts older than
	// beforeID (0: newest), newest first, optionally of one asset and
	// entry type.
	Entries(ctx context.Context, ownerID, asset, entryType string, beforeID int64, limit int) ([]domain.Entry, error)
	// KeyedTotal sums what the journals whose key starts with prefix moved
	// on an account (a custody reset's DEPOSIT_PENDING, for one).
	KeyedTotal(ctx context.Context, prefix string, account domain.AccountKey) (decimal.Decimal, error)
	// Payee returns the user whose account the journal paid ("" when it
	// paid no user's).
	Payee(ctx context.Context, journalID string) (string, error)
}

// TransferRepo stores account transfers.
type TransferRepo interface {
	// ByKey returns the user's transfer created under idemKey, or nil.
	ByKey(ctx context.Context, userID, idemKey string) (*domain.Transfer, error)
	Insert(ctx context.Context, t domain.Transfer) error
	// List returns up to limit transfers older than beforeID ("": newest).
	List(ctx context.Context, userID, beforeID string, limit int) ([]domain.Transfer, error)
}

// TradeRepo records the engine trades the ledger settled or parked.
type TradeRepo interface {
	// Statuses returns the status of each known trade among ids.
	Statuses(ctx context.Context, ids []string) (map[string]string, error)
	// Insert records a trade seen for the first time.
	Insert(ctx context.Context, t domain.Trade) error
	// Update stores the status, error, attempts and settlement time of a
	// recorded trade.
	Update(ctx context.Context, t domain.Trade) error
	// List returns up to limit trades, newest first, optionally of one
	// status.
	List(ctx context.Context, status string, limit int) ([]domain.Trade, error)
}

// FuturesRepo stores the settlement requests of derivatives-service.
type FuturesRepo interface {
	// ByKey returns the request booked under key, or nil.
	ByKey(ctx context.Context, key string) (*domain.FuturesSettlement, error)
	Insert(ctx context.Context, s domain.FuturesSettlement) error
}

// HoldRepo stores administrators' holds.
type HoldRepo interface {
	// Get returns a hold, or nil.
	Get(ctx context.Context, id string) (*domain.Hold, error)
	// GetForUpdate is Get with the row locked.
	GetForUpdate(ctx context.Context, id string) (*domain.Hold, error)
	Insert(ctx context.Context, h domain.Hold) error
	// Release stores a hold's release; one released already fails with
	// ErrHoldReleased.
	Release(ctx context.Context, h domain.Hold) error
	// OfUser returns a user's holds, newest first.
	OfUser(ctx context.Context, userID string, activeOnly bool, limit int) ([]domain.Hold, error)
	// OthersActive is what h's user's other active holds on h's account
	// and asset add up to.
	OthersActive(ctx context.Context, h domain.Hold) (decimal.Decimal, error)
}

// SettingsRepo stores the settings operators change (design 2026-10-04
// §4.2).
type SettingsRepo interface {
	// WelcomeCredits returns the welcome credits, nil before a first value.
	WelcomeCredits(ctx context.Context) (*domain.WelcomeCredits, error)
	// WelcomeCreditsForUpdate is WelcomeCredits with the row locked.
	WelcomeCreditsForUpdate(ctx context.Context) (*domain.WelcomeCredits, error)
	// SaveWelcomeCredits stores a value, the first or a change.
	SaveWelcomeCredits(ctx context.Context, w domain.WelcomeCredits) error
	// SeedWelcomeCredits stores w unless there is a value; it reports
	// whether it stored it.
	SeedWelcomeCredits(ctx context.Context, w domain.WelcomeCredits) (bool, error)
}

// Assets tells the precision of an asset (instrument-service); unknown
// assets fail with COMMON_NOT_FOUND.
type Assets interface {
	Decimals(ctx context.Context, asset string) (int32, error)
}

// Eligibility asks user-service whether a user may use a feature now.
type Eligibility interface {
	Check(ctx context.Context, userID, feature string) (allowed bool, reason string, err error)
}

// Futures is derivatives-service: the unrealized profit and loss of a
// user's cross positions, which transfers out of FUTURES must leave
// covered.
type Futures interface {
	CrossUnrealizedPnL(ctx context.Context, userID, asset string) (decimal.Decimal, error)
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
	// Closed reports a product line an operator closed (design
	// 2026-10-07, product switches).
	Closed(key string) bool
}

// ReconciliationRuns reads the recorded runs of the invariant checks.
type ReconciliationRuns interface {
	// ReconciliationRuns returns the latest run of each check and up to
	// failures runs that found mismatches, newest first.
	ReconciliationRuns(ctx context.Context, failures int) (latest, failing []domain.ReconciliationRun, err error)
}
