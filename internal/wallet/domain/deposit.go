// Package domain holds the wallet's model (requirements §5.10, §11.5):
// deposit addresses derived per user and network, and deposits that move
// DETECTED -> CONFIRMING -> CONFIRMED -> CREDITED as blocks confirm them,
// to ORPHANED when a reorganization drops them (back to DETECTED if they
// reappear) and to REJECTED when they cannot go to the user: unclaimed
// ones (below the minimum, closed account) are booked to
// UNCLAIMED_DEPOSIT, unsupported tokens are not booked at all.
package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Deposit statuses (appendix B).
const (
	StatusDetected   = "DETECTED"
	StatusConfirming = "CONFIRMING"
	StatusConfirmed  = "CONFIRMED"
	StatusCredited   = "CREDITED"
	StatusOrphaned   = "ORPHANED"
	StatusRejected   = "REJECTED"
)

// Reasons a deposit does not reach the user (§11.5).
const (
	ReasonBelowMinimum     = "BELOW_MINIMUM"
	ReasonAccountClosed    = "ACCOUNT_CLOSED"
	ReasonNotEligible      = "NOT_ELIGIBLE"
	ReasonUnsupportedToken = "UNSUPPORTED_TOKEN"
)

// NativeLog marks a transfer of the chain's own coin (no token log).
const NativeLog = -1

// Errors (appendix C).
var (
	ErrDepositsDisabled = apperr.New(apperr.KindUnprocessable, "WALLET_NETWORK_DISABLED", "deposits of this asset on this network are closed")
	ErrUnknownNetwork   = apperr.New(apperr.KindNotFound, "WALLET_NETWORK_UNKNOWN", "the asset has no such network")
	ErrNotConfigured    = apperr.New(apperr.KindUnavailable, "WALLET_UNAVAILABLE", "deposit addresses are not available yet")
)

// Address is a user's deposit address on a network.
type Address struct {
	UserID    string
	Network   string
	Index     uint32
	Address   string
	CreatedAt time.Time
}

// Deposit is an incoming transfer to a deposit address.
type Deposit struct {
	ID          string
	UserID      string
	Asset       string // empty for an unsupported token
	Network     string
	Address     string
	Contract    string // empty for the chain's coin
	TxHash      string
	LogIndex    int64
	BlockNumber uint64
	BlockHash   string
	// Amount is what the ledger books, in the asset's decimals; RawAmount
	// the transferred integer in the chain's smallest unit.
	Amount        decimal.Decimal
	RawAmount     decimal.Decimal
	Confirmations uint32
	Required      uint32
	// Unclaimed deposits are booked to UNCLAIMED_DEPOSIT, for Reason.
	Unclaimed bool
	Reason    string
	Status    string
	JournalID string
	// CreditRequested is when DepositConfirmed went to the ledger.
	CreditRequested time.Time
	DetectedAt      time.Time
	ConfirmedAt     time.Time
	CreditedAt      time.Time
}

// Pending reports whether the deposit still waits for confirmations.
func (d *Deposit) Pending() bool {
	return d.Status == StatusDetected || d.Status == StatusConfirming
}

// Observe records the confirmations seen at head (the last verified
// block) and reports whether the deposit has just become CONFIRMED.
func (d *Deposit) Observe(head uint64, now time.Time) bool {
	if !d.Pending() || head < d.BlockNumber {
		return false
	}
	conf := head - d.BlockNumber + 1
	d.Confirmations = uint32(min(conf, uint64(d.Required))) //nolint:gosec // at most Required
	if conf < uint64(d.Required) {
		d.Status = StatusConfirming
		return false
	}
	d.Status, d.ConfirmedAt = StatusConfirmed, now
	return true
}

// Orphan marks a deposit dropped by a reorganization. Only one not yet
// confirmed may be: a confirmed deposit is past the reorganization depth
// the network is configured for.
func (d *Deposit) Orphan() error {
	if !d.Pending() {
		return fmt.Errorf("deposit %s is %s, past the reorganization depth", d.ID, d.Status)
	}
	d.Status, d.Confirmations = StatusOrphaned, 0
	return nil
}

// Seen handles a rescan finding the deposit's transfer in block n with
// hash: an orphaned deposit returns to DETECTED there; another one that
// moved to a new block follows it. It reports whether anything changed.
func (d *Deposit) Seen(n uint64, hash string) bool {
	if d.BlockNumber == n && d.BlockHash == hash && d.Status != StatusOrphaned {
		return false
	}
	d.BlockNumber, d.BlockHash = n, hash
	if d.Status == StatusOrphaned {
		d.Status, d.Confirmations = StatusDetected, 0
	}
	return true
}

// RequestCredit marks the deposit as sent to the ledger, unclaimed for
// reason when not empty. It reports false unless the deposit is
// CONFIRMED and was not sent yet.
func (d *Deposit) RequestCredit(reason string, now time.Time) bool {
	if d.Status != StatusConfirmed || !d.CreditRequested.IsZero() {
		return false
	}
	if reason != "" {
		d.Unclaimed, d.Reason = true, reason
	}
	d.CreditRequested = now
	return true
}

// Credit records the ledger's journal: CREDITED, or REJECTED for an
// unclaimed deposit booked to UNCLAIMED_DEPOSIT.
func (d *Deposit) Credit(journalID string, now time.Time) bool {
	if d.Status != StatusConfirmed {
		return false
	}
	d.Status, d.JournalID, d.CreditedAt = StatusCredited, journalID, now
	if d.Unclaimed {
		d.Status = StatusRejected
	}
	return true
}

// Network is a deposit network of an asset, as instrument-service
// configures it.
type Network struct {
	Asset         string
	Network       string
	Chain         string
	Contract      string // empty for the native coin
	Decimals      int32  // the asset's, which the ledger books in
	Confirmations uint32
	MinDeposit    decimal.Decimal
	Enabled       bool // the asset and the network both take deposits
}
