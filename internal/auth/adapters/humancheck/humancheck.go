// Package humancheck adapts the platform captcha verifiers to auth's port.
package humancheck

import (
	"context"
	"crypto/subtle"
	"errors"

	"github.com/lidp280504357/exchange/internal/platform/captcha"
)

// Verifier checks Turnstile tokens. Outside production a configured bypass
// token also passes, so end-to-end tests can request codes without a
// browser (CAPTCHA_BYPASS_TOKEN); it must never be set in production.
type Verifier struct {
	provider captcha.Verifier
	bypass   string
}

// New returns a verifier; provider may be nil when only the bypass token
// is configured.
func New(provider captcha.Verifier, bypass string) *Verifier {
	return &Verifier{provider: provider, bypass: bypass}
}

// ErrNotConfigured means no captcha provider is set up.
var ErrNotConfigured = errors.New("human verification is not configured")

// Verify implements ports.Captcha.
func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) error {
	if v.bypass != "" && subtle.ConstantTimeCompare([]byte(token), []byte(v.bypass)) == 1 {
		return nil
	}
	if v.provider == nil {
		return ErrNotConfigured
	}
	_, err := v.provider.Verify(ctx, captcha.Request{Token: token, RemoteIP: remoteIP})
	return err
}
