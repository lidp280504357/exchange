// Package ports declares what wallet-service's application layer needs.
package ports

import (
	"context"
	"math/big"

	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

// Store is the unit of work over the wallet schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Addresses() AddressRepo
	Deposits() DepositRepo
	Blocks() BlockRepo
	// Emit queues a wallet.deposit.events event keyed by the user.
	Emit(ctx context.Context, msg proto.Message, userID string) error
}

// AddressRepo stores deposit addresses, one per user and network.
type AddressRepo interface {
	// Get returns the user's address on network, or nil.
	Get(ctx context.Context, userID, network string) (*domain.Address, error)
	// NextIndex reserves the next derivation index of network.
	NextIndex(ctx context.Context, network string) (uint32, error)
	Insert(ctx context.Context, a domain.Address) error
	// Owners maps the lower-case addresses of network to their users.
	Owners(ctx context.Context, network string) (map[string]string, error)
}

// DepositRepo stores deposits; (network, tx hash, log index) is unique.
type DepositRepo interface {
	Insert(ctx context.Context, d domain.Deposit) error
	Update(ctx context.Context, d domain.Deposit) error
	// Find returns the deposit of a transfer, or nil.
	Find(ctx context.Context, network, txHash string, logIndex int64) (*domain.Deposit, error)
	// GetForUpdate returns a deposit locked, or nil.
	GetForUpdate(ctx context.Context, id string) (*domain.Deposit, error)
	// Pending lists the deposits of network waiting for confirmations.
	Pending(ctx context.Context, network string) ([]domain.Deposit, error)
	// Unrequested lists the CONFIRMED deposits of network not yet sent to
	// the ledger.
	Unrequested(ctx context.Context, network string) ([]domain.Deposit, error)
	// FromBlock lists the pending deposits of network in blocks at or
	// above n (what a reorganization may drop).
	FromBlock(ctx context.Context, network string, n uint64) ([]domain.Deposit, error)
	// ByUser returns up to limit of a user's deposits older than before
	// ("": newest), newest first.
	ByUser(ctx context.Context, userID, before string, limit int) ([]domain.Deposit, error)
}

// BlockRepo keeps the scan cursor and recent block hashes per network.
type BlockRepo interface {
	// Cursor returns the last scanned block, 0 when none.
	Cursor(ctx context.Context, network string) (uint64, error)
	// Hash returns the hash stored for block n, "" when not kept.
	Hash(ctx context.Context, network string, n uint64) (string, error)
	// Save records block n as scanned and moves the cursor to it.
	Save(ctx context.Context, network string, n uint64, hash string) error
	// Rewind forgets blocks from n on; the cursor goes back to n-1.
	Rewind(ctx context.Context, network string, n uint64) error
	// Prune forgets the hashes of blocks below n.
	Prune(ctx context.Context, network string, below uint64) error
}

// Transfer is a value transfer to a watched address: the chain's coin
// (LogIndex domain.NativeLog, Contract "") or a token's Transfer log.
type Transfer struct {
	BlockNumber uint64
	BlockHash   string
	TxHash      string
	LogIndex    int64
	To          string // lower-case
	Contract    string // lower-case
	Amount      *big.Int
}

// Block is a scanned block with the coin transfers in it.
type Block struct {
	Number     uint64
	Hash       string
	ParentHash string
	Transfers  []Transfer
}

// Chain reads an EVM network.
type Chain interface {
	Head(ctx context.Context) (uint64, error)
	// Block returns block n with its successful coin transfers to the
	// watched (lower-case) addresses.
	Block(ctx context.Context, n uint64, watched map[string]string) (Block, error)
	// TokenTransfers returns the ERC-20 Transfer logs of any contract to
	// the watched addresses in blocks from..to.
	TokenTransfers(ctx context.Context, from, to uint64, watched []string) ([]Transfer, error)
	// Decimals returns a token's decimals, or the coin's for "".
	Decimals(ctx context.Context, contract string) (int32, error)
}

// Networks reads deposit networks (instrument-service).
type Networks interface {
	// Network returns an asset's network; unknown ones fail with
	// domain.ErrUnknownNetwork.
	Network(ctx context.Context, asset, network string) (domain.Network, error)
	// OnNetwork lists the assets of a network that take deposits.
	OnNetwork(ctx context.Context, network string) ([]domain.Network, error)
}

// Eligibility asks user-service whether a user may use a feature now.
type Eligibility interface {
	Check(ctx context.Context, userID, feature string) (allowed bool, reason string, err error)
}

// Deriver derives deposit addresses from the deposit account's xpub.
type Deriver interface {
	Address(index uint32) (string, error)
}
