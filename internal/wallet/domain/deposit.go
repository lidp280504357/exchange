// Package domain holds the wallet's model (requirements §5.10, §11.5):
// deposit addresses per user and network, derived from the platform's
// key or created by the custodian (ADR-0011), and deposits that move
// DETECTED -> CONFIRMING -> CONFIRMED -> CREDITED as blocks confirm them
// (a custodian reports them CONFIRMED), to ORPHANED when a reorganization
// drops them (back to DETECTED if they reappear) and to REJECTED when they
// cannot go to the user: unclaimed ones (below the minimum, closed
// account) are booked to UNCLAIMED_DEPOSIT, unsupported tokens are not
// booked at all.
package domain

import (
	"fmt"
	"strings"
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
	// ReasonUnknownAddress: a custodian's deposit to an address no user
	// has (a probe's, a retired stand-in's), owned by NoOwner (B7a).
	ReasonUnknownAddress = "UNKNOWN_ADDRESS"
)

// NoOwner owns a custodian's deposit to an address no user has (B7a of the
// real gateway's integration): booked to UNCLAIMED_DEPOSIT until an
// administrator names the user it is credited to (or dismisses it). It is
// the nil UUID, no user's ID; only that path writes it, and nothing that
// needs a user takes it: no event, eligibility or release names it.
const NoOwner = "00000000-0000-0000-0000-000000000000"

// NativeLog marks a transfer of the chain's own coin (no token log).
const NativeLog = -1

// Deposit kinds: from the chain, or another user's withdrawal to this
// deposit address, completed in the ledger (§11.6).
const (
	KindChain    = "CHAIN"
	KindInternal = "INTERNAL"
)

// Errors (appendix C).
var (
	ErrDepositsDisabled = apperr.New(apperr.KindUnprocessable, "WALLET_NETWORK_DISABLED", "deposits of this asset on this network are closed")
	ErrUnknownNetwork   = apperr.New(apperr.KindNotFound, "WALLET_NETWORK_UNKNOWN", "the asset has no such network")
	ErrNotConfigured    = apperr.New(apperr.KindUnavailable, "WALLET_UNAVAILABLE", "deposit addresses are not available yet")
)

// Providers of custody networks (instrument-service's network provider):
// the Udun custody wallet, and on the test server a second merchant of its
// stand-in gateway that serves only the hidden test asset of the end-to-end
// tests, so they go on when UDUN is the real gateway (ADR-0017).
const (
	ProviderUdun     = "UDUN"
	ProviderUdunMock = "UDUNMOCK"
)

// Address is a user's deposit address on a network: derived at Index, or
// created by Provider.
type Address struct {
	UserID    string
	Network   string
	Index     uint32
	Provider  string
	Address   string
	CreatedAt time.Time
}

// Deposit is an incoming transfer to a deposit address.
type Deposit struct {
	ID          string
	Kind        string // KindChain unless set
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
	// ProviderTxID is the custodian's ID of a deposit it reported
	// ("UDUN:<tradeId>"), which keys it.
	ProviderTxID string
	// CreditRequested is when DepositConfirmed went to the ledger.
	CreditRequested time.Time
	DetectedAt      time.Time
	ConfirmedAt     time.Time
	CreditedAt      time.Time
	// Source is SourceManual for an administrator's backfill of a
	// custodian deposit whose callback was lost (EnteredBy names who),
	// empty otherwise. CallbackAt is when the custodian's own callback
	// matched it; Discrepancy what in that callback disagreed.
	Source      string
	EnteredBy   string
	CallbackAt  time.Time
	Discrepancy string
	// An administrator's decision on a deposit that needs one (Attention):
	// Resolution* and, for released unclaimed funds, the release's journal.
	Resolution       string
	ResolvedBy       string
	ResolvedAt       time.Time
	ResolutionNote   string
	ReleaseJournalID string
}

// SourceManual marks a deposit an administrator backfilled (design
// 2026-10-02 §4.3).
const SourceManual = "MANUAL"

// Resolutions of a deposit that needs a decision: its unclaimed funds
// released to the user, or the item dismissed.
const (
	ResolutionCredited  = "CREDITED"
	ResolutionDismissed = "DISMISSED"
)

// Errors of the admin console's deposit handling (appendix C).
var (
	ErrNotReleasable = apperr.New(apperr.KindConflict, "WALLET_DEPOSIT_NOT_RELEASABLE",
		"only an unclaimed deposit booked to UNCLAIMED_DEPOSIT and not decided yet can be credited")
	ErrResolved = apperr.New(apperr.KindConflict, "WALLET_DEPOSIT_RESOLVED", "the deposit needs no decision")
	ErrNoOwner  = apperr.New(apperr.KindConflict, "WALLET_DEPOSIT_NO_OWNER",
		"the deposit came to an address no user has: name the user it is credited to")
	ErrKnown = apperr.New(apperr.KindConflict, "WALLET_DEPOSIT_KNOWN", "this deposit is known already")
)

