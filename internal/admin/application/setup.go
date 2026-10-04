package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/totp"
)

// Administrators set their own credentials (C5.5 ⑪). A new account and
// a reset give a one-time setup link (a day): its holder sets the
// password, binds the authenticator, or both; whoever created or reset
// the account hands the link over and never learns what signs it in. A
// signed-in administrator changes their own password and authenticator;
// one whose password was generated for them (exchangectl admin create)
// changes it before anything else.

// setupAAD seals an authenticator that waits for its setup, apart from
// the one that signs the account in.
func setupAAD(a *domain.Admin) []byte { return []byte("setup:" + a.ID) }

// startSetup makes a setup link of kind for a, binding a new
// authenticator when the kind does.
func (s *Service) startSetup(a *domain.Admin, kind string) Setup {
	token, hash := domain.NewToken()
	var pending []byte
	if domain.BindsTOTP(kind) {
		pending = s.Box.Seal(totp.NewSecret(), setupAAD(a))
	}
	a.StartSetup(kind, hash, pending, s.Now(), domain.SetupTTL)
	return Setup{Token: token, Kind: kind, ExpiresAt: a.SetupExpiresAt}
}

// SetupView is what a setup link sets up: the account, and the new
// authenticator to bind when it binds one.
type SetupView struct {
	Email      string
	Name       string
	Kind       string
	ExpiresAt  time.Time
	TOTPSecret string
	TOTPURI    string
}

// setupOf finds the administrator a live setup token belongs to.
func (s *Service) setupOf(ctx context.Context, r ports.Repos, token string) (*domain.Admin, error) {
	if token == "" {
		return nil, domain.ErrSetupInvalid
	}
	a, err := r.Admins().BySetupForUpdate(ctx, domain.HashToken(token))
	if err != nil {
		return nil, err
	}
	if a == nil || a.Status != domain.StatusActive || a.SetupKind == domain.SetupSelfTOTP || !a.SetupLive(a.SetupKind, s.Now()) {
		return nil, domain.ErrSetupInvalid
	}
	return a, nil
}

// InspectSetup shows what a setup link sets up, with the authenticator to
// bind (to its holder alone: the link is theirs).
func (s *Service) InspectSetup(ctx context.Context, token string) (SetupView, error) {
	var out SetupView
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		a, err := s.setupOf(ctx, r, token)
		if err != nil {
			return err
		}
		out = SetupView{Email: a.Email, Name: a.Name, Kind: a.SetupKind, ExpiresAt: a.SetupExpiresAt}
		if domain.BindsTOTP(a.SetupKind) {
			secret, err := s.Box.Open(a.SetupTOTPSealed, setupAAD(a))
			if err != nil {
				return err
			}
			out.TOTPSecret, out.TOTPURI = totp.Encode(secret), totp.URI(totpIssuer, a.Email, secret)
		}
		return nil
	})
	return out, err
}

// CompleteSetup sets what a setup link sets up: the password (at least
// 12 characters) and the authenticator, proven by its current code. The
// link is spent; the account then signs in as usual. Audited as
// admin.setup_completed in the administrator's name, with the address it
// came from.
func (s *Service) CompleteSetup(ctx context.Context, token, pw, code, ip string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		a, err := s.setupOf(ctx, r, token)
		if err != nil {
			return err
		}
		kind := a.SetupKind
		if domain.SetsPassword(kind) {
			if len(pw) < domain.MinPassword {
				return domain.ErrWeakPassword
			}
			a.PasswordHash, a.MustChangePassword = s.Hasher.Hash(pw), false
		}
		if domain.BindsTOTP(kind) {
			if err := s.bindPending(a, code); err != nil {
				return err
			}
		}
		a.ClearSetup()
		a.FailedAttempts, a.LockedUntil = 0, time.Time{}
		if err := r.Admins().Update(ctx, *a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + a.ID, Action: "admin.setup_completed", Actor: a.Email, Reason: "set up from a one-time link",
			Details: fmt.Sprintf(`{"kind":%q,"ip":%q}`, kind, ip),
		}, a.Email)
	})
}

// bindPending makes the authenticator waiting in a's setup the one that
// signs it in, once code proves it.
func (s *Service) bindPending(a *domain.Admin, code string) error {
	secret, err := s.Box.Open(a.SetupTOTPSealed, setupAAD(a))
	if err != nil {
		return err
	}
	step, ok := totp.Verify(secret, strings.TrimSpace(code), s.Now(), 0)
	if !ok {
		return domain.ErrTOTPCodeWrong
	}
	a.TOTPSealed, a.TOTPLastStep = s.Box.Seal(secret, []byte(a.ID)), step
	return nil
}

