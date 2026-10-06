package domain

import (
	"encoding/json"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
)

// A change of trading parameters (design 2026-10-02 §2 item 6): a pair's
// or contract's status other than a halt, fee rates, risk ladders and
// reference symbols. An ADMIN confirms what the preview showed; the change
// then waits Settings.ChangeDelay before it takes effect, and while
// two-person approval is on it first waits for a second ADMIN. Any ADMIN
// may cancel it until it takes effect.

// The kinds of changes. COIN_CONTRACTS_STATUS closes or reopens a coin's
// contracts at once (coin-margined design 2026-10-06 §3.5).
const (
	ChangeConfig              = "CONFIG"
	ChangePairStatus          = "PAIR_STATUS"
	ChangeContractStatus      = "CONTRACT_STATUS"
	ChangeCoinContractsStatus = "COIN_CONTRACTS_STATUS"
)

// The statuses of a change.
const (
	ChangePendingApproval = "PENDING_APPROVAL"
	ChangeScheduled       = "SCHEDULED"
	ChangeApplied         = "APPLIED"
	ChangeCanceled        = "CANCELED"
	ChangeRejected        = "REJECTED"
	ChangeFailed          = "FAILED"
)

// Errors of changes.
var (
	// ErrConfirmationRequired asks for the preview's confirmation of a
	// change of trading parameters, or a fresh one when it expired or the
	// change is no longer what was previewed.
	ErrConfirmationRequired = apperr.New(apperr.KindConflict, "ADMIN_CONFIRMATION_REQUIRED",
		"preview the change and confirm what it does")
	// ErrChangeClosed refuses deciding a change that took effect or was
	// canceled, rejected or failed.
	ErrChangeClosed = apperr.New(apperr.KindConflict, "ADMIN_CHANGE_CLOSED", "the change was already applied or closed")
	// ErrChangeApplying refuses canceling a change an apply round claimed:
	// it may have taken effect already (C5.5 ⑩).
	ErrChangeApplying = apperr.New(apperr.KindConflict, "ADMIN_CHANGE_APPLYING", "the change is being applied")
)

// InstrumentChange is a confirmed change of trading parameters.
type InstrumentChange struct {
	ID string
	// Kind is CONFIG (a config document), PAIR_STATUS, CONTRACT_STATUS or
	// COIN_CONTRACTS_STATUS.
	Kind string
	// Target is what it changes: instruments, pair:<symbol>,
	// contract:<symbol>, coin:<coin>.
	Target string
	// Payload is the config document, {"symbol","from","to"}, or a coin's
	// {"coin","to","contracts":[{"symbol","margin_type","from","to"}]}.
	Payload json.RawMessage
	// Summary is what the confirmation showed: the trading parameters it
	// moves and, for a ladder, the positions it would liquidate.
	Summary json.RawMessage
	Reason  string
	Status  string
	// RequestedBy confirmed it; ApprovedBy (two-person) approved it;
	// ClosedBy canceled or rejected it. The emails are read with them.
	RequestedBy      string
	RequestedByEmail string
	ApprovedBy       string
	ApprovedByEmail  string
	ApprovedAt       time.Time
	ClosedBy         string
	ClosedByEmail    string
	ClosedAt         time.Time
	// EffectiveAt is when a scheduled change takes effect.
	EffectiveAt time.Time
	AppliedAt   time.Time
	// ApplyingAt is when an apply round claimed it: until its outcome is
	// recorded it may have taken effect, so it is not canceled, and a
	// round finding it claimed checks whether it did (C5.5 ⑩).
	ApplyingAt time.Time
	// ConfirmationHash identifies the preview's confirmation that
	// confirmed it: a confirmation confirms one change.
	ConfirmationHash string
	// Result is why it failed, or what applying it reported.
	Result    string
	CreatedAt time.Time
}

// NewInstrumentChange is a change an ADMIN confirmed: waiting for a second
// ADMIN with two-person approval on, else scheduled after delay.
func NewInstrumentChange(id, kind, target string, payload, summary json.RawMessage, reason, requestedBy string, twoPerson bool,
	delay time.Duration, now time.Time,
) InstrumentChange {
	c := InstrumentChange{
		ID: id, Kind: kind, Target: target, Payload: payload, Summary: summary, Reason: reason, Status: ChangeScheduled,
		RequestedBy: requestedBy, EffectiveAt: now.Add(delay), CreatedAt: now,
	}
	if twoPerson {
		c.Status, c.EffectiveAt = ChangePendingApproval, time.Time{}
	}
	return c
}

// Open reports whether the change may still be decided or canceled.
func (c InstrumentChange) Open() bool {
	return c.Status == ChangePendingApproval || c.Status == ChangeScheduled
}

// Approve schedules a change waiting for a second ADMIN after delay.
func (c *InstrumentChange) Approve(by string, delay time.Duration, now time.Time) error {
	if c.Status != ChangePendingApproval {
		return ErrChangeClosed
	}
	if by == c.RequestedBy {
		return ErrSelfApproval
	}
	c.Status, c.ApprovedBy, c.ApprovedAt, c.EffectiveAt = ChangeScheduled, by, now, now.Add(delay)
	return nil
}

// Reject refuses a change waiting for a second ADMIN (its requester
// cancels it instead).
func (c *InstrumentChange) Reject(by string, now time.Time) error {
	if c.Status != ChangePendingApproval {
		return ErrChangeClosed
	}
	if by == c.RequestedBy {
		return ErrSelfApproval
	}
	c.Status, c.ClosedBy, c.ClosedAt = ChangeRejected, by, now
	return nil
}

// Cancel withdraws a change before it takes effect, unless an apply
// round has it.
func (c *InstrumentChange) Cancel(by string, now time.Time) error {
	if !c.Open() {
		return ErrChangeClosed
	}
	if !c.ApplyingAt.IsZero() {
		return ErrChangeApplying
	}
	c.Status, c.ClosedBy, c.ClosedAt = ChangeCanceled, by, now
	return nil
}

// ClaimHold is how long a round's claim keeps other rounds off a change:
// one claimed longer ago was left by a round that stopped.
const ClaimHold = time.Minute

// Claim marks a due change as being applied by a round, now; false when a
// round claimed it before and its outcome is unknown.
func (c *InstrumentChange) Claim(now time.Time) bool {
	fresh := c.ApplyingAt.IsZero()
	c.ApplyingAt = now
	return fresh
}

// Wait releases a claimed change that did not take effect this round
// (a service down): it stays scheduled for the next, with why.
func (c *InstrumentChange) Wait(result string) {
	c.ApplyingAt, c.Result = time.Time{}, result
}

// Settle records how applying a scheduled change went.
func (c *InstrumentChange) Settle(applied bool, result string, now time.Time) {
	c.Status, c.Result, c.AppliedAt = ChangeFailed, result, now
	if applied {
		c.Status = ChangeApplied
	}
}
