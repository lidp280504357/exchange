package application

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/totp"
)

// The first start stores the code switch from the flag it replaces (N1):
// on unless admin.login_without_totp was on, and only once an active
// ADMIN's authenticator is bound (coordinator); audited once.
func TestAccessCarriedOver(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		without, bound bool
		want           bool
		why            string
	}{
		"the flag on":      {without: true, bound: true, want: false, why: "which was on"},
		"nobody bound":     {want: false, why: "migration: no admin bound"},
		"an ADMIN bound":   {bound: true, want: true, why: "carried over from the flag admin.login_without_totp"},
		"flag on, unbound": {without: true, want: false, why: "which was on"},
	} {
		h := newHarness(t)
		h.admin(t, "boss@example.com", domain.RoleAdmin)
		if c.bound {
			a := h.store.admins[h.idOf("boss@example.com")]
			a.TOTPConfirmedAt = h.now
			h.store.admins[a.ID] = a
		}
		h.svc.Features = onFlags{flags.KeyAdminNoTOTP: c.without}
		if err := h.svc.LoadAccess(ctx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h.svc.TOTPRequired() != c.want || h.store.access == nil || h.store.access.RequireTOTP != c.want ||
			h.store.access.UpdatedBy != "migration:admin.login_without_totp" {
			t.Fatalf("%s: required %t, stored %+v", name, h.svc.TOTPRequired(), h.store.access)
		}
		last := h.store.audits[len(h.store.audits)-1]
		if last.GetAction() != "admin.settings.require_totp" || last.GetTarget() != "settings:access" || !strings.Contains(last.GetReason(), c.why) {
			t.Fatalf("%s: audit %v", name, last)
		}
		// Read again, not carried over again; the flag no longer counts.
		h.svc.Features = onFlags{flags.KeyAdminNoTOTP: !c.without}
		audits := len(h.store.audits)
		if err := h.svc.LoadAccess(ctx); err != nil || h.svc.TOTPRequired() != c.want || len(h.store.audits) != audits {
			t.Fatalf("%s: read again %t %v", name, h.svc.TOTPRequired(), err)
		}
	}
}

