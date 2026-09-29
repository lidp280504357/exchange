// Package totp implements RFC 6238 time-based one-time passwords as
// authenticator apps use them: HMAC-SHA1, 30-second steps, 6 digits, one
// step of clock skew either way. A step is accepted once: a code must
// belong to a step after the last one used.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP uses HMAC-SHA1, as authenticator apps do
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Period is the length of a step.
const Period = 30 * time.Second

const (
	digits  = 6
	skew    = 1
	bytes   = 20
	modulus = 1_000_000
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random 160-bit secret.
func NewSecret() []byte {
	s := make([]byte, bytes)
	if _, err := rand.Read(s); err != nil {
		panic(err)
	}
	return s
}

// Encode writes a secret as authenticator apps take it (base32).
func Encode(secret []byte) string { return b32.EncodeToString(secret) }

// Decode reads a base32 secret, with or without padding and in any case.
func Decode(s string) ([]byte, error) {
	return b32.DecodeString(strings.TrimRight(strings.ToUpper(strings.TrimSpace(s)), "="))
}

// URI is the otpauth URI for a QR code.
func URI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {Encode(secret)}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Step returns the step of t.
func Step(t time.Time) int64 { return t.Unix() / int64(Period/time.Second) }

// Code returns the code of a step.
func Code(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // steps are positive
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", digits, bin%modulus)
}

// Verify checks code at now and returns its step. Codes of steps at or
// before lastStep are refused, so an intercepted code cannot be replayed.
func Verify(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != digits {
		return 0, false
	}
	cur := Step(now)
	for d := -skew; d <= skew; d++ {
		step := cur + int64(d)
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(Code(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
