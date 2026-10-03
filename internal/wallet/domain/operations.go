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

// ChainFee is the gas a mined platform transaction cost, or the fee a
// custodian charged for a withdrawal it sent; the ledger books it from
// GAS_SUPPLY to WITHDRAWAL_PENDING. A custodian's fee a person must look
// at first is held (FeeHeld, HoldReason) until they book it, in the asset
// and amount they found it charged, or write it off (review ④).
type ChainFee struct {
	TxHash    string
	Network   string
	Asset     string
	Amount    decimal.Decimal
	Purpose   string
	Reference string
	JournalID string
	BookedAt  time.Time
	// Status is FeeBookable ("" when inserted), FeeHeld or
	// FeeWrittenOff.
	Status     string
	HoldReason string
	ResolvedBy string
	Resolution string
	ResolvedAt time.Time
	CreatedAt  time.Time
}

// CustodyFee is a custodian's fee for a withdrawal as the console lists
// it: the fee, its withdrawal and the withdrawal's custodian, and how the
// custodian counts its fee on the withdrawal's network as a person
// confirmed it ("" when nobody did).
type CustodyFee struct {
	ChainFee
	WithdrawalID string
	Provider     string
	Unit         string
}

// What becomes of a fee.
const (
	FeeBookable   = "BOOKABLE"
	FeeHeld       = "HELD"
	FeeWrittenOff = "WRITTEN_OFF"
)

// How a custodian counts its fee on a network's withdrawals, as a person
// confirmed it against a real withdrawal (FeeUnit, review ④).
const (
	// FeeUnitSelf: in the withdrawal's asset, at the callback's decimals,
	// as the custodian's documentation reads; taken without a
	// confirmation for a chain's own coin.
	FeeUnitSelf = "SELF"
	// FeeUnitMain: in the chain's own coin at its decimals (the gas of a
	// token's transfer), booked in the asset the platform holds of that
	// coin with the custodian.
	FeeUnitMain = "MAIN"
	// FeeUnitOutside: charged outside the coin balances the platform
	// reconciles; not booked.
	FeeUnitOutside = "OUTSIDE"
)

// FeeUnits are the units a person may confirm.
var FeeUnits = []string{FeeUnitSelf, FeeUnitMain, FeeUnitOutside}

// FeeUnit is a person's confirmation of how a custodian counts its fee on
// one of its networks. Until a token's is confirmed, each of its fees is
// held for a person.
type FeeUnit struct {
	Provider    string
	Asset       string
	Network     string
	Unit        string
	ConfirmedBy string
	Reason      string
	ConfirmedAt time.Time
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
// (invariant 4). The ledger's figure covers every holder of the asset: a
// check is made by one holder (Network: a network of the platform's own
// wallets, or the custodian, ProviderUdun) and counts what the others
// held at the time (Elsewhere). Gas already paid but not yet booked, and
// withdrawals with the custodian that it may have sent already
// (InFlight), explain part of a difference; the rest is a shortfall when
// positive.
type ChainCheck struct {
	Network   string
	Asset     string
	Chain     decimal.Decimal
	Ledger    decimal.Decimal
	Unbooked  decimal.Decimal
	Elsewhere decimal.Decimal
	InFlight  decimal.Decimal
	Shortfall decimal.Decimal
	// Baseline is the simulated deposits taken out of the expectation when
	// the custodian replaced a stand-in (CustodyBaseline): at no custodian,
	// shown apart; the shortfall does not count it.
	Baseline  decimal.Decimal
	Addresses int
	CheckedAt time.Time
}

// CustodyBaseline is a journal that took simulated deposits out of what
// the ledger expects a custodian to hold (exchangectl ledger
// custody-reset), negative when it put them back.
type CustodyBaseline struct {
	JournalID string
	Provider  string
	Asset     string
	Amount    decimal.Decimal
	Actor     string
	Reason    string
	CreatedAt time.Time
}

// RetiredAddress is a custodian's deposit address taken out of use.
type RetiredAddress struct {
	Network   string
	Address   string
	UserID    string
	Provider  string
	CreatedAt time.Time
	RetiredAt time.Time
	RetiredBy string
	Reason    string
}

// NewChainCheck computes the shortfall of a holder that is the asset's
// only one.
func NewChainCheck(network, asset string, chain, ledger, unbooked decimal.Decimal, addresses int, at time.Time) ChainCheck {
	return ChainCheck{
		Network: network, Asset: asset, Chain: chain, Ledger: ledger, Unbooked: unbooked, Addresses: addresses, CheckedAt: at,
	}.Beside(decimal.Zero, decimal.Zero)
}

// Beside counts what the asset's other holders hold and what is in
// flight, and computes the shortfall again.
func (c ChainCheck) Beside(elsewhere, inFlight decimal.Decimal) ChainCheck {
	c.Elsewhere, c.InFlight = elsewhere, inFlight
	c.Shortfall = c.Ledger.Sub(c.Chain).Sub(c.Elsewhere).Sub(c.InFlight).Sub(c.Unbooked)
	return c
}