// verifyOwn checks a signed-in administrator's current password.
func (s *Service) verifyOwn(a *domain.Admin, pw string) error {
	ok, err := s.Hasher.Verify(a.PasswordHash, pw)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(apperr.KindUnprocessable, "ADMIN_PASSWORD_WRONG", "the current password is wrong")
	}
	return nil
}

// ChangeOwnPassword changes the signed-in administrator's password: the
// current one proves it is them; their other sessions end. Audited as
// admin.password_changed.
func (s *Service) ChangeOwnPassword(ctx context.Context, p Principal, current, next string) error {
	if len(next) < domain.MinPassword {
		return domain.ErrWeakPassword
	}
	if next == current {
		return apperr.Invalid("the new password is the current one")
	}
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		a, err := r.Admins().GetForUpdate(ctx, p.Admin.ID)
		if err != nil {
			return err
		}
		if a == nil {
			return domain.ErrUnauthorized
		}
		if err := s.verifyOwn(a, current); err != nil {
			return err
		}
		forced := a.MustChangePassword
		a.PasswordHash, a.MustChangePassword = s.Hasher.Hash(next), false
		if err := r.Admins().Update(ctx, *a); err != nil {
			return err
		}
		if err := r.Sessions().RevokeOthers(ctx, a.ID, p.Session, s.Now()); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + a.ID, Action: "admin.password_changed", Actor: a.Email, Reason: "changed by the administrator",
			Details: fmt.Sprintf(`{"required":%t}`, forced),
		}, a.Email)
	})
}

// StartOwnTOTP gives the signed-in administrator a new authenticator to
// bind (ConfirmOwnTOTP, within 10 minutes); the current password and,
// while sign-in asks for it, the current authenticator's code prove it is
// them (a session and a password alone do not move the account to another
// authenticator). The old one signs in until then.
func (s *Service) StartOwnTOTP(ctx context.Context, p Principal, current, code string) (secret, uri string, err error) {
	checkCode := s.TOTPRequired()
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		a, err := r.Admins().GetForUpdate(ctx, p.Admin.ID)
		if err != nil {
			return err
		}
		if a == nil {
			return domain.ErrUnauthorized
		}
		if err := s.verifyOwn(a, current); err != nil {
			return err
		}
		if checkCode {
			old, err := s.Box.Open(a.TOTPSealed, []byte(a.ID))
			if err != nil {
				return err
			}
			step, ok := totp.Verify(old, strings.TrimSpace(code), s.Now(), a.TOTPLastStep)
			if !ok {
				return domain.ErrTOTPCodeWrong
			}
			a.TOTPLastStep = step
		}
		if a.SetupKind != "" && a.SetupKind != domain.SetupSelfTOTP {
			return apperr.New(apperr.KindConflict, apperr.CodeConflict, "a setup link waits for this account: use it first")
		}
		raw := totp.NewSecret()
		a.StartSetup(domain.SetupSelfTOTP, nil, s.Box.Seal(raw, setupAAD(a)), s.Now(), domain.SelfTOTPTTL)
		secret, uri = totp.Encode(raw), totp.URI(totpIssuer, a.Email, raw)
		return r.Admins().Update(ctx, *a)
	})
	return secret, uri, err
}

// ConfirmOwnTOTP binds the authenticator StartOwnTOTP gave, proven by its
// code; the administrator's other sessions end. Audited as
// admin.totp_changed.
func (s *Service) ConfirmOwnTOTP(ctx context.Context, p Principal, code string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		a, err := r.Admins().GetForUpdate(ctx, p.Admin.ID)
		if err != nil {
			return err
		}
		if a == nil {
			return domain.ErrUnauthorized
		}
		if !a.SetupLive(domain.SetupSelfTOTP, s.Now()) {
			return apperr.New(apperr.KindConflict, apperr.CodeConflict, "start again: no new authenticator waits, or it expired")
		}
		if err := s.bindPending(a, code); err != nil {
			return err
		}
		a.ClearSetup()
		if err := r.Admins().Update(ctx, *a); err != nil {
			return err
		}
		if err := r.Sessions().RevokeOthers(ctx, a.ID, p.Session, s.Now()); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + a.ID, Action: "admin.totp_changed", Actor: a.Email, Reason: "changed by the administrator", Details: "{}",
		}, a.Email)
	})
}
