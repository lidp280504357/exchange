package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/totp"
)

// accessRefresh is how often admin-service reads the console's access
// switches again: exchangectl's switch-off in its container takes effect
// within it.
const accessRefresh = 5 * time.Second

// The switches' names (N1: the settings page, the launch checklist,
// exchangectl), and their audit: the target and each switch's action.
const (
	settingRequireTOTP = "admin.require_totp"
	settingRestriction = "admin.access_restriction"
	accessTarget       = "settings:access"
	actionRequireTOTP  = "admin.settings.require_totp"
	actionRestriction  = "admin.settings.access_restriction"
)

// TOTPRequired reports whether sign-in asks for the authenticator code
// (admin.require_totp, N1); until the switches are read, it does.
func (s *Service) TOTPRequired() bool {
	a := s.access.Load()
	return a == nil || a.RequireTOTP
}

// setAccess keeps the switches in effect.
func (s *Service) setAccess(a domain.ConsoleAccess) { s.access.Store(&a) }

// Allows reports whether a request from ip (the client's address as the
// proxies passed it on) reaches the console's API: the restriction's list
// when it is on (admin.access_restriction, N1).
func (s *Service) Allows(ip string) bool {
	a := s.access.Load()
	if a == nil {
		return true // before the switches are read, nothing is restricted
	}
	addr, _ := netip.ParseAddr(ip)
	return a.Allows(addr)
}

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
// (named to those who manage administrators, counted for the others); the
// address the caller's request came from, as the restriction sees it.
type AccessView struct {
	domain.ConsoleAccess
	YouBound     bool
	BoundAdmins  int
	Unbound      []domain.Admin
	UnboundCount int
	YourIP       string
}

// Access returns the console's access switches, for a request from ip;
// every administrator may read them.
func (s *Service) Access(ctx context.Context, p Principal, ip string) (AccessView, error) {
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
	out := AccessView{ConsoleAccess: *cur, BoundAdmins: boundAdmins(admins), Unbound: []domain.Admin{}, YourIP: ip}
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
func (s *Service) SetRequireTOTP(ctx context.Context, p Principal, on bool, ip, reason string) (AccessView, error) {
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
		next := changed(cur, p.Admin.Email, s.Now())
		next.RequireTOTP = on
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
	return s.accessChanged(ctx, p, ip)
}

// changed is the switches to store in place of cur (nil before the first
// row: the defaults, the code asked), changed by who at now.
func changed(cur *domain.ConsoleAccess, who string, now time.Time) domain.ConsoleAccess {
	next := domain.ConsoleAccess{RequireTOTP: true}
	if cur != nil {
		next = *cur
	}
	next.UpdatedBy, next.UpdatedAt = who, now
	return next
}

// accessChanged takes a change in effect here at once (the other
// instances read it within accessRefresh) and answers with the switches.
func (s *Service) accessChanged(ctx context.Context, p Principal, ip string) (AccessView, error) {
	if err := s.LoadAccess(ctx); err != nil {
		s.Log.WarnContext(ctx, "console access: read after a change failed; the next refresh takes it", "error", err)
	}
	return s.Access(ctx, p, ip)
}

// SetAccessRestriction sets the addresses the console's API answers
// (admin.access_restriction, N1), with a reason; ADMIN only. On, the list
// has 1 to MaxAllowlist entries (IPv4 or IPv6 addresses or CIDR prefixes)
// and must hold ip, the caller's own address (ADMIN_ACCESS_SELF_LOCKOUT
// with the detail ip otherwise); off keeps the list given (or the one
// stored when none is). A request from elsewhere is refused at once
// (ADMIN_ACCESS_DENIED), sign-in included; the other instances follow
// within accessRefresh. As it is, nothing changes; otherwise audited as
// admin.settings.access_restriction with the lists before and after.
func (s *Service) SetAccessRestriction(ctx context.Context, p Principal, on bool, list []string, ip, reason string) (AccessView, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return AccessView{}, err
	}
	if err := needReason(reason); err != nil {
		return AccessView{}, err
	}
	var allow []netip.Prefix
	if list != nil {
		var err error
		if allow, err = domain.ParseAllowlist(list); err != nil {
			return AccessView{}, err
		}
	}
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Access().GetForUpdate(ctx)
		if err != nil {
			return err
		}
		next := changed(cur, p.Admin.Email, s.Now())
		before := next
		next.Restricted = on
		if list != nil {
			next.Allowlist = allow
		}
		if on {
			if len(next.Allowlist) == 0 {
				return apperr.Invalid("the restriction needs at least one address")
			}
			addr, _ := netip.ParseAddr(ip)
			if !next.Allows(addr) {
				return domain.ErrAccessSelfLockout.WithDetail("ip", ip)
			}
		}
		if cur != nil && before.Restricted == next.Restricted && slices.Equal(before.Allowlist, next.Allowlist) {
			return nil
		}
		if err := r.Access().Put(ctx, next); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{
			"from": map[string]any{"enabled": before.Restricted, "allowlist": domain.AllowlistText(before.Allowlist)},
			"to":   map[string]any{"enabled": next.Restricted, "allowlist": domain.AllowlistText(next.Allowlist)},
			"ip":   ip,
		})
		if err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: accessTarget, Action: actionRestriction, Actor: p.Admin.Email, Reason: strings.TrimSpace(reason), Details: string(details),
		}, p.Admin.Email)
	})
	if err != nil {
		return AccessView{}, err
	}
	return s.accessChanged(ctx, p, ip)
}

// SwitchOffAccessRestriction lets every address reach the console's API
// again: exchangectl's way in admin-service's container when nobody's
// address is in the list (N1); admin-service reads it within
// accessRefresh. The list stays. Audited as
// admin.settings.access_restriction; reports whether it was on.
func SwitchOffAccessRestriction(ctx context.Context, store ports.Store, actor, reason string, now time.Time) (bool, error) {
	if err := needReason(reason); err != nil {
		return false, err
	}
	was := false
	err := store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Access().GetForUpdate(ctx)
		if err != nil || cur == nil || !cur.Restricted {
			return err
		}
		next := changed(cur, actor, now)
		next.Restricted = false
		if err := r.Access().Put(ctx, next); err != nil {
			return err
		}
		was = true
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: accessTarget, Action: actionRestriction, Actor: actor, Reason: strings.TrimSpace(reason),
			Details: `{"from":{"enabled":true},"to":{"enabled":false},"via":"exchangectl"}`,
		}, actor)
	})
	return was, err
}

// SwitchOffRequireTOTP stops sign-in asking for the authenticator code:
// exchangectl's way in admin-service's container when nobody can sign in
// (N1); admin-service reads it within accessRefresh. Audited as
// admin.settings.require_totp; reports whether it was on.
func SwitchOffRequireTOTP(ctx context.Context, store ports.Store, actor, reason string, now time.Time) (bool, error) {
	if err := needReason(reason); err != nil {
		return false, err
	}
	was := false
	err := store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Access().GetForUpdate(ctx)
		if err != nil {
			return err
		}
		if cur != nil && !cur.RequireTOTP {
			return nil
		}
		next := changed(cur, actor, now)
		next.RequireTOTP = false
		if err := r.Access().Put(ctx, next); err != nil {
			return err
		}
		was = true
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: accessTarget, Action: actionRequireTOTP, Actor: actor, Reason: strings.TrimSpace(reason),
			Details: `{"from":true,"to":false,"via":"exchangectl"}`,
		}, actor)
	})
	return was, err
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
