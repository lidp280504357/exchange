package domain

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Runtime caps (the user's decision of 2026-10-07, review C45): HOUSE's
// Caps are stored and changed from the console; the service's environment
// gives the first ones.

// StoredCaps are HOUSE's caps as stored: a version that every change
// raises, and who made the last change, when.
type StoredCaps struct {
	Caps      Caps
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
}

// CapsChange asks to replace the caps of Version (optimistic: another
// change in between is refused) with Caps. SignedBy is the service key
// the request was signed with (review FL, C47): the signer vouches for
// the actor, and only the admin console's may name an approver.
type CapsChange struct {
	Caps       Caps
	Version    int64
	Actor      string
	Approver   string
	ApprovalID string
	Reason     string
	SignedBy   string
}

// CapsRecord is a change as kept: what was set and what it replaced (none
// for the first, from the environment), and the key that signed it.
type CapsRecord struct {
	Version    int64
	Caps       Caps
	Previous   *Caps
	Actor      string
	Approver   string
	ApprovalID string
	Reason     string
	SignedBy   string
	At         time.Time
}

// ErrCapsVersion refuses a change of caps that are not the stored ones
// any more.
var ErrCapsVersion = apperr.New(apperr.KindConflict, "HOUSE_CAPS_VERSION", "the caps changed meanwhile: read them again")

// Validate checks a change: an actor and a reason, every cap a decimal
// not below zero (a level cap of zero leaves the levels whole), the
// contract leverage above it (zero would stop every contract quietly;
// market.house_liquidity does that openly).
func (c CapsChange) Validate() error {
	switch {
	case c.Actor == "":
		return apperr.Invalid("actor is required")
	case c.Reason == "":
		return apperr.Invalid("reason is required")
	case c.Version <= 0:
		return apperr.Invalid("version is required: the version of the caps being changed")
	case !c.Caps.ContractLeverage.IsPositive():
		return apperr.Invalid("contract_leverage must be above zero")
	}
	for name, v := range c.Caps.fields() {
		if v.IsNegative() {
			return apperr.Invalid(name + " must not be below zero")
		}
	}
	return nil
}

// fields names the caps as the API does.
func (c Caps) fields() map[string]decimal.Decimal {
	return map[string]decimal.Decimal{
		"level": c.Level, "symbol": c.Symbol, "total": c.Total, "contract": c.Contract, "safety": c.Safety,
		"contract_leverage": c.ContractLeverage,
	}
}