// The code switch on the settings page (N1): ADMIN only, with a reason;
// on once the caller's authenticator and an active ADMIN's are bound;
// off from exchangectl in the container; an authenticator removed only
// while it is off.
func TestRequireTOTPSwitch(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.store.access = &domain.ConsoleAccess{RequireTOTP: false, UpdatedBy: "migration:admin.login_without_totp"}
	if err := h.svc.LoadAccess(ctx); err != nil {
		t.Fatal(err)
	}
	boss, ops := h.login(t, "boss@example.com"), h.login(t, "ops@example.com")
	if _, err := h.svc.SetRequireTOTP(ctx, ops, true, "the launch"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR switches it: %v", err)
	}
	if _, err := h.svc.SetRequireTOTP(ctx, boss, true, ""); code(err) != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("without a reason: %v", err)
	}
	// Signed in without the code: nobody's authenticator is bound.
	_, err := h.svc.SetRequireTOTP(ctx, boss, true, "the launch")
	if code(err) != "ADMIN_TOTP_NOT_BOUND" {
		t.Fatalf("nobody bound: %v", err)
	}
	if v, _ := h.svc.Access(ctx, boss); v.YouBound || v.BoundAdmins != 0 || v.UnboundCount != 2 || len(v.Unbound) != 2 {
		t.Fatalf("the view, nobody bound %+v", v)
	}
	if v, _ := h.svc.Access(ctx, ops); v.UnboundCount != 2 || len(v.Unbound) != 0 {
		t.Fatalf("an OPERATOR counts the unbound, unnamed %+v", v)
	}

	// The OPERATOR binds one: still no ADMIN's.
	bind(t, h, ops, "ops@example.com")
	if _, err := h.svc.SetRequireTOTP(ctx, boss, true, "the launch"); code(err) != "ADMIN_TOTP_NOT_BOUND" {
		t.Fatalf("an OPERATOR's alone: %v", err)
	}
	bind(t, h, boss, "boss@example.com")
	audits := len(h.store.audits)
	v, err := h.svc.SetRequireTOTP(ctx, boss, true, "the launch")
	if err != nil || !v.RequireTOTP || !v.YouBound || v.BoundAdmins != 1 || v.UnboundCount != 0 || !h.svc.TOTPRequired() {
		t.Fatalf("switched on %+v %v", v, err)
	}
	if got := h.store.audits[audits:]; len(got) != 1 || got[0].GetAction() != "admin.settings.require_totp" ||
		got[0].GetDetails() != `{"from":false,"to":true}` || got[0].GetActor() != "boss@example.com" {
		t.Fatalf("audit %v", got)
	}
	// As it is: nothing changes, nothing audited.
	if _, err := h.svc.SetRequireTOTP(ctx, boss, true, "again"); err != nil || len(h.store.audits) != audits+1 {
		t.Fatalf("again: %v, %d audits", err, len(h.store.audits)-audits)
	}
	// Signing in takes the code again.
	h.now = h.now.Add(totp.Period)
	if _, _, _, err := h.svc.Login(ctx, "boss@example.com", testPassword, "", "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
		t.Fatalf("no code: %v", err)
	}
	h.login(t, "boss@example.com")
	if err := h.svc.RemoveOwnTOTP(ctx, boss, testPassword, h.code("boss@example.com")); code(err) != "ADMIN_TOTP_REQUIRED" {
		t.Fatalf("removed while asked for: %v", err)
	}

	// exchangectl switches it off when nobody can sign in; admin-service reads it.
	if _, err := SwitchOffRequireTOTP(ctx, h.store, "cli:ops", "", h.now); code(err) != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("without a reason: %v", err)
	}
	changed, err := SwitchOffRequireTOTP(ctx, h.store, "cli:ops", "the phone is lost", h.now)
	if err != nil || !changed || h.store.access.RequireTOTP || h.store.access.UpdatedBy != "cli:ops" {
		t.Fatalf("switched off %t %+v %v", changed, h.store.access, err)
	}
	if changed, err := SwitchOffRequireTOTP(ctx, h.store, "cli:ops", "again", h.now); err != nil || changed {
		t.Fatalf("off already %t %v", changed, err)
	}
	if err := h.svc.LoadAccess(ctx); err != nil || h.svc.TOTPRequired() {
		t.Fatalf("read off %t %v", h.svc.TOTPRequired(), err)
	}
	last := h.store.audits[len(h.store.audits)-1]
	if last.GetActor() != "cli:ops" || !strings.Contains(last.GetDetails(), `"via":"exchangectl"`) {
		t.Fatalf("exchangectl's audit %v", last)
	}

	// Off, an authenticator is removed with the password and its code; not bound any more.
	h.now = h.now.Add(totp.Period)
	if err := h.svc.RemoveOwnTOTP(ctx, boss, "a wrong password", h.code("boss@example.com")); code(err) != "ADMIN_PASSWORD_WRONG" {
		t.Fatalf("a wrong password: %v", err)
	}
	if err := h.svc.RemoveOwnTOTP(ctx, boss, testPassword, "000000"); code(err) != "ADMIN_TOTP_CODE_WRONG" {
		t.Fatalf("a wrong code: %v", err)
	}
	if err := h.svc.RemoveOwnTOTP(ctx, boss, testPassword, h.code("boss@example.com")); err != nil {
		t.Fatal(err)
	}
	if a := h.store.admins[boss.Admin.ID]; a.TOTPBound() || !slices.Contains(h.actions(), "admin.totp_removed") {
		t.Fatalf("removed %+v %v", a, h.actions())
	}
	if _, err := h.svc.SetRequireTOTP(ctx, boss, true, "the launch"); code(err) != "ADMIN_TOTP_NOT_BOUND" {
		t.Fatalf("after the removal: %v", err)
	}
	// Not bound, nothing to prove but the password.
	if err := h.svc.RemoveOwnTOTP(ctx, boss, testPassword, ""); err != nil {
		t.Fatalf("removed again: %v", err)
	}

	// An authenticator reset by another ADMIN is not bound until its link binds a new one.
	h.admin(t, "second@example.com", domain.RoleAdmin)
	second := h.login(t, "second@example.com")
	bind(t, h, second, "second@example.com")
	if _, err := h.svc.ResetAdminTOTP(ctx, second, h.idOf("ops@example.com"), "lost the phone"); err != nil {
		t.Fatal(err)
	}
	if h.store.admins[h.idOf("ops@example.com")].TOTPBound() {
		t.Fatal("a reset authenticator is still bound")
	}
}

// idOf is the ID of the administrator with email.
func (h *harness) idOf(email string) string {
	for id, a := range h.store.admins {
		if a.Email == email {
			return id
		}
	}
	return ""
}

// bind binds email's authenticator from its account page: a new one
// started (the password alone while sign-in does not ask for the code) and
// confirmed with its code.
func bind(t *testing.T, h *harness, p Principal, email string) {
	t.Helper()
	ctx := context.Background()
	code := ""
	if h.svc.TOTPRequired() {
		code = h.code(email)
	}
	secret, _, err := h.svc.StartOwnTOTP(ctx, p, testPassword, code)
	if err != nil {
		t.Fatalf("bind %s: %v", email, err)
	}
	raw, _ := totp.Decode(secret)
	if err := h.svc.ConfirmOwnTOTP(ctx, p, totp.Code(raw, totp.Step(h.now))); err != nil {
		t.Fatalf("confirm %s: %v", email, err)
	}
	h.secrets[email] = raw
	h.now = h.now.Add(totp.Period)
	if !h.store.admins[p.Admin.ID].TOTPBound() {
		t.Fatalf("%s not bound", email)
	}
}
