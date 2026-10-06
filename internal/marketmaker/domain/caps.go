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

// CapsPatch is what a change sets: the caps given, the others as they are.
type CapsPatch struct {
	Level, Symbol, Total, Contract, Safety, ContractLeverage *decimal.Decimal
}

// CapsChange asks to change the caps of Version (optimistic: another
// change in between is refused) by Patch, applied to the stored caps in
// the same transaction that checks the version (review FL, C47: not to a
// copy read up to 10 seconds before). SignedBy is the service key the
// request was signed with: the signer vouches for the actor, and only the
// admin console's may name an approver.
type CapsChange struct {
	Patch      CapsPatch
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

// ErrCapsStep refuses a change that moves a cap more than CapsMaxStep
// times up or down: a digit too many or too few, most likely.
var ErrCapsStep = apperr.New(apperr.KindInvalid, "HOUSE_CAPS_STEP",
	"a change may move a cap at most ten times up or down: change it in steps")

// The bounds of the caps (review FL, C47): amounts in USDT up to
// MaxCapAmount (far beyond any book, and well inside the columns), the
// contract leverage from 1 to MaxContractLeverage, and one change moving a
// cap by at most CapsMaxStep times either way.
var (
	MaxCapAmount        = decimal.New(1, 15)
	MaxContractLeverage = decimal.NewFromInt(125)
	CapsMaxStep         = decimal.NewFromInt(10)
)

// Validate checks the caps against their bounds: the level cap at least
// zero (zero leaves the levels whole), the other amounts above it (a
// symbol or total cap of zero stops HOUSE buying anything, a contract cap
// of zero leaves only reductions: market.house_liquidity does that
// openly), all at most MaxCapAmount, and the contract leverage from 1 to
// MaxContractLeverage.
func (c Caps) Validate() error {
	for _, f := range c.fields() {
		switch {
		case f.name == "contract_leverage":
			if f.v.LessThan(decimal.NewFromInt(1)) || f.v.GreaterThan(MaxContractLeverage) {
				return apperr.Invalid("contract_leverage must be from 1 to 125")
			}
		case f.name == "level" && f.v.IsNegative():
			return apperr.Invalid("level must not be below zero (zero: levels whole)")
		case f.name != "level" && !f.v.IsPositive():
			return apperr.Invalid(f.name + " must be above zero")
		case f.v.GreaterThan(MaxCapAmount):
			return apperr.Invalid(f.name + " must be at most 1000000000000000")
		}
	}
	return nil
}

// Validate checks a change before the stored caps are read: an actor, a
// reason, the version changed, and something to change.
func (c CapsChange) Validate() error {
	p := c.Patch
	switch {
	case c.Actor == "":
		return apperr.Invalid("actor is required")
	case c.Reason == "":
		return apperr.Invalid("reason is required")
	case c.Version <= 0:
		return apperr.Invalid("version is required: the version of the caps being changed")
	case p.Level == nil && p.Symbol == nil && p.Total == nil && p.Contract == nil && p.Safety == nil && p.ContractLeverage == nil:
		return apperr.Invalid("no cap to change")
	}
	return nil
}

// Next is cur with the change applied: within the bounds, and each cap
// moved by at most CapsMaxStep times up or down (a level cap going to or
// from zero, "whole", is not a step).
func (c CapsChange) Next(cur Caps) (Caps, error) {
	next := cur
	for _, f := range []struct {
		in  *decimal.Decimal
		out *decimal.Decimal
	}{
		{c.Patch.Level, &next.Level},
		{c.Patch.Symbol, &next.Symbol},
		{c.Patch.Total, &next.Total},
		{c.Patch.Contract, &next.Contract},
		{c.Patch.Safety, &next.Safety},
		{c.Patch.ContractLeverage, &next.ContractLeverage},
	} {
		if f.in != nil {
			*f.out = *f.in
		}
	}
	if err := next.Validate(); err != nil {
		return Caps{}, err
	}
	before := cur.fields()
	for i, f := range next.fields() {
		was := before[i].v
		if was.IsPositive() && f.v.IsPositive() && (f.v.GreaterThan(was.Mul(CapsMaxStep)) || f.v.Mul(CapsMaxStep).LessThan(was)) {
			return Caps{}, ErrCapsStep.WithDetail("cap", f.name).WithDetail("from", was.String()).WithDetail("to", f.v.String())
		}
	}
	return next, nil
}

type capField struct {
	name string
	v    decimal.Decimal
}

// fields names the caps as the API does, in its order.
func (c Caps) fields() []capField {
	return []capField{
		{"level", c.Level},
		{"symbol", c.Symbol},
		{"total", c.Total},
		{"contract", c.Contract},
		{"safety", c.Safety},
		{"contract_leverage", c.ContractLeverage},
	}
}
