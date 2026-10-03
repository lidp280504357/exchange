package domain

import (
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Administrators set their own credentials (C5.5 ⑪): the console's create
// and resets give a one-time setup token instead of a password or an
// authenticator secret, so whoever creates or resets an account never
// knows what signs it in. The token is shown once; only its hash is kept.

// What a setup token sets up.
const (
	// SetupCreate is a new account: its password and its authenticator.
	SetupCreate = "CREATE"
	// SetupPassword is a password reset: the old one no longer signs in.
	SetupPassword = "PASSWORD"
	// SetupTOTP is an authenticator reset: the old one no longer works.
	SetupTOTP = "TOTP"
	// SetupSelfTOTP is a signed-in administrator's own new
	// authenticator, waiting for its first code (no token).
	SetupSelfTOTP = "SELF_TOTP"
)

// SetupTTL bounds a setup token; SelfTOTPTTL an authenticator of one's
// own waiting for its code; MinPassword is the least length of a password.
const (
	SetupTTL    = 24 * time.Hour
	SelfTOTPTTL = 10 * time.Minute
	MinPassword = 12
)

// Errors of the setup.
var (
	// ErrSetupInvalid refuses a setup token that is unknown, used or
	// expired: ask for another reset.
	ErrSetupInvalid = apperr.New(apperr.KindNotFound, "ADMIN_SETUP_INVALID", "the setup link is unknown, used or expired")
	// ErrPasswordChangeRequired holds an administrator whose password was
	// generated for them until they change it.
	ErrPasswordChangeRequired = apperr.New(apperr.KindForbidden, "ADMIN_PASSWORD_CHANGE_REQUIRED", "change your password first")
	// ErrWeakPassword refuses a password shorter than MinPassword.
	ErrWeakPassword = apperr.Invalid("the password needs at least 12 characters")
	// ErrTOTPCodeWrong refuses an authenticator code that does not match
	// (or was used already).
	ErrTOTPCodeWrong = apperr.New(apperr.KindUnprocessable, "ADMIN_TOTP_CODE_WRONG", "the authenticator code is wrong; check the app's clock")
)

// StartSetup waits for the administrator to set up kind with the token
// whose hash is given (a pending authenticator sealed, for those that
// bind one), until now plus ttl.
func (a *Admin) StartSetup(kind string, hash, pendingTOTP []byte, now time.Time, ttl time.Duration) {
	a.SetupKind, a.SetupHash, a.SetupTOTPSealed, a.SetupExpiresAt = kind, hash, pendingTOTP, now.Add(ttl)
}

// SetupLive reports whether a setup of kind waits at now.
func (a *Admin) SetupLive(kind string, now time.Time) bool {
	return a.SetupKind == kind && now.Before(a.SetupExpiresAt)
}

// ClearSetup ends a setup, done or replaced.
func (a *Admin) ClearSetup() {
	a.SetupKind, a.SetupHash, a.SetupTOTPSealed, a.SetupExpiresAt = "", nil, nil, time.Time{}
}

// NextSetup is the setup a reset of kind starts while a link of pending
// was never used (expired or not): what neither was given yet stays to
// set, so a reset of one factor keeps the other's to set too (both are
// CREATE's). An administrator's own pending authenticator gives way.
func NextSetup(pending, kind string) string {
	if pending == "" || pending == SetupSelfTOTP || pending == kind {
		return kind
	}
	return SetupCreate
}

// SetsPassword reports whether a setup of kind sets the password.
func SetsPassword(kind string) bool { return kind == SetupCreate || kind == SetupPassword }

// BindsTOTP reports whether a setup of kind binds an authenticator.
func BindsTOTP(kind string) bool {
	return kind == SetupCreate || kind == SetupTOTP || kind == SetupSelfTOTP
}
