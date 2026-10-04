// Package domain holds the signer's own rules (requirements §5.10,
// ADR-0003): what it signs and within which limits, checked independently
// of wallet-service, so that a compromised business service still cannot
// send the platform's funds anywhere it likes.
package domain

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/evm"
)

// Purposes of a signature.
const (
	PurposeWithdrawal = "WITHDRAWAL"
	PurposeSweep      = "SWEEP"
)

// Key accounts (BIP-44 m/44'/60'/account').
const (
	DepositAccount = 0
	HotAccount     = 1
)

// ErrRefused is a request outside the signer's rules (appendix C).
var ErrRefused = apperr.New(apperr.KindForbidden, "SIGNER_REFUSED", "the signer refuses to sign this transaction")

// Request is an EIP-1559 transaction to sign.
type Request struct {
	ID         string
	Purpose    string
	Reference  string
	ApprovedBy string
	ChainID    uint64
	// Index is the deposit address a sweep spends from.
	Index    uint32
	Nonce    uint64
	To       string
	Value    *big.Int
	Data     []byte
	GasLimit uint64
	MaxFee   *big.Int
	MaxTip   *big.Int
}

// Hash digests the request, telling a retry from another request under
// the same ID.
func (r Request) Hash() []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%d|%d|%d|%s|%s|%x|%d|%s|%s", r.ID, r.Purpose, r.Reference, r.ApprovedBy, r.ChainID, r.Index,
		r.Nonce, strings.ToLower(r.To), r.Value, r.Data, r.GasLimit, r.MaxFee, r.MaxTip)
	return h.Sum(nil)
}

// Signature is a signed transaction with the request it answers.
type Signature struct {
	Request Request
	From    string
	TxHash  string
	Raw     string // 0x-prefixed binary encoding
	At      time.Time
}

// Policy bounds what the signer signs; limits are in wei.
type Policy struct {
	ChainID uint64
	// MaxValue caps one withdrawal, DailyValue the withdrawals signed in
	// the last 24 hours.
	MaxValue   *big.Int
	DailyValue *big.Int
	// MaxFee caps the fee per gas and MaxGas the gas limit.
	MaxFee *big.Int
	MaxGas uint64
}

func refuse(format string, args ...any) error {
	return ErrRefused.WithDetail("reason", fmt.Sprintf(format, args...))
}

// Check validates a request on its own; hot is the hot wallet's address.
// The history rules are in CheckHistory.
func (p Policy) Check(r Request, hot string) error {
	switch {
	case r.ID == "" || len(r.ID) > 100:
		return refuse("a request ID of at most 100 characters is required")
	case r.ChainID != p.ChainID:
		return refuse("chain %d is not the signer's chain %d", r.ChainID, p.ChainID)
	case !evm.ValidAddress(r.To):
		return refuse("the recipient is not an address")
	case r.Value == nil || r.Value.Sign() < 0 || r.MaxFee == nil || r.MaxTip == nil || r.MaxTip.Sign() < 0:
		return refuse("value and fees are required")
	case r.GasLimit == 0 || r.GasLimit > p.MaxGas:
		return refuse("the gas limit must be within 1..%d", p.MaxGas)
	case r.MaxFee.Cmp(p.MaxFee) > 0:
		return refuse("the fee per gas %s is above the signer's cap %s", r.MaxFee, p.MaxFee)
	case r.MaxTip.Cmp(r.MaxFee) > 0:
		return refuse("the tip is above the fee cap")
	case len(r.Data) > 0:
		// Token transfers come with token withdrawals and sweeps.
		return refuse("only coin transfers are signed")
	}
	switch r.Purpose {
	case PurposeWithdrawal:
		switch {
		case r.Reference == "" || r.ApprovedBy == "":
			return refuse("a withdrawal needs its ID and its approval")
		case r.Value.Sign() == 0 || r.Value.Cmp(p.MaxValue) > 0:
			return refuse("a withdrawal must be above 0 and at most %s wei", p.MaxValue)
		case strings.EqualFold(r.To, hot):
			return refuse("a withdrawal cannot pay the hot wallet")
		}
	case PurposeSweep:
		if !strings.EqualFold(r.To, hot) {
			return refuse("a sweep can only pay the hot wallet")
		}
		if r.Reference == "" {
			return refuse("a sweep needs its ID")
		}
	default:
		return refuse("unknown purpose %q", r.Purpose)
	}
	return nil
}

// CheckHistory applies the rules that need earlier signatures: every
// signature of one withdrawal pays the same recipient the same value with
// the same nonce (replacements only raise the fee, so it can never be
// paid twice), and withdrawals stay within the daily limit. signed are
// the earlier signatures of the request's reference; daily is the value
// of the withdrawals signed in the last 24 hours, one per reference.
func (p Policy) CheckHistory(r Request, signed []Signature, daily *big.Int) error {
	for _, s := range signed {
		prev := s.Request
		if prev.Purpose != r.Purpose || prev.Nonce != r.Nonce || !strings.EqualFold(prev.To, r.To) || prev.Value.Cmp(r.Value) != 0 {
			return refuse("%s was signed before with another nonce, recipient or value", r.Reference)
		}
		if r.MaxFee.Cmp(prev.MaxFee) <= 0 {
			return refuse("a replacement must raise the fee")
		}
	}
	if r.Purpose == PurposeWithdrawal && len(signed) == 0 && new(big.Int).Add(daily, r.Value).Cmp(p.DailyValue) > 0 {
		return refuse("the signer's daily withdrawal limit of %s wei would be exceeded", p.DailyValue)
	}
	return nil
}
