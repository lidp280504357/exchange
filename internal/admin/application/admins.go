package application

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/totp"
)

// The administrators and their roles (design 2026-10-02 §4.6, C4):
// created with a password and an authenticator secret shown once, roles
// changed, disabled and enabled, passwords and authenticators reset,
// sessions ended. exchangectl admin stays the way in when no ADMIN can
// sign in. Nobody changes their own account here, and an active ADMIN
// always remains.

// totpIssuer names the console in authenticator apps.
const totpIssuer = "Exchange Admin"

// AdminView is an administrator as the console lists them.
type AdminView struct {
	domain.Admin
	// Sessions counts the live sessions.
	Sessions int
}

// Credentials are shown once, never kept in the clear: a new password, a
// new authenticator secret (base32) with its otpauth URI. Either may be
// empty.
type Credentials struct {
	Password   string
	TOTPSecret string
	TOTPURI    string
}

// RoleView is a role with its permissions.
type RoleView struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

// Roles lists every role with its permissions (any administrator reads
// them).
func (s *Service) Roles() []RoleView {
	out := []RoleView{}
	for _, r := range domain.Roles() {
		out = append(out, RoleView{Role: r, Permissions: domain.Permissions(r)})
	}
	return out
}

// Admins lists the administrators with their live sessions.
func (s *Service) Admins(ctx context.Context, p Principal) ([]AdminView, error) {
	if err := p.require(domain.PermAdminsManage); err != nil {
		return nil, err
	}
	r := s.Store.Read()
	list, err := r.Admins().List(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	out := make([]AdminView, 0, len(list))
	for _, a := range list {
		live, err := r.Sessions().Live(ctx, a.ID, now)
		if err != nil {
			return nil, err
		}
		out = append(out, AdminView{Admin: a, Sessions: len(live)})
	}
	return out, nil
}

// newPassword draws a password of 24 URL-safe characters.
func newPassword() string {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// CreateAdmin creates an administrator; the password and authenticator
// secret come back once.
func (s *Service) CreateAdmin(ctx context.Context, p Principal, email, name, role, reason string) (domain.Admin, Credentials, error) {
	if err := p.require(domain.PermAdminsManage); err != nil {
		return domain.Admin{}, Credentials{}, err
	}
	if err := needReason(reason); err != nil {
		return domain.Admin{}, Credentials{}, err
	}
	pw, secret := newPassword(), totp.NewSecret()
	a, err := createAdmin(ctx, s.Store, s.Hasher, s.Box, email, name, strings.ToUpper(strings.TrimSpace(role)), pw, secret, p.Admin.Email,
		strings.TrimSpace(reason), s.Now())
	if err != nil {
		return domain.Admin{}, Credentials{}, err
	}
	return a, Credentials{Password: pw, TOTPSecret: totp.Encode(secret), TOTPURI: totp.URI(totpIssuer, a.Email, secret)}, nil
}

// changeAdmin locks another administrator's row, applies change and
// audits action; target refuses changing oneself.
func (s *Service) changeAdmin(ctx context.Context, p Principal, id, reason, action string, change func(r ports.Repos, a *domain.Admin) (string, error)) (domain.Admin, error) {
	if err := p.require(domain.PermAdminsManage); err != nil {
		return domain.Admin{}, err
	}
	if err := needReason(reason); err != nil {
		return domain.Admin{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return domain.Admin{}, apperr.NotFound("no such administrator")
	}
	if id == p.Admin.ID {
		return domain.Admin{}, domain.ErrSelf
	}
	var out domain.Admin
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		a, err := r.Admins().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if a == nil {
			return apperr.NotFound("no such administrator")
		}
		details, err := change(r, a)
		if err != nil {
			return err
		}
		if err := r.Admins().Update(ctx, *a); err != nil {
			return err
		}
		out = *a
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + a.ID, Action: action, Actor: p.Admin.Email, Reason: strings.TrimSpace(reason), Details: details,
		}, p.Admin.Email)
	})
	return out, err
}

// lastAdmin reports whether a is the only active ADMIN.
func lastAdmin(ctx context.Context, r ports.Repos, a *domain.Admin) (bool, error) {
	if a.Role != domain.RoleAdmin || a.Status != domain.StatusActive {
		return false, nil
	}
	list, err := r.Admins().List(ctx)
	if err != nil {
		return false, err
	}
	for _, x := range list {
		if x.ID != a.ID && x.Role == domain.RoleAdmin && x.Status == domain.StatusActive {
			return false, nil
		}
	}
	return true, nil
}

