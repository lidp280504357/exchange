package application

import (
	"context"
	"slices"
	"testing"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/totp"
)

func TestManagingAdministrators(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	boss, ops := h.login(t, "boss@example.com"), h.login(t, "ops@example.com")
	signIn := func(email, pw string, c Credentials) error {
		t.Helper()
		code := ""
		if c.TOTPSecret != "" {
			secret, err := totp.Decode(c.TOTPSecret)
			if err != nil {
				t.Fatal(err)
			}
			code = totp.Code(secret, totp.Step(h.now))
		}
		h.now = h.now.Add(totp.Period)
		_, _, _, err := h.svc.Login(ctx, email, pw, code, "192.0.2.1", "test")
		return err
	}

	if _, err := h.svc.Admins(ctx, ops); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR manages nobody: %v", err)
	}
	if roles := h.svc.Roles(); len(roles) != 4 || roles[0].Role != domain.RoleAdmin || !slices.Contains(roles[0].Permissions, domain.PermAdminsManage) ||
		slices.Contains(roles[1].Permissions, domain.PermAdminsManage) {
		t.Fatalf("roles %+v", roles)
	}

	// A new OPERATOR signs in with what came back once.
	if _, _, err := h.svc.CreateAdmin(ctx, boss, "new@example.com", "New", "operator", ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	a, cred, err := h.svc.CreateAdmin(ctx, boss, "New@Example.com", "New", "operator", "a second operator")
	if err != nil || a.Email != "new@example.com" || a.Role != domain.RoleOperator || len(cred.Password) < 12 || cred.TOTPSecret == "" || cred.TOTPURI == "" {
		t.Fatalf("created %+v %+v %v", a, cred, err)
	}
	if _, _, err := h.svc.CreateAdmin(ctx, boss, "new@example.com", "Again", "AUDITOR", "a duplicate"); code(err) != "ADMIN_EXISTS" {
		t.Fatalf("duplicate: %v", err)
	}
	if err := signIn("new@example.com", cred.Password, cred); err != nil {
		t.Fatalf("the new administrator signs in: %v", err)
	}
	list, err := h.svc.Admins(ctx, boss)
	if err != nil || len(list) != 3 || !slices.ContainsFunc(list, func(v AdminView) bool { return v.ID == a.ID && v.Sessions == 1 }) {
		t.Fatalf("admins %+v %v", list, err)
	}

	// Nobody changes their own account here.
	if _, err := h.svc.SetAdminStatus(ctx, boss, boss.Admin.ID, false, "leaving"); code(err) != "ADMIN_SELF" {
		t.Fatalf("self: %v", err)
	}
	// Disabled, the sessions end and the sign-in fails; enabled, it works again.
	if got, err := h.svc.SetAdminStatus(ctx, boss, a.ID, false, "on leave"); err != nil || got.Status != domain.StatusDisabled {
		t.Fatalf("disable %+v %v", got, err)
	}
	if live, _ := h.svc.AdminSessions(ctx, boss, a.ID); len(live) != 0 {
		t.Fatalf("sessions after disabling: %+v", live)
	}
	if err := signIn("new@example.com", cred.Password, cred); err == nil {
		t.Fatal("a disabled administrator signed in")
	}
	if _, err := h.svc.SetAdminStatus(ctx, boss, a.ID, true, "back from leave"); err != nil {
		t.Fatal(err)
	}
	// A new password: the old one fails; a new authenticator: the old code fails.
	reset, err := h.svc.ResetAdminPassword(ctx, boss, a.ID, "forgot it")
	if err != nil || reset.Password == "" || reset.Password == cred.Password {
		t.Fatalf("reset %+v %v", reset, err)
	}
	if err := signIn("new@example.com", cred.Password, cred); err == nil {
		t.Fatal("the old password still works")
	}
	fresh, err := h.svc.ResetAdminTOTP(ctx, boss, a.ID, "lost the phone")
	if err != nil || fresh.TOTPSecret == "" || fresh.TOTPSecret == cred.TOTPSecret || fresh.Password != "" {
		t.Fatalf("totp %+v %v", fresh, err)
	}
	if err := signIn("new@example.com", reset.Password, cred); err == nil {
		t.Fatal("the old authenticator still works")
	}
	if err := signIn("new@example.com", reset.Password, fresh); err != nil {
		t.Fatalf("the new password and authenticator: %v", err)
	}
	// Roles.
	if _, err := h.svc.SetAdminRole(ctx, boss, a.ID, "KING", "promotion"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("unknown role: %v", err)
	}
	if got, err := h.svc.SetAdminRole(ctx, boss, a.ID, "auditor", "reads only now"); err != nil || got.Role != domain.RoleAuditor {
		t.Fatalf("role %+v %v", got, err)
	}
	if _, err := h.svc.SetAdminRole(ctx, boss, a.ID, "AUDITOR", "again"); code(err) != apperr.CodeConflict {
		t.Fatalf("the same role: %v", err)
	}
	if err := h.svc.RevokeAdminSessions(ctx, boss, a.ID, "signed in on a shared computer"); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{
		"admin.created", "admin.disabled", "admin.enabled", "admin.password_reset", "admin.totp_reset",
		"admin.role_changed", "admin.sessions_revoked",
	} {
		if !slices.Contains(h.actions(), action) {
			t.Fatalf("%s not audited: %v", action, h.actions())
		}
	}
}
