package application

import (
	"context"
	"time"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/auth/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pii"
	"github.com/skill/exchange/internal/platform/secretbox"
)

// Authenticator apps (TOTP, requirements §6.5). Once bound, a step-up is
// proven with the app, not with a code by mail or SMS: TOTP comes first in
// the step-up order and makes a taken-over mailbox or SIM not enough.

// TOTPIssuer names the exchange in authenticator apps.
const TOTPIssuer = "Astras"

// ErrTOTPUnavailable means TOTP_SECRET_KEY is not configured.
var ErrTOTPUnavailable = apperr.New(apperr.KindUnavailable, "AUTH_TOTP_UNAVAILABLE", "authenticator apps are not available")

func (s *AccountService) box() (*secretbox.Box, error) {
	if s.TOTP == nil {
		return nil, ErrTOTPUnavailable
	}
	return s.TOTP, nil
}

// TOTPState is whether an authenticator app is bound, or being set up.
type TOTPState struct {
	Enabled bool
	Pending bool
}

// TOTPStatus reports the user's authenticator binding.
func (s *AccountService) TOTPStatus(ctx context.Context, userID string) (TOTPState, error) {
	t, err := s.Store.Read().TOTP().Get(ctx, userID)
	if err != nil || t == nil {
		return TOTPState{}, err
	}
	return TOTPState{Enabled: t.Status == domain.TOTPActive, Pending: t.Status == domain.TOTPPending}, nil
}

// activeTOTP returns the user's bound secret, or nil when none is bound.
func (s *AccountService) activeTOTP(ctx context.Context, r ports.Repos, userID string) (*ports.SealedTOTP, []byte, error) {
	t, err := r.TOTP().GetForUpdate(ctx, userID)
	if err != nil || t == nil || t.Status != domain.TOTPActive {
		return nil, nil, err
	}
	box, err := s.box()
	if err != nil {
		return nil, nil, err
	}
	secret, err := box.Open(t.Sealed, []byte(userID))
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	return t, secret, nil
}

// SetupTOTP starts binding an authenticator app after a step-up: it returns
// the secret to type in and the otpauth URI for a QR code. The binding
// takes effect once ConfirmTOTP gets a code from the app; setting up again
// replaces a pending secret.
func (s *AccountService) SetupTOTP(ctx context.Context, userID, stepUp string) (string, string, error) {
	box, err := s.box()
	if err != nil {
		return "", "", err
	}
	now := s.Now()
	secret := domain.NewTOTPSecret()
	var account string
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if _, err := s.consumeStepUp(ctx, r, userID, stepUp); err != nil {
			return err
		}
		t, err := r.TOTP().GetForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if t != nil && t.Status == domain.TOTPActive {
			return domain.ErrTOTPEnabled
		}
		ids, err := r.Identities().ByUser(ctx, userID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if account == "" || id.Kind == domain.ChannelEmail.Kind() {
				account = pii.MaskIdentifier(id.Value)
			}
		}
		return r.TOTP().Put(ctx, ports.SealedTOTP{
			UserID: userID, Sealed: box.Seal(secret, []byte(userID)), Status: domain.TOTPPending, CreatedAt: now,
		})
	})
	if err != nil {
		return "", "", err
	}
	return domain.EncodeTOTPSecret(secret), domain.TOTPURI(TOTPIssuer, account, secret), nil
}

// ConfirmTOTP binds the pending authenticator app with a first code.
func (s *AccountService) ConfirmTOTP(ctx context.Context, userID, code string) error {
	box, err := s.box()
	if err != nil {
		return err
	}
	now := s.Now()
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		t, err := r.TOTP().GetForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if t == nil || t.Status != domain.TOTPPending {
			return domain.ErrTOTPNotPending
		}
		secret, err := box.Open(t.Sealed, []byte(userID))
		if err != nil {
			return apperr.Internal(err)
		}
		step, err := domain.VerifyTOTP(secret, code, now, t.LastStep)
		if err != nil {
			return err
		}
		t.Status, t.LastStep, t.ActivatedAt = domain.TOTPActive, step, now
		if err := r.TOTP().Put(ctx, *t); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.TotpEnabled{UserId: userID}, "user", userID)
	})
}

// DisableTOTP removes the authenticator app. The step-up must itself have
// been proven with the app.
func (s *AccountService) DisableTOTP(ctx context.Context, userID, stepUp string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		su, err := s.consumeStepUp(ctx, r, userID, stepUp)
		if err != nil {
			return err
		}
		t, err := r.TOTP().GetForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if t == nil || t.Status != domain.TOTPActive {
			return domain.ErrTOTPNotEnabled
		}
		if su.Channel != domain.ChannelTOTP {
			return domain.ErrTOTPRequired
		}
		if err := r.TOTP().Delete(ctx, userID); err != nil {
			return err
		}
		// Withdrawals wait for review for a day after it (C5.5 ⑤).
		if err := r.Credentials().TOTPChanged(ctx, userID, s.Now()); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.TotpDisabled{UserId: userID}, "user", userID)
	})
}

// StepUpTOTP proves a step-up with the authenticator app: a token valid
// 10 minutes for one sensitive action (§6.5).
func (s *AccountService) StepUpTOTP(ctx context.Context, userID, sessionID, code string) (string, time.Time, error) {
	now := s.Now()
	plain, hash := domain.NewToken()
	expires := now.Add(domain.StepUpTokenTTL)
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		t, secret, err := s.activeTOTP(ctx, r, userID)
		if err != nil {
			return err
		}
		if t == nil {
			return domain.ErrTOTPNotEnabled
		}
		step, err := domain.VerifyTOTP(secret, code, now, t.LastStep)
		if err != nil {
			return err
		}
		t.LastStep = step
		if err := r.TOTP().Put(ctx, *t); err != nil {
			return err
		}
		return r.StepUps().Create(ctx, domain.StepUp{Hash: hash, UserID: userID, SessionID: sessionID, Channel: domain.ChannelTOTP, ExpiresAt: expires})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return plain, expires, nil
}
