package domain

import (
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
)

// ConsoleAccess is how the console lets administrators in (design
// 2026-10-02, N1): whether signing in asks for the authenticator code
// (admin.require_totp).
type ConsoleAccess struct {
	RequireTOTP bool
	UpdatedBy   string
	UpdatedAt   time.Time
}

// The access switches' refusals.
var (
	// ErrTOTPNotBound refuses asking for the code at sign-in before the
	// administrator switching it on and at least one active ADMIN have a
	// bound authenticator: nobody would be left to sign in.
	ErrTOTPNotBound = apperr.New(apperr.KindConflict, "ADMIN_TOTP_NOT_BOUND",
		"bind your authenticator on your account page first; at least one active ADMIN must have one bound")
	// ErrTOTPRequired refuses removing one's authenticator while sign-in
	// asks for its code.
	ErrTOTPRequired = apperr.New(apperr.KindConflict, "ADMIN_TOTP_REQUIRED",
		"sign-in asks for the authenticator code: an authenticator can be replaced, not removed")
)
