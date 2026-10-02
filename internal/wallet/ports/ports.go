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
	Callbacks() CallbackRepo
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
	// Lock holds the user's first address request on network for the
	// transaction: a custodian's address is created only once.
	Lock(ctx context.Context, userID, network string) error
	// Owner returns the user of an address on network, "" when none.
	Owner(ctx context.Context, network, address string) (string, error)
	// Count counts the addresses on the networks.
	Count(ctx context.Context, networks []string) (int, error)
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

// CallbackFilter selects callbacks for the admin console; empty fields
// match everything. Query matches a trade ID, withdrawal ID, transaction
// hash or address.
type CallbackFilter struct {
	Result string
	Kind   string
	Query  string
	After  string
	Limit  int
}

// CallbackRepo keeps the custodian's callbacks.
type CallbackRepo interface {
	// Receive records a callback. A verified one is kept once per
	// provider, trade and status: a repeat counts one more attempt and
	// returns the stored callback with its result (fresh false).
	Receive(ctx context.Context, c domain.Callback) (stored domain.Callback, fresh bool, err error)
	// Finish records what became of a callback.
	Finish(ctx context.Context, id, result, detail string, at time.Time) error
	// Get returns a callback, or nil.
	Get(ctx context.Context, id string) (*domain.Callback, error)
	// Page returns up to f.Limit callbacks matching f, newest first,
	// after the one with ID f.After.
	Page(ctx context.Context, f CallbackFilter) ([]domain.Callback, error)
	// Attention counts the verified callbacks that failed or found
	// nothing to apply to, and returns when the last one arrived.
	Attention(ctx context.Context) (int, time.Time, error)
	// RejectedSince counts the refused callbacks kept since t.
	RejectedSince(ctx context.Context, t time.Time) (int, error)
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
	// Submitted lists the withdrawals with the custodian provider, oldest
	// first.
	Submitted(ctx context.Context, provider string) ([]domain.Withdrawal, error)
	// Outstanding lists the custodian provider's withdrawals the ledger
	// has not settled: SUBMITTED, or CONFIRMED and not booked yet.
	Outstanding(ctx context.Context, provider string) ([]domain.Withdrawal, error)
	// Page returns up to f.Limit of the network's withdrawals ("": every
	// network's) matching f, after the one with ID f.After in f's order
	// ("": from the start).
	Page(ctx context.Context, network string, f WithdrawalFilter) ([]domain.Withdrawal, error)
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
	// Unbooked lists the network's fees the ledger has not booked yet and
	// may (neither held for a person nor written off).
	Unbooked(ctx context.Context, network string) ([]domain.ChainFee, error)
	MarkBooked(ctx context.Context, txHash, journalID string) error
	// Held lists the fees waiting for a person, oldest first.
	Held(ctx context.Context) ([]domain.ChainFee, error)
	// OfReference returns the fees of a withdrawal or sweep, oldest first.
	OfReference(ctx context.Context, reference string) ([]domain.ChainFee, error)
	// Resolve stores a person's decision on a held fee (its status,
	// asset, amount and who decided why) if it is still held; false when
	// it is not.
	Resolve(ctx context.Context, f domain.ChainFee) (bool, error)
	// Unit returns the confirmed fee unit of a custodian's network, or nil.
	Unit(ctx context.Context, provider, asset, network string) (*domain.FeeUnit, error)
	// PutUnit records a fee unit, replacing the network's earlier one.
	PutUnit(ctx context.Context, u domain.FeeUnit) error
	// Units lists the confirmed fee units.
	Units(ctx context.Context) ([]domain.FeeUnit, error)
}

// FundingRepo stores platform fundings, one per transaction.
type FundingRepo interface {
	Get(ctx context.Context, txHash string) (*domain.Funding, error)
	Insert(ctx context.Context, f domain.Funding) error
}

// CheckRepo keeps the chain checks.
type CheckRepo interface {
	Insert(ctx context.Context, c domain.ChainCheck) error
	// Latest returns the latest check of each asset of network (of every
	// holder when network is empty).
	Latest(ctx context.Context, network string) ([]domain.ChainCheck, error)
}

// DepositRepo stores deposits; (network, tx hash, log index) is unique.
type DepositRepo interface {
	Insert(ctx context.Context, d domain.Deposit) error
	Update(ctx context.Context, d domain.Deposit) error
	// Find returns the deposit of a transfer, or nil.
	Find(ctx context.Context, network, txHash string, logIndex int64) (*domain.Deposit, error)
	// ByProviderTx returns the deposit a custodian reported, or nil.
	ByProviderTx(ctx context.Context, providerTxID string) (*domain.Deposit, error)
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
	// ByTransfer returns a deposit of the transfer txHash to address on
	// network, whatever its source, or nil.
	ByTransfer(ctx context.Context, network, txHash, address string) (*domain.Deposit, error)
	// Get returns a deposit, or nil.
	Get(ctx context.Context, id string) (*domain.Deposit, error)
	// Page returns up to f.Limit deposits matching f, newest first.
	Page(ctx context.Context, f DepositFilter) ([]domain.Deposit, error)
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
	// TOTPChanged is when the authenticator app was last removed.
	TOTPChanged time.Time
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
	// ReleaseUnclaimed books an unclaimed deposit, booked to
	// UNCLAIMED_DEPOSIT, to its user (DEPOSIT_CREDIT) for an administrator
	// (actor); the deposit ID keys it.
	ReleaseUnclaimed(ctx context.Context, depositID, userID, asset string, amount decimal.Decimal, actor, reason string) (journalID string, err error)
}

