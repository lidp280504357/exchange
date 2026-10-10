package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/totp"
)

// accessRefresh is how often admin-service reads the console's access
// switches again: exchangectl's switch-off in its container takes effect
// within it.
const accessRefresh = 5 * time.Second

// The code switch's name (N1: the settings page, the launch checklist,
// exchangectl), and the audit of the access switches: their target and
// the code switch's action.
const (
	settingRequireTOTP = "admin.require_totp"
	accessTarget       = "settings:access"
	actionRequireTOTP  = "admin.settings.require_totp"
)

// TOTPRequired reports whether sign-in asks for the authenticator code
// (admin.require_totp, N1); until the switches are read, it does.
func (s *Service) TOTPRequired() bool {
	a := s.access.Load()
	return a == nil || a.RequireTOTP
}

// setAccess keeps the switches in effect.
func (s *Service) setAccess(a domain.ConsoleAccess) { s.access.Store(&a) }

// LoadAccess reads the console's access switches, storing them the first
// time it runs: sign-in asks for the code unless the flag it replaces,
// admin.login_without_totp, was on - and only once an active ADMIN has a
// bound authenticator, so that nobody is shut out (coordinator, N1).
// Audited when stored.
func (s *Service) LoadAccess(ctx context.Context) error {
	cur, err := s.Store.Read().Access().Get(ctx)
	if err != nil {
		return err
	}
	if cur == nil {
		if cur, err = s.carryOverAccess(ctx); err != nil {
			return err
		}
	}
	s.access.Store(cur)
	return nil
}

func (s *Service) carryOverAccess(ctx context.Context) (*domain.ConsoleAccess, error) {
	var out *domain.ConsoleAccess
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		admins, err := r.Admins().List(ctx)
		if err != nil {
			return err
		}
		without := s.Features != nil && s.Features.Enabled(flags.KeyAdminNoTOTP, flags.Subject{})
		bound := boundAdmins(admins)
		a := domain.ConsoleAccess{RequireTOTP: !without && bound > 0, UpdatedBy: "migration:" + flags.KeyAdminNoTOTP, UpdatedAt: s.Now()}
		stored, err := r.Access().Init(ctx, a)
		if err != nil {
			return err
		}
		if !stored {
			// Another admin-service stored them first.
			out, err = r.Access().Get(ctx)
			return err
		}
		out = &a
		why := "carried over from the flag " + flags.KeyAdminNoTOTP
		switch {
		case without:
			why += ", which was on"
		case bound == 0:
			why = "migration: no admin bound (" + why + ")"
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: accessTarget, Action: actionRequireTOTP, Actor: a.UpdatedBy, Reason: why,
			Details: fmt.Sprintf(`{"from":null,"to":%t,"bound_admins":%d}`, a.RequireTOTP, bound),
		}, a.UpdatedBy)
	})
	return out, err
}

// RunAccess reads the switches again every accessRefresh until ctx ends,
// keeping the last ones when they cannot be read.
func (s *Service) RunAccess(ctx context.Context) error {
	tick := time.NewTicker(accessRefresh)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		if err := s.LoadAccess(ctx); err != nil && ctx.Err() == nil {
			s.Log.WarnContext(ctx, "console access: read failed; keeping the last switches", "error", err)
		}
	}
}

// boundAdmins counts the active ADMINs with a bound authenticator.
func boundAdmins(admins []domain.Admin) int {
	n := 0
	for _, a := range admins {
		if a.Status == domain.StatusActive && a.Role == domain.RoleAdmin && a.TOTPBound() {
			n++
		}
	}
	return n
}

// AccessView is the console's access switches as the settings page shows
// them (N1): the switches and who changed them last; whether the caller's
// authenticator is bound and how many active ADMINs' are; the active
// administrators without one, who cannot sign in while the code is asked
// (named to those who manage administrators, counted for the others).
type AccessView struct {
	domain.ConsoleAccess
	YouBound     bool
	BoundAdmins  int
	Unbound      []domain.Admin
	UnboundCount int
}

// Access returns the console's access switches; every administrator may
// read them.
func (s *Service) Access(ctx context.Context, p Principal) (AccessView, error) {
	r := s.Store.Read()
	cur, err := r.Access().Get(ctx)
	if err != nil {
		return AccessView{}, err
	}
	if cur == nil {
		cur = &domain.ConsoleAccess{RequireTOTP: s.TOTPRequired()}
	}
	admins, err := r.Admins().List(ctx)
	if err != nil {
		return AccessView{}, err
	}
	out := AccessView{ConsoleAccess: *cur, BoundAdmins: boundAdmins(admins), Unbound: []domain.Admin{}}
	names := p.require(domain.PermAdminsManage) == nil
	for _, a := range admins {
		if a.Status != domain.StatusActive {
			continue
		}
		if a.ID == p.Admin.ID {
			out.YouBound = a.TOTPBound()
		}
		if !a.TOTPBound() {
			out.UnboundCount++
			if names {
				out.Unbound = append(out.Unbound, a)
			}
		}
	}
	return out, nil
}

