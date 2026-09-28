// Package ports declares what wallet-service's application layer needs.
package ports

import (
	"context"
	"math/big"
	"time"

	"github.com/shopspring/decimal"
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
	Commands() CommandRepo
	Sweeps() SweepRepo
	ChainFees() ChainFeeRepo
	Fundings() FundingRepo
	Checks() CheckRepo
	WithdrawAddresses() WithdrawAddressRepo
	Withdrawals() WithdrawalRepo
	Attempts() AttemptRepo
	Nonces() NonceRepo
	Prices() PriceRepo
	// Emit queues a wallet.deposit.events event keyed by the user.
	Emit(ctx context.Context, msg proto.Message, userID string) error
	// EmitWithdrawal queues a wallet.withdrawal.events event keyed by the
	// user.
	EmitWithdrawal(ctx context.Context, msg proto.Message, userID string) error
	// Audit queues an operator action on audit.events.
	Audit(ctx context.Context, msg proto.Message, actor string) error
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
	// List returns the addresses of network by index.
	List(ctx context.Context, network string) ([]domain.Address, error)
}

// WithdrawAddressRepo stores users' withdrawal address books.
type WithdrawAddressRepo interface {
	Insert(ctx context.Context, a domain.WithdrawAddress) error
	// List returns a user's active entries, newest first.
	List(ctx context.Context, userID string) ([]domain.WithdrawAddress, error)
	// Find returns the user's active entry of an address on network, or
	// nil.
	Find(ctx context.Context, userID, network, address string) (*domain.WithdrawAddress, error)
	// Delete removes a user's entry and reports whether there was one.
	Delete(ctx context.Context, userID, id string, now time.Time) (bool, error)
}

// WithdrawalRepo stores withdrawals.
type WithdrawalRepo interface {
	Insert(ctx context.Context, w domain.Withdrawal) error
	Update(ctx context.Context, w domain.Withdrawal) error
	// Get returns a withdrawal, or nil.
	Get(ctx context.Context, id string) (*domain.Withdrawal, error)
	// GetForUpdate is Get with the row locked.
	GetForUpdate(ctx context.Context, id string) (*domain.Withdrawal, error)
	// ByUser returns up to limit of a user's withdrawals older than before
	// ("": newest), newest first.
	ByUser(ctx context.Context, userID, before string, limit int) ([]domain.Withdrawal, error)
	// ByStatus lists the network's withdrawals in the statuses, oldest
	// first.
	ByStatus(ctx context.Context, network string, statuses ...string) ([]domain.Withdrawal, error)
	// Unreleased lists the refused withdrawals whose funds are not released.
	Unreleased(ctx context.Context, network string) ([]domain.Withdrawal, error)
	// Unsettled lists the broadcast withdrawals the ledger has not settled.
	Unsettled(ctx context.Context, network string) ([]domain.Withdrawal, error)
	// ValueSince sums the USDT worth of a user's withdrawals created since
	// t, refused ones left out.
	ValueSince(ctx context.Context, userID string, t time.Time) (decimal.Decimal, error)
}

// AttemptRepo stores the signed transactions of withdrawals.
type AttemptRepo interface {
	Insert(ctx context.Context, a domain.Attempt) error
	// Of lists a withdrawal's attempts, newest first.
	Of(ctx context.Context, withdrawalID string) ([]domain.Attempt, error)
}

// NonceRepo tracks the next nonce of the hot wallet. A nonce is taken
// only once its transaction is signed, so a failed signature leaves no
// gap that would hold up every later transaction.
type NonceRepo interface {
	// Peek returns the next nonce recorded for address, 0 when none.
	Peek(ctx context.Context, address string) (uint64, error)
	// Advance records that nonces below next are taken.
	Advance(ctx context.Context, address string, next uint64) error
}

// PriceRepo keeps the day's USDT prices for the limits (§11.6: a snapshot
// at 00:00 UTC).
type PriceRepo interface {
	// Get returns the price of asset on day, or nil.
	Get(ctx context.Context, day time.Time, asset string) (*decimal.Decimal, error)
	// Put records the day's price unless one is there; it returns the
	// price that holds.
	Put(ctx context.Context, day time.Time, asset string, price decimal.Decimal, source string) (decimal.Decimal, error)
}

// CommandRepo queues operator commands.
type CommandRepo interface {
	Insert(ctx context.Context, c domain.Command) error
	// Pending lists the network's pending commands, oldest first.
	Pending(ctx context.Context, network string) ([]domain.Command, error)
	Update(ctx context.Context, c domain.Command) error
	// Recent returns the latest commands, newest first.
	Recent(ctx context.Context, limit int) ([]domain.Command, error)
}

// SweepRepo stores sweeps.
type SweepRepo interface {
	Insert(ctx context.Context, s domain.Sweep) error
	Update(ctx context.Context, s domain.Sweep) error
	// Open lists the network's sweeps still waiting for their receipt.
	Open(ctx context.Context, network string) ([]domain.Sweep, error)
}

