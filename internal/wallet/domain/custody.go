package domain

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// What became of a custodian's callback (ADR-0011): recorded and not
// processed yet, applied, nothing to do (a review step of a deposit, a
// repeat), no deposit address, coin or withdrawal of ours to apply it to
// (a person looks), refused (signature, age or form) or failed while
// being applied (the custodian retries; an operator may replay it); or a
// deposit an administrator backfilled that the callback disagrees with
// (a person looks; nothing is booked again).
const (
	CallbackReceived    = "RECEIVED"
	CallbackApplied     = "APPLIED"
	CallbackIgnored     = "IGNORED"
	CallbackUnmatched   = "UNMATCHED"
	CallbackRejected    = "REJECTED"
	CallbackFailed      = "FAILED"
	CallbackDiscrepancy = "DISCREPANCY"
)

// Kinds of callbacks.
const (
	CallbackDeposit    = "DEPOSIT"
	CallbackWithdrawal = "WITHDRAWAL"
)

// Errors of callbacks (appendix C).
var (
	ErrCallbackSignature = apperr.New(apperr.KindUnauthenticated, "WALLET_CALLBACK_SIGNATURE", "the callback's signature does not match")
	ErrCallbackStale     = apperr.New(apperr.KindUnauthenticated, "WALLET_CALLBACK_STALE", "the callback's timestamp is outside the accepted window")
	ErrCallbackMalformed = apperr.New(apperr.KindInvalid, "WALLET_CALLBACK_MALFORMED", "not a callback of the custodian")
	ErrCustodyRefused    = apperr.New(apperr.KindUnprocessable, "WALLET_CUSTODY_REFUSED", "the custodian refused the request")
)

// Callback is a custodian's callback as received, with what became of it.
// A verified one is kept once per trade and status; the custodian's
// retries count as attempts.
type Callback struct {
	ID       string
	Provider string
	TradeID  string
	Kind     string
	// Status is the custodian's status code, -1 when unreadable.
	Status      int
	BusinessID  string
	Coin        string
	Address     string
	Amount      *decimal.Decimal
	TxHash      string
	Raw         string
	SignatureOK bool
	Result      string
	Detail      string
	Attempts    int
	ReceivedAt  time.Time
	ProcessedAt time.Time
}

// Settled reports whether nothing is left to do with the callback.
func (c *Callback) Settled() bool {
	return c.Result == CallbackApplied || c.Result == CallbackIgnored || c.Result == CallbackRejected
}
