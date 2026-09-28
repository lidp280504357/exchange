package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Commands an operator queues with exchangectl; the network's processor
// (the instance holding the scanner lease) carries them out, since only
// it talks to the chain and the signer.
const (
	CommandSweep     = "SWEEP"
	CommandFund      = "FUND"
	CommandReconcile = "RECONCILE"
)

// Command statuses.
const (
	CommandPending = "PENDING"
	CommandDone    = "DONE"
	CommandFailed  = "FAILED"
)

// Command is an operator's request.
type Command struct {
	ID          string
	Network     string
	Kind        string
	Args        map[string]string
	Status      string
	Result      string
	RequestedBy string
	CreatedAt   time.Time
	DoneAt      time.Time
}

// Sweep statuses: a sweep is signed and broadcast in one step, then
// confirmed or failed on chain.
const (
	SweepBroadcast = "BROADCAST"
	SweepConfirmed = "CONFIRMED"
	SweepFailed    = "FAILED"
)

// Sweep moves a deposit address's coin to the hot wallet (§5.10). It
// needs no journal: both are platform wallets. Its gas is a ChainFee.
type Sweep struct {
	ID        string
	Network   string
	Address   string
	Index     uint32
	Asset     string
	Amount    decimal.Decimal
	Nonce     uint64
	TxHash    string
	Raw       string
	Status    string
	CommandID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Purposes of on-chain fees.
const (
	FeeSweep      = "SWEEP"
	FeeWithdrawal = "WITHDRAWAL"
)

// ChainFee is the gas a mined platform transaction cost; the ledger books
// it from GAS_SUPPLY to WITHDRAWAL_PENDING.
type ChainFee struct {
	TxHash    string
	Network   string
	Asset     string
	Amount    decimal.Decimal
	Purpose   string
	Reference string
	JournalID string
	BookedAt  time.Time
}

// Funding is the platform's own transfer into its hot wallet, booked to a
// system account such as GAS_SUPPLY.
type Funding struct {
	TxHash      string
	Network     string
	Asset       string
	AccountType string
	Amount      decimal.Decimal
	JournalID   string
	CommandID   string
	CreatedAt   time.Time
}

// ChainCheck compares what the platform's wallets hold with what the
// ledger expects them to hold, −(DEPOSIT_PENDING + WITHDRAWAL_PENDING)
// (invariant 4). Gas already paid but not yet booked explains part of a
// difference; the rest is a shortfall when positive.
type ChainCheck struct {
	Network   string
	Asset     string
	Chain     decimal.Decimal
	Ledger    decimal.Decimal
	Unbooked  decimal.Decimal
	Shortfall decimal.Decimal
	Addresses int
	CheckedAt time.Time
}

// NewChainCheck computes the shortfall.
func NewChainCheck(network, asset string, chain, ledger, unbooked decimal.Decimal, addresses int, at time.Time) ChainCheck {
	return ChainCheck{
		Network: network, Asset: asset, Chain: chain, Ledger: ledger, Unbooked: unbooked,
		Shortfall: ledger.Sub(chain).Sub(unbooked), Addresses: addresses, CheckedAt: at,
	}
}