// ChainFeeRepo stores the gas of mined platform transactions.
type ChainFeeRepo interface {
	// Insert records a fee once per transaction.
	Insert(ctx context.Context, f domain.ChainFee) error
	// Unbooked lists the network's fees the ledger has not booked yet.
	Unbooked(ctx context.Context, network string) ([]domain.ChainFee, error)
	MarkBooked(ctx context.Context, txHash, journalID string) error
}

// FundingRepo stores platform fundings, one per transaction.
type FundingRepo interface {
	Get(ctx context.Context, txHash string) (*domain.Funding, error)
	Insert(ctx context.Context, f domain.Funding) error
}

// CheckRepo keeps the chain checks.
type CheckRepo interface {
	Insert(ctx context.Context, c domain.ChainCheck) error
	// Latest returns the latest check of each asset of network.
	Latest(ctx context.Context, network string) ([]domain.ChainCheck, error)
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

// Receipt is the outcome of a mined transaction.
type Receipt struct {
	Succeeded         bool
	BlockNumber       uint64
	BlockHash         string
	GasUsed           uint64
	EffectiveGasPrice *big.Int
}

// Tx is a transaction looked up by hash; BlockNumber is 0 while pending.
type Tx struct {
	From        string
	To          string
	Value       *big.Int
	BlockNumber uint64
}

// Transactor sends the platform's transactions and reads accounts.
type Transactor interface {
	Head(ctx context.Context) (uint64, error)
	Balance(ctx context.Context, address string) (*big.Int, error)
	PendingNonce(ctx context.Context, address string) (uint64, error)
	// Fees returns the latest base fee and a suggested priority fee.
	Fees(ctx context.Context) (baseFee, tip *big.Int, err error)
	// SendRaw broadcasts a signed transaction.
	SendRaw(ctx context.Context, raw string) error
	// Receipt returns a mined transaction's receipt, nil while pending or
	// unknown.
	Receipt(ctx context.Context, txHash string) (*Receipt, error)
	// Transaction returns a transaction, nil when the node does not know
	// it.
	Transaction(ctx context.Context, txHash string) (*Tx, error)
}

// SignRequest is a transaction for the signer.
type SignRequest struct {
	ID         string
	Purpose    string // WITHDRAWAL or SWEEP
	Reference  string
	ApprovedBy string
	ChainID    uint64
	Index      uint32
	Nonce      uint64
	To         string
	Value      *big.Int
	GasLimit   uint64
	MaxFee     *big.Int
	MaxTip     *big.Int
}

// Signed is a signed transaction.
type Signed struct {
	Raw    string
	TxHash string
	From   string
}

// Signer signs the platform's transactions (the signer service).
type Signer interface {
	HotWallet(ctx context.Context) (string, error)
	Sign(ctx context.Context, r SignRequest) (Signed, error)
}

// StepUp is a redeemed step-up with the user's security context.
type StepUp struct {
	Channel         string
	DeviceID        string
	DeviceFirstSeen time.Time
	Identities      int
	TOTPEnabled     bool
	IdentityChanged time.Time
	PasswordChanged time.Time
}

// StepUps redeems step-up tokens (auth-service).
type StepUps interface {
	Consume(ctx context.Context, userID, token string) (StepUp, error)
}

// Profiles reads accounts (user-service).
type Profiles interface {
	// Created returns when the account was created.
	Created(ctx context.Context, userID string) (time.Time, error)
}

// Prices quotes assets in USDT (market-data-service's reference prices).
type Prices interface {
	// USDT returns asset's price in USDT and its source.
	USDT(ctx context.Context, asset string) (decimal.Decimal, string, error)
}

// Ledger books the wallet's journals (ledger-service).
type Ledger interface {
	// Freeze and Unfreeze move a withdrawal's amount and fee between the
	// user's available and frozen SPOT balance (WITHDRAW_FREEZE,
	// WITHDRAW_UNFREEZE).
	Freeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) (journalID string, err error)
	Unfreeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) (journalID string, err error)
	// Settle books a broadcast withdrawal (WITHDRAW_SETTLE).
	Settle(ctx context.Context, key, userID, asset string, amount, fee decimal.Decimal, reference string) (journalID string, err error)
	// TransferInternal completes a withdrawal to another user
	// (INTERNAL_TRANSFER).
	TransferInternal(ctx context.Context, key, fromUser, toUser, asset string, amount decimal.Decimal, reference string) (journalID string, err error)
	// BookChainFee books gas the platform paid; key makes it once.
	BookChainFee(ctx context.Context, key, asset string, amount decimal.Decimal, reference string) (journalID string, err error)
	// Fund books a platform funding to a system account.
	Fund(ctx context.Context, key, accountType, asset string, amount decimal.Decimal, reference string) (journalID string, err error)
	// SystemBalances returns the available balance of each system account
	// in asset.
	SystemBalances(ctx context.Context, asset string) (map[string]decimal.Decimal, error)
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