// SetAdminStatus disables an administrator (their sessions end) or
// enables them again.
func (s *Service) SetAdminStatus(ctx context.Context, p Principal, id string, enable bool, reason string) (domain.Admin, error) {
	action := "admin.disabled"
	if enable {
		action = "admin.enabled"
	}
	return s.changeAdmin(ctx, p, id, reason, action, func(r ports.Repos, a *domain.Admin) (string, error) {
		if enable {
			a.Status, a.FailedAttempts, a.LockedUntil = domain.StatusActive, 0, time.Time{}
			return "{}", nil
		}
		if last, err := lastAdmin(ctx, r, a); err != nil || last {
			if last {
				return "", domain.ErrLastAdmin
			}
			return "", err
		}
		a.Status = domain.StatusDisabled
		return "{}", r.Sessions().RevokeAll(ctx, a.ID, s.Now())
	})
}

// SetAdminRole changes an administrator's role; it holds from their next
// request.
func (s *Service) SetAdminRole(ctx context.Context, p Principal, id, role, reason string) (domain.Admin, error) {
	role = strings.ToUpper(strings.TrimSpace(role))
	if !domain.ValidRole(role) {
		return domain.Admin{}, apperr.Invalid(fmt.Sprintf("unknown role %q (ADMIN, OPERATOR, FINANCE, AUDITOR)", role))
	}
	return s.changeAdmin(ctx, p, id, reason, "admin.role_changed", func(r ports.Repos, a *domain.Admin) (string, error) {
		if a.Role == role {
			return "", apperr.New(apperr.KindConflict, apperr.CodeConflict, "the administrator already has this role")
		}
		if role != domain.RoleAdmin {
			if last, err := lastAdmin(ctx, r, a); err != nil || last {
				if last {
					return "", domain.ErrLastAdmin
				}
				return "", err
			}
		}
		from := a.Role
		a.Role = role
		return fmt.Sprintf(`{"from":%q,"to":%q}`, from, role), nil
	})
}

// ResetAdminPassword gives an administrator a new password (shown once)
// and ends their sessions; a lock from failed sign-ins is lifted.
func (s *Service) ResetAdminPassword(ctx context.Context, p Principal, id, reason string) (Credentials, error) {
	pw := newPassword()
	_, err := s.changeAdmin(ctx, p, id, reason, "admin.password_reset", func(r ports.Repos, a *domain.Admin) (string, error) {
		a.PasswordHash, a.FailedAttempts, a.LockedUntil = s.Hasher.Hash(pw), 0, time.Time{}
		return "{}", r.Sessions().RevokeAll(ctx, a.ID, s.Now())
	})
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Password: pw}, nil
}

// ResetAdminTOTP gives an administrator a new authenticator secret (shown
// once) and ends their sessions.
func (s *Service) ResetAdminTOTP(ctx context.Context, p Principal, id, reason string) (Credentials, error) {
	secret := totp.NewSecret()
	var email string
	_, err := s.changeAdmin(ctx, p, id, reason, "admin.totp_reset", func(r ports.Repos, a *domain.Admin) (string, error) {
		a.TOTPSealed, a.TOTPLastStep, email = s.Box.Seal(secret, []byte(a.ID)), 0, a.Email
		return "{}", r.Sessions().RevokeAll(ctx, a.ID, s.Now())
	})
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{TOTPSecret: totp.Encode(secret), TOTPURI: totp.URI(totpIssuer, email, secret)}, nil
}

// AdminSessions lists an administrator's live sessions.
func (s *Service) AdminSessions(ctx context.Context, p Principal, id string) ([]domain.Session, error) {
	if err := p.require(domain.PermAdminsManage); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such administrator")
	}
	return s.Store.Read().Sessions().Live(ctx, id, s.Now())
}

// RevokeAdminSessions ends every session of another administrator.
func (s *Service) RevokeAdminSessions(ctx context.Context, p Principal, id, reason string) error {
	_, err := s.changeAdmin(ctx, p, id, reason, "admin.sessions_revoked", func(r ports.Repos, a *domain.Admin) (string, error) {
		return "{}", r.Sessions().RevokeAll(ctx, a.ID, s.Now())
	})
	return err
}
