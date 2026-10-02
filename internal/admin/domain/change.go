package domain

import (
	"encoding/json"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// A change of trading parameters (design 2026-10-02 §2 item 6): a pair's
// or contract's status other than a halt, fee rates, risk ladders and
// reference symbols. An ADMIN confirms what the preview showed; the change
// then waits Settings.ChangeDelay before it takes effect, and while
// two-person approval is on it first waits for a second ADMIN. Any ADMIN
// may cancel it until it takes effect.

// The kinds of changes.
const (
	ChangeConfig         = "CONFIG"
	ChangePairStatus     = "PAIR_STATUS"
	ChangeContractStatus = "CONTRACT_STATUS"
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
)

// InstrumentChange is a confirmed change of trading parameters.
type InstrumentChange struct {
	ID string
	// Kind is CONFIG (a config document), PAIR_STATUS or CONTRACT_STATUS.
	Kind string
	// Target is what it changes: instruments, pair:<symbol>,
	// contract:<symbol>.
	Target string
	// Payload is the config document, or {"symbol","from","to"}.
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

// Cancel withdraws a change before it takes effect.
func (c *InstrumentChange) Cancel(by string, now time.Time) error {
	if !c.Open() {
		return ErrChangeClosed
	}
	c.Status, c.ClosedBy, c.ClosedAt = ChangeCanceled, by, now
	return nil
}

// Settle records how applying a scheduled change went.
func (c *InstrumentChange) Settle(applied bool, result string, now time.Time) {
	c.Status, c.Result, c.AppliedAt = ChangeFailed, result, now
	if applied {
		c.Status = ChangeApplied
	}
}
