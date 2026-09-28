package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP uses HMAC-SHA1, as authenticator apps do
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// TOTP (RFC 6238, requirements §6.5): HMAC-SHA1, 30-second steps, 6
// digits, one step of clock skew either way. A step is accepted once:
// a code must belong to a step after the last one used.
const (
	TOTPPeriod = 30 * time.Second
	totpDigits = 6
	totpSkew   = 1
	totpBytes  = 20
)

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

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

const totpModulus uint32 = 1_000_000

// NewTOTPSecret returns a random 160-bit secret.
func NewTOTPSecret() []byte {
	s := make([]byte, totpBytes)
	if _, err := rand.Read(s); err != nil {
		panic(err)
	}
	return s
}

// EncodeTOTPSecret writes a secret as authenticator apps take it.
func EncodeTOTPSecret(secret []byte) string { return b32.EncodeToString(secret) }

// TOTPURI is the otpauth URI for a QR code.
func TOTPURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {EncodeTOTPSecret(secret)}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPStep returns the step of t.
func TOTPStep(t time.Time) int64 { return t.Unix() / int64(TOTPPeriod/time.Second) }

// TOTPCode returns the code of a step.
func TOTPCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // steps are positive
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, bin%totpModulus)
}

// VerifyTOTP checks code at now and returns its step. Codes of steps at or
// before lastStep are refused, so an intercepted code cannot be replayed.
func VerifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, error) {
	if len(code) != totpDigits {
		return 0, ErrTOTPInvalid
	}
	cur := TOTPStep(now)
	for d := -totpSkew; d <= totpSkew; d++ {
		step := cur + int64(d)
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(TOTPCode(secret, step)), []byte(code)) == 1 {
			return step, nil
		}
	}
	return 0, ErrTOTPInvalid
}
