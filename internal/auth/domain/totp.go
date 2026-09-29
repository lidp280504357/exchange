package domain

import (
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/totp"
)

// TOTPPeriod is the step of TOTP (RFC 6238, requirements §6.5), which is
// internal/platform/totp: HMAC-SHA1, 30-second steps, 6 digits, one step
// of clock skew either way, each step accepted once.
const TOTPPeriod = totp.Period

// TOTP states.
const (
	TOTPPending = "PENDING" // set up, not confirmed with a code yet
	TOTPActive  = "ACTIVE"
)

// Errors (appendix C).
var (
	ErrTOTPInvalid    = apperr.New(apperr.KindUnprocessable, "AUTH_TOTP_INVALID", "wrong or already used authenticator code")
	ErrTOTPNotEnabled = apperr.New(apperr.KindConflict, "AUTH_TOTP_NOT_ENABLED", "no authenticator app is bound")
	ErrTOTPEnabled    = apperr.New(apperr.KindConflict, "AUTH_TOTP_ENABLED", "an authenticator app is already bound")
	ErrTOTPRequired   = apperr.New(apperr.KindForbidden, "AUTH_TOTP_REQUIRED", "confirm with your authenticator app")
	ErrTOTPNotPending = apperr.New(apperr.KindConflict, "AUTH_TOTP_NOT_PENDING", "set up the authenticator app first")
)

// NewTOTPSecret returns a random 160-bit secret.
func NewTOTPSecret() []byte { return totp.NewSecret() }

// EncodeTOTPSecret writes a secret as authenticator apps take it.
func EncodeTOTPSecret(secret []byte) string { return totp.Encode(secret) }

// TOTPURI is the otpauth URI for a QR code.
func TOTPURI(issuer, account string, secret []byte) string { return totp.URI(issuer, account, secret) }

// TOTPStep returns the step of t.
func TOTPStep(t time.Time) int64 { return totp.Step(t) }

// TOTPCode returns the code of a step.
func TOTPCode(secret []byte, step int64) string { return totp.Code(secret, step) }

// VerifyTOTP checks code at now and returns its step. Codes of steps at or
// before lastStep are refused, so an intercepted code cannot be replayed.
func VerifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, error) {
	step, ok := totp.Verify(secret, code, now, lastStep)
	if !ok {
		return 0, ErrTOTPInvalid
	}
	return step, nil
}