// CustodyCoin is one of the custodian's coins with what it holds of it.
type CustodyCoin struct {
	// Code is the custodian's "mainCoinType:coinType", networks'
	// provider_coin.
	Code     string
	Symbol   string
	Decimals int32
	Token    bool
	// Balance is nil when the custodian did not report one.
	Balance *decimal.Decimal
}

// CustodyTrade is a verified callback: a deposit to one of the platform's
// addresses, or news of a withdrawal (BusinessID: its ID).
type CustodyTrade struct {
	TradeID string
	Kind    string // domain.CallbackDeposit or domain.CallbackWithdrawal
	// Status is the custodian's code; Word what it means for a withdrawal
	// (domain.Custody*), and domain.CustodySuccess for a credited deposit.
	Status  int
	Word    string
	Coin    string
	Address string
	Memo    string
	Amount  decimal.Decimal
	Fee     decimal.Decimal
	// Decimals is how many decimals the custodian counted the amounts in.
	Decimals int32
	// RawAmount is the amount in the coin's smallest unit.
	RawAmount  decimal.Decimal
	TxHash     string
	Block      uint64
	BusinessID string
}

// Custody is a custodian's gateway (ADR-0011): it creates deposit
// addresses, takes withdrawals, checks addresses, reports its coins and
// balances, and signs the callbacks it sends.
type Custody interface {
	// Provider is the code networks name it by (domain.ProviderUdun).
	Provider() string
	// CreateAddress asks for a new deposit address of net for the user.
	CreateAddress(ctx context.Context, net domain.Network, userID string) (string, error)
	// Submit hands a withdrawal over. The withdrawal's ID makes a repeat
	// harmless; a refusal for good fails with domain.ErrCustodyRefused.
	Submit(ctx context.Context, w domain.Withdrawal, net domain.Network) error
	// CheckAddress asks whether address belongs to net's chain.
	CheckAddress(ctx context.Context, net domain.Network, address string) (bool, error)
	// Coins lists the custodian's coins with their balances.
	Coins(ctx context.Context) ([]CustodyCoin, error)
	// Parse verifies a callback as received and reads its trade; window 0
	// skips the age check (a stored callback replayed). It fails with
	// domain.ErrCallbackSignature, ErrCallbackStale or
	// ErrCallbackMalformed.
	Parse(contentType string, raw []byte, now time.Time, window time.Duration) (CustodyTrade, error)
}

// Holdings reports what an asset's other holder holds now: the custodian
// for the chain check of the platform's wallets, and the other way round.
type Holdings func(ctx context.Context, asset string) (decimal.Decimal, error)

// WithdrawalFilter selects withdrawals for the admin console; empty
// fields match everything.
type WithdrawalFilter struct {
	Status string
	UserID string
	Asset  string
	After  string
	// Oldest lists oldest first (the review queue), else newest first.
	Oldest bool
	Limit  int
	// Held is "true" for the withdrawals on hold, "false" for the others,
	// "" for both.
	Held string
	// MinValue and MaxValue bound the worth in USDT (zero: unbounded);
	// MinRisk the risk score.
	MinValue decimal.Decimal
	MaxValue decimal.Decimal
	MinRisk  int
}

// DepositFilter selects deposits for the admin console; empty fields match
// everything.
type DepositFilter struct {
	UserID  string
	Status  string
	Network string
	// Attention lists the deposits waiting for an administrator's decision
	// (domain.Deposit.Attention); ManualPending the backfilled ones whose
	// custodian callback has not come.
	Attention     bool
	ManualPending bool
	// After is the last ID of the previous page, newest first.
	After string
	Limit int
}

// Networks reads deposit networks (instrument-service).
type Networks interface {
	// Network returns an asset's network; unknown ones fail with
	// domain.ErrUnknownNetwork.
	Network(ctx context.Context, asset, network string) (domain.Network, error)
	// OnNetwork lists the assets of a network that take deposits.
	OnNetwork(ctx context.Context, network string) ([]domain.Network, error)
	// ForAsset lists an asset's networks (every network when asset is
	// empty), open or not.
	ForAsset(ctx context.Context, asset string) ([]domain.Network, error)
}

// Eligibility asks user-service whether a user may use a feature now.
type Eligibility interface {
	Check(ctx context.Context, userID, feature string) (allowed bool, reason string, err error)
}

// Deriver derives deposit addresses from the deposit account's xpub.
type Deriver interface {
	Address(index uint32) (string, error)
}