// Attention reports whether the deposit waits for an administrator: it
// did not reach the user (REJECTED), or the custodian's callback of a
// backfilled one disagreed, and nobody decided yet.
func (d *Deposit) Attention() bool {
	return d.Resolution == "" && (d.Status == StatusRejected || d.Discrepancy != "")
}

// Releasable reports whether an administrator may credit the deposit's
// funds to the user: unclaimed, booked to UNCLAIMED_DEPOSIT, undecided,
// and not a backfill the custodian's callback disagreed with (what it
// says is in doubt).
func (d *Deposit) Releasable() error {
	if d.Status != StatusRejected || !d.Unclaimed || d.JournalID == "" || d.Resolution != "" || d.Discrepancy != "" {
		return ErrNotReleasable
	}
	if d.UserID == NoOwner {
		return ErrNoOwner
	}
	return nil
}

// Release records an administrator crediting an unclaimed deposit's
// funds, booked to UNCLAIMED_DEPOSIT, to the user (journal is the
// release's): it becomes CREDITED.
func (d *Deposit) Release(journal, actor, note string, now time.Time) error {
	if err := d.Releasable(); err != nil {
		return err
	}
	d.Status, d.CreditedAt = StatusCredited, now
	d.Resolution, d.ResolvedBy, d.ResolvedAt, d.ResolutionNote, d.ReleaseJournalID = ResolutionCredited, actor, now, note, journal
	return nil
}

// RecordRelease records a release of an unclaimed deposit the ledger made
// already (journal) while the deposit was put in doubt since (a
// discrepancy marked after it): the funds moved, so it is CREDITED, as
// Release would have left it (C5.5 ⑮).
func (d *Deposit) RecordRelease(journal, actor, note string, now time.Time) error {
	if d.Status != StatusRejected || !d.Unclaimed || d.JournalID == "" || d.Resolution != "" || journal == "" {
		return ErrNotReleasable
	}
	d.Status, d.CreditedAt = StatusCredited, now
	d.Resolution, d.ResolvedBy, d.ResolvedAt, d.ResolutionNote, d.ReleaseJournalID = ResolutionCredited, actor, now, note, journal
	return nil
}

// Dismiss records an administrator closing a deposit that waited for a
// decision without moving funds.
func (d *Deposit) Dismiss(actor, note string, now time.Time) error {
	if !d.Attention() {
		return ErrResolved
	}
	d.Resolution, d.ResolvedBy, d.ResolvedAt, d.ResolutionNote = ResolutionDismissed, actor, now, note
	return nil
}

// MatchCallback compares a backfilled deposit with the custodian's own
// callback that arrived later (found by its trade, or by its transfer
// when the backfill was entered with another trade): the same trade,
// address, asset and amount mark it confirmed; anything else is recorded
// as a discrepancy for an administrator, never corrected or booked again.
// It reports whether the callback matched.
func (d *Deposit) MatchCallback(trade, address, asset string, amount decimal.Decimal, now time.Time) bool {
	d.CallbackAt = now
	var diffs []string
	if d.ProviderTxID != trade {
		diffs = append(diffs, fmt.Sprintf("trade %s, entered %s", trade, d.ProviderTxID))
	}
	if !strings.EqualFold(d.Address, address) {
		diffs = append(diffs, fmt.Sprintf("address %s, entered %s", address, d.Address))
	}
	if d.Asset != asset {
		diffs = append(diffs, fmt.Sprintf("asset %s, entered %s", asset, d.Asset))
	}
	if !d.Amount.Equal(amount) {
		diffs = append(diffs, fmt.Sprintf("amount %s, entered %s", amount, d.Amount))
	}
	if len(diffs) == 0 {
		return true
	}
	d.Discrepancy = "the custodian's callback says " + strings.Join(diffs, "; ")
	return false
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
// CONFIRMED and was not sent yet; a backfill the custodian's callback
// disagreed with, or one an administrator closed, waits for a person
// (C5.5 ⑦).
func (d *Deposit) RequestCredit(reason string, now time.Time) bool {
	if d.Status != StatusConfirmed || !d.CreditRequested.IsZero() || d.Discrepancy != "" || d.Resolution != "" {
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
	// Withdrawals: both switches on, the minimum and the fixed fee.
	WithdrawEnabled bool
	MinWithdraw     decimal.Decimal
	WithdrawFee     decimal.Decimal
	MemoRequired    bool
	// What users see of the network: its name (TRC20, ERC20, ...), how
	// its addresses look (FormatEVM, FormatTRON, FormatBTC), the usual
	// minutes to a credit, and block explorer links with {tx} or {address}.
	DisplayName        string
	AddressFormat      string
	ETAMinutes         int32
	ExplorerTxURL      string
	ExplorerAddressURL string
	// Provider is the custodian that serves the network (ProviderUdun),
	// empty for the platform's own wallets; ProviderCoin its code of the
	// asset there ("mainCoinType:coinType").
	Provider     string
	ProviderCoin string
	// Hidden: the asset is a hidden test asset (ADR-0017); its networks are
	// only for users eligible for TEST_ASSETS, unknown to anyone else.
	Hidden bool
}

// Custody reports whether a custodian serves the network.
func (n Network) Custody() bool { return n.Provider != "" }
