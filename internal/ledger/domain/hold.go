package domain

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Hold is an administrator's hold on part of a user's SPOT balance (design
// 2026-10-02 §4.1, risk control): ADMIN_FREEZE moves the amount from
// available to frozen, and releasing the hold moves it back with
// ADMIN_UNFREEZE, once. Holds stay on SPOT: the FUTURES frozen balance is
// the contracts' margin, which derivatives-service reconciles.
type Hold struct {
	ID          string
	UserID      string
	AccountType string
	Asset       string
	Amount      decimal.Decimal
	Reason      string
	// Actor is the administrator who placed it.
	Actor            string
	JournalID        string
	CreatedAt        time.Time
	ReleasedAt       time.Time
	ReleasedBy       string
	ReleaseReason    string
	ReleaseJournalID string
}

// Active reports whether the hold still freezes its amount.
func (h Hold) Active() bool { return h.ReleasedAt.IsZero() }

// Errors of holds.
var (
	ErrHoldNotFound = apperr.NotFound("no such hold")
	ErrHoldReleased = apperr.New(apperr.KindConflict, "LEDGER_HOLD_RELEASED", "the hold was already released")
)

func needReason(reason string) error {
	if len(strings.TrimSpace(reason)) < 3 {
		return apperr.Invalid("a reason of at least 3 characters is required")
	}
	return nil
}

// NewHold checks a hold an administrator asks for.
func NewHold(id, userID, asset string, amount decimal.Decimal, decimals int32, actor, reason string, now time.Time) (Hold, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Hold{}, err
	}
	if strings.TrimSpace(actor) == "" {
		return Hold{}, apperr.Invalid("the actor is required")
	}
	if err := needReason(reason); err != nil {
		return Hold{}, err
	}
	return Hold{
		ID: id, UserID: userID, AccountType: AccountSpot, Asset: asset, Amount: amount, Reason: strings.TrimSpace(reason),
		Actor: actor, CreatedAt: now,
	}, nil
}

// HoldPosting freezes the hold's amount (ADMIN_FREEZE, key hold:<id>).
func HoldPosting(h Hold, decimals int32) (Posting, error) {
	return moveWithin("hold:"+h.ID, EntryAdminFreeze, h.UserID, h.AccountType, h.Asset, h.Amount, decimals, h.Reason, Available, Frozen)
}

// ReleasePosting returns a released hold's amount to available
// (ADMIN_UNFREEZE, key hold-release:<id>, the release's reason as memo).
func ReleasePosting(h Hold, decimals int32) (Posting, error) {
	return moveWithin("hold-release:"+h.ID, EntryAdminUnfreeze, h.UserID, h.AccountType, h.Asset, h.Amount, decimals, h.ReleaseReason,
		Frozen, Available)
}

// Release marks the hold released by actor; it fails once released.
func (h *Hold) Release(actor, reason string, now time.Time) error {
	if !h.Active() {
		return ErrHoldReleased
	}
	if strings.TrimSpace(actor) == "" {
		return apperr.Invalid("the actor is required")
	}
	if err := needReason(reason); err != nil {
		return err
	}
	h.ReleasedAt, h.ReleasedBy, h.ReleaseReason = now, actor, strings.TrimSpace(reason)
	return nil
}
