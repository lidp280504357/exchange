// Package captcha provides human-verification adapters. Business code depends
// only on Verifier; concrete providers (Cloudflare Turnstile, a static test
// verifier) are wired in at startup so the provider can be swapped by config.
package captcha

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxTokenLength is the longest challenge token a client may submit.
const MaxTokenLength = 2048

var (
	// ErrMissingToken is returned when the client sent no challenge token.
	ErrMissingToken = errors.New("captcha: missing token")
	// ErrTokenTooLong is returned when the token exceeds MaxTokenLength.
	ErrTokenTooLong = errors.New("captcha: token exceeds maximum length")
)

// Request carries what the client submitted together with what the server
// expects. Action is optional; when set, the provider's reported action must
// match it exactly.
type Request struct {
	Token    string
	RemoteIP string
	Action   string
}

// Result describes an accepted challenge.
type Result struct {
	Hostname    string
	Action      string
	ChallengeAt time.Time
	CData       string
}

// VerificationError reports a rejected challenge. Reason is a stable,
// non-sensitive code suitable for logs and metrics; Codes holds the raw
// provider error codes when the provider returned any.
type VerificationError struct {
	Reason string
	Codes  []string
}

func (e *VerificationError) Error() string {
	if len(e.Codes) == 0 {
		return fmt.Sprintf("captcha: verification rejected (%s)", e.Reason)
	}
	return fmt.Sprintf("captcha: verification rejected (%s: %s)", e.Reason, strings.Join(e.Codes, ","))
}

// Reasons used by VerificationError. Provider error codes such as
// "invalid-input-response" are passed through unchanged.
const (
	ReasonHostnameMismatch    = "hostname-mismatch"
	ReasonActionMismatch      = "action-mismatch"
	ReasonProviderUnavailable = "provider-unavailable"
	ReasonRejected            = "verification-failed"
)

// Verifier validates a challenge token. Implementations must never log or
// return the provider secret.
type Verifier interface {
	Verify(ctx context.Context, req Request) (Result, error)
}

// validateToken applies the client-independent pre-checks.
func validateToken(token string) error {
	if strings.TrimSpace(token) == "" {
		return ErrMissingToken
	}
	if len(token) > MaxTokenLength {
		return ErrTokenTooLong
	}
	return nil
}
