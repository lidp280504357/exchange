package domain

import "github.com/skill/exchange/internal/platform/apperr"

// Status is a margin account's state (contract MarginAccount.status).
type Status string

// Statuses: WARNED under the warning level, LIQUIDATING from the trigger
// to the end, FROZEN by an operator.
const (
	StatusNormal      Status = "NORMAL"
	StatusWarned      Status = "WARNED"
	StatusLiquidating Status = "LIQUIDATING"
	StatusFrozen      Status = "FROZEN"
)

// Open reports whether the account takes new risk: transfers in,
// borrowing and orders.
func (s Status) Open() bool { return s == StatusNormal || s == StatusWarned }

// Direction is a transfer's: IN from SPOT to the margin account, OUT
// back.
type Direction string

// Directions of a transfer.
const (
	DirectionIn  Direction = "IN"
	DirectionOut Direction = "OUT"
)

// ParseDirection checks a transfer's direction.
func ParseDirection(s string) (Direction, error) {
	switch Direction(s) {
	case DirectionIn, DirectionOut:
		return Direction(s), nil
	}
	return "", apperr.Invalid("direction must be IN or OUT")
}

// MoveType is one step of a margin posting in the ledger (design §3.2,
// ledger PostMargin).
type MoveType string

// Moves of a margin posting:
//
//   - TRANSFER_IN: SPOT to the margin account (MARGIN_TRANSFER_IN);
//     TRANSFER_OUT back (MARGIN_TRANSFER_OUT), never out of what the
//     asset's own debt holds;
//   - BORROW: the asset into the margin account against a principal owed
//     to HOUSE (MARGIN_BORROW);
//   - INTEREST: interest owed, HOUSE's income (MARGIN_INTEREST);
//   - REPAY: the margin account pays back interest (its Interest part)
//     and principal (MARGIN_REPAY), never more than is owed.
const (
	MoveTransferIn  MoveType = "TRANSFER_IN"
	MoveTransferOut MoveType = "TRANSFER_OUT"
	MoveBorrow      MoveType = "BORROW"
	MoveInterest    MoveType = "INTEREST"
	MoveRepay       MoveType = "REPAY"
)