// SetRequireTOTP switches whether sign-in asks for the authenticator code
// (admin.require_totp, N1), with a reason; ADMIN only. Switching it on
// needs the caller's authenticator bound and at least one active ADMIN's
// (ADMIN_TOTP_NOT_BOUND otherwise). Sessions already open stay open. As it
// is already, nothing changes and nothing is audited; otherwise audited as
// admin.settings.require_totp.
func (s *Service) SetRequireTOTP(ctx context.Context, p Principal, on bool, reason string) (AccessView, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return AccessView{}, err
	}
	if err := needReason(reason); err != nil {
		return AccessView{}, err
	}
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Access().GetForUpdate(ctx)
		if err != nil {
			return err
		}
		from := cur == nil || cur.RequireTOTP
		if cur != nil && from == on {
			return nil
		}
		if on {
			admins, err := r.Admins().List(ctx)
			if err != nil {
				return err
			}
			you := false
			for _, a := range admins {
				if a.ID == p.Admin.ID && a.Status == domain.StatusActive && a.TOTPBound() {
					you = true
				}
			}
			if bound := boundAdmins(admins); !you || bound == 0 {
				return domain.ErrTOTPNotBound.WithDetail("you_bound", you).WithDetail("bound_admins", bound)
			}
		}
		next := domain.ConsoleAccess{RequireTOTP: on, UpdatedBy: p.Admin.Email, UpdatedAt: s.Now()}
		if err := r.Access().Put(ctx, next); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: accessTarget, Action: actionRequireTOTP, Actor: p.Admin.Email, Reason: strings.TrimSpace(reason),
			Details: fmt.Sprintf(`{"from":%t,"to":%t}`, from, on),
		}, p.Admin.Email)
	})
	if err != nil {
		return AccessView{}, err
	}
	if err := s.LoadAccess(ctx); err != nil {
		s.Log.WarnContext(ctx, "console access: read after a change failed; the next refresh takes it", "error", err)
	}
	return s.Access(ctx, p)
}

// SwitchOffRequireTOTP stops sign-in asking for the authenticator code:
// exchangectl's way in admin-service's container when nobody can sign in
// (N1); admin-service reads it within accessRefresh. Audited as
// admin.settings.require_totp; reports whether it was on.
func SwitchOffRequireTOTP(ctx context.Context, store ports.Store, actor, reason string, now time.Time) (bool, error) {
	if err := needReason(reason); err != nil {
		return false, err
	}
	changed := false
	err := store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Access().GetForUpdate(ctx)
		if err != nil {
			return err
		}
		if cur != nil && !cur.RequireTOTP {
			return nil
		}
		if err := r.Access().Put(ctx, domain.ConsoleAccess{RequireTOTP: false, UpdatedBy: actor, UpdatedAt: now}); err != nil {
			return err
		}
		changed = true
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: accessTarget, Action: actionRequireTOTP, Actor: actor, Reason: strings.TrimSpace(reason),
			Details: `{"from":true,"to":false,"via":"exchangectl"}`,
		}, actor)
	})
	return changed, err
}

// RemoveOwnTOTP unbinds the signed-in administrator's authenticator while
// sign-in does not ask for its code (ADMIN_TOTP_REQUIRED otherwise): the
// current password and, when it is bound, its code prove it is them. A
// new secret nobody holds takes its place, so the account signs in with
// the code again only after an authenticator is bound anew. Audited as
// admin.totp_removed.
func (s *Service) RemoveOwnTOTP(ctx context.Context, p Principal, current, code string) error {
	if s.TOTPRequired() {
		return domain.ErrTOTPRequired
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
		bound := a.TOTPBound()
		if bound {
			secret, err := s.Box.Open(a.TOTPSealed, []byte(a.ID))
			if err != nil {
				return err
			}
			if _, ok := totp.Verify(secret, strings.TrimSpace(code), s.Now(), a.TOTPLastStep); !ok {
				return domain.ErrTOTPCodeWrong
			}
		}
		a.TOTPSealed, a.TOTPLastStep, a.TOTPConfirmedAt = s.Box.Seal(totp.NewSecret(), []byte(a.ID)), 0, time.Time{}
		if a.SetupKind == domain.SetupSelfTOTP {
			a.ClearSetup()
		}
		if err := r.Admins().Update(ctx, *a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "admin:" + a.ID, Action: "admin.totp_removed", Actor: a.Email, Reason: "removed by the administrator",
			Details: fmt.Sprintf(`{"was_bound":%t}`, bound),
		}, a.Email)
	})
}
