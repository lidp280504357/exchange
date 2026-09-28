// Package ports declares what ledger-service's application layer needs.
package ports

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
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
}

// TransferRepo stores account transfers.
type TransferRepo interface {
	// ByKey returns the user's transfer created under idemKey, or nil.
	ByKey(ctx context.Context, userID, idemKey string) (*domain.Transfer, error)
	Insert(ctx context.Context, t domain.Transfer) error
	// List returns up to limit transfers older than beforeID ("": newest).
	List(ctx context.Context, userID, beforeID string, limit int) ([]domain.Transfer, error)
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

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
