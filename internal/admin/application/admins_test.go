package application

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/totp"
)

func TestManagingAdministrators(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	boss, ops := h.login(t, "boss@example.com"), h.login(t, "ops@example.com")
	codeOf := func(secret string) string {
		t.Helper()
		raw, err := totp.Decode(secret)
		if err != nil {
			t.Fatal(err)
		}
		return totp.Code(raw, totp.Step(h.now))
	}
	signIn := func(email, pw, secret string) error {
		t.Helper()
		h.now = h.now.Add(totp.Period)
		code := ""
		if secret != "" {
			code = codeOf(secret)
		}
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

	// A new OPERATOR: a one-time setup link, whose holder sets the
	// password and binds the authenticator (C5.5 ⑪).
	if _, _, err := h.svc.CreateAdmin(ctx, boss, "new@example.com", "New", "operator", ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	a, setup, err := h.svc.CreateAdmin(ctx, boss, "New@Example.com", "New", "operator", "a second operator")
	if err != nil || a.Email != "new@example.com" || a.Role != domain.RoleOperator || setup.Kind != domain.SetupCreate || len(setup.Token) < 40 ||
		!setup.ExpiresAt.Equal(h.now.Add(domain.SetupTTL)) {
		t.Fatalf("created %+v %+v %v", a, setup, err)
	}
	if _, _, err := h.svc.CreateAdmin(ctx, boss, "new@example.com", "Again", "AUDITOR", "a duplicate"); code(err) != "ADMIN_EXISTS" {
		t.Fatalf("duplicate: %v", err)
	}
	view, err := h.svc.InspectSetup(ctx, setup.Token)
	if err != nil || view.Email != "new@example.com" || view.Kind != domain.SetupCreate || view.TOTPSecret == "" || view.TOTPURI == "" {
		t.Fatalf("the link shows %+v %v", view, err)
	}
	if _, err := h.svc.InspectSetup(ctx, "not-a-token"); code(err) != "ADMIN_SETUP_INVALID" {
		t.Fatalf("another token: %v", err)
	}
	if err := h.svc.CompleteSetup(ctx, setup.Token, "too short", codeOf(view.TOTPSecret), "192.0.2.9"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a short password: %v", err)
	}
	if err := h.svc.CompleteSetup(ctx, setup.Token, "the new operator's own", "000000", "192.0.2.9"); code(err) != "ADMIN_TOTP_CODE_WRONG" {
		t.Fatalf("a wrong code: %v", err)
	}
	if err := h.svc.CompleteSetup(ctx, setup.Token, "the new operator's own", codeOf(view.TOTPSecret), "192.0.2.9"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.CompleteSetup(ctx, setup.Token, "the new operator's own", codeOf(view.TOTPSecret), "192.0.2.9"); code(err) != "ADMIN_SETUP_INVALID" {
		t.Fatalf("a link used twice: %v", err)
	}
	cred := view.TOTPSecret
	if err := signIn("new@example.com", "the new operator's own", cred); err != nil {
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
	if err := signIn("new@example.com", "the new operator's own", cred); err == nil {
		t.Fatal("a disabled administrator signed in")
	}
	if _, err := h.svc.SetAdminStatus(ctx, boss, a.ID, true, "back from leave"); err != nil {
		t.Fatal(err)
	}
	// A password reset: the old one fails; the link sets a new one, the
	// authenticator stays.
	reset, err := h.svc.ResetAdminPassword(ctx, boss, a.ID, "forgot it")
	if err != nil || reset.Kind != domain.SetupPassword || reset.Token == setup.Token {
		t.Fatalf("reset %+v %v", reset, err)
	}
	if err := signIn("new@example.com", "the new operator's own", cred); err == nil {
		t.Fatal("the old password still works")
	}
	if v, err := h.svc.InspectSetup(ctx, reset.Token); err != nil || v.TOTPSecret != "" {
		t.Fatalf("a password reset binds no authenticator: %+v %v", v, err)
	}
	if err := h.svc.CompleteSetup(ctx, reset.Token, "another long password", "", "192.0.2.9"); err != nil {
		t.Fatal(err)
	}
	// An authenticator reset: the old code fails; the link binds a new one.
	fresh, err := h.svc.ResetAdminTOTP(ctx, boss, a.ID, "lost the phone")
	if err != nil || fresh.Kind != domain.SetupTOTP {
		t.Fatalf("totp %+v %v", fresh, err)
	}
	if err := signIn("new@example.com", "another long password", cred); err == nil {
		t.Fatal("the old authenticator still works")
	}
	v, err := h.svc.InspectSetup(ctx, fresh.Token)
	if err != nil || v.TOTPSecret == "" || v.TOTPSecret == cred {
		t.Fatalf("a new authenticator %+v %v", v, err)
	}
	if err := h.svc.CompleteSetup(ctx, fresh.Token, "", codeOf(v.TOTPSecret), "192.0.2.9"); err != nil {
		t.Fatal(err)
	}
	cred = v.TOTPSecret
	if err := signIn("new@example.com", "another long password", cred); err != nil {
		t.Fatalf("the new password and authenticator: %v", err)
	}
	// A link lasts a day.
	late, err := h.svc.ResetAdminPassword(ctx, boss, a.ID, "forgot it again")
	if err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(domain.SetupTTL + time.Minute)
	if err := h.svc.CompleteSetup(ctx, late.Token, "yet another password", "", "192.0.2.9"); code(err) != "ADMIN_SETUP_INVALID" {
		t.Fatalf("an expired link: %v", err)
	}
	again, err := h.svc.ResetAdminPassword(ctx, boss, a.ID, "a fresh link")
	if err != nil || h.svc.CompleteSetup(ctx, again.Token, "yet another password", "", "192.0.2.9") != nil {
		t.Fatalf("a fresh link: %v", err)
	}
	// A reset while another link waits unused keeps what that one was to
	// set: the authenticator reset over an unused password link sets both.
	if _, err := h.svc.ResetAdminPassword(ctx, boss, a.ID, "forgot it once more"); err != nil {
		t.Fatal(err)
	}
	both, err := h.svc.ResetAdminTOTP(ctx, boss, a.ID, "and the phone")
	if err != nil || both.Kind != domain.SetupCreate {
		t.Fatalf("both to set %+v %v", both, err)
	}
	if v, err := h.svc.InspectSetup(ctx, both.Token); err != nil || v.TOTPSecret == "" || !domain.SetsPassword(v.Kind) {
		t.Fatalf("the link sets both %+v %v", v, err)
	} else if err := h.svc.CompleteSetup(ctx, both.Token, "the operator's latest", codeOf(v.TOTPSecret), "192.0.2.9"); err != nil {
		t.Fatal(err)
	} else if err := signIn("new@example.com", "the operator's latest", v.TOTPSecret); err != nil {
		t.Fatalf("both set: %v", err)
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
		"admin.role_changed", "admin.sessions_revoked", "admin.setup_completed",
	} {
		if !slices.Contains(h.actions(), action) {
			t.Fatalf("%s not audited: %v", action, h.actions())
		}
	}
}

// TestAnAdministratorsOwnCredentials: a signed-in administrator changes
// their password and authenticator, proving the current password; one
// whose password was generated for them changes it first (C5.5 ⑪).
func TestAnAdministratorsOwnCredentials(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	signIn := func() (string, Principal) {
		t.Helper()
		h.now = h.now.Add(totp.Period)
		token, _, _, err := h.svc.Login(ctx, "boss@example.com", testPassword, h.code("boss@example.com"), "192.0.2.1", "test")
		if err != nil {
			t.Fatal(err)
		}
		p, err := h.svc.Authenticate(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		return token, p
	}
	otherToken, _ := signIn()
	bossToken, boss := signIn()

	if err := h.svc.ChangeOwnPassword(ctx, boss, "not the password", "a brand new password"); code(err) != "ADMIN_PASSWORD_WRONG" {
		t.Fatalf("a wrong current password: %v", err)
	}
	if err := h.svc.ChangeOwnPassword(ctx, boss, testPassword, "short"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a short one: %v", err)
	}
	if err := h.svc.ChangeOwnPassword(ctx, boss, testPassword, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Authenticate(ctx, otherToken); code(err) != "ADMIN_UNAUTHORIZED" {
		t.Fatalf("the other session goes: %v", err)
	}
	if _, err := h.svc.Authenticate(ctx, bossToken); err != nil {
		t.Fatalf("this one stays: %v", err)
	}

	// A new authenticator: the password and the current authenticator's
	// code start it, the old one works until the new one's code binds it,
	// within ten minutes.
	h.now = h.now.Add(totp.Period)
	if _, _, err := h.svc.StartOwnTOTP(ctx, boss, testPassword, h.code("boss@example.com")); code(err) != "ADMIN_PASSWORD_WRONG" {
		t.Fatalf("the old password: %v", err)
	}
	if _, _, err := h.svc.StartOwnTOTP(ctx, boss, "a brand new password", "000000"); code(err) != "ADMIN_TOTP_CODE_WRONG" {
		t.Fatalf("a wrong current code: %v", err)
	}
	current := h.code("boss@example.com")
	secret, uri, err := h.svc.StartOwnTOTP(ctx, boss, "a brand new password", current)
	if err != nil || secret == "" || uri == "" {
		t.Fatalf("start %q %q %v", secret, uri, err)
	}
	if _, _, err := h.svc.StartOwnTOTP(ctx, boss, "a brand new password", current); code(err) != "ADMIN_TOTP_CODE_WRONG" {
		t.Fatalf("the current code twice: %v", err)
	}
	if err := h.svc.ConfirmOwnTOTP(ctx, boss, "000000"); code(err) != "ADMIN_TOTP_CODE_WRONG" {
		t.Fatalf("a wrong code: %v", err)
	}
	raw, _ := totp.Decode(secret)
	if err := h.svc.ConfirmOwnTOTP(ctx, boss, totp.Code(raw, totp.Step(h.now))); err != nil {
		t.Fatal(err)
	}
	h.secrets["boss@example.com"] = raw
	h.now = h.now.Add(totp.Period)
	if _, _, _, err := h.svc.Login(ctx, "boss@example.com", "a brand new password", h.code("boss@example.com"), "192.0.2.1", "test"); err != nil {
		t.Fatalf("signs in with the new authenticator: %v", err)
	}
	h.now = h.now.Add(totp.Period)
	if _, _, err := h.svc.StartOwnTOTP(ctx, boss, "a brand new password", h.code("boss@example.com")); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(domain.SelfTOTPTTL + time.Second)
	if err := h.svc.ConfirmOwnTOTP(ctx, boss, totp.Code(raw, totp.Step(h.now))); code(err) != apperr.CodeConflict {
		t.Fatalf("too late: %v", err)
	}
	// Without codes at sign-in (admin.login_without_totp) the password
	// alone starts one.
	h.svc.Features = onFlags{flags.KeyAdminNoTOTP: true}
	if _, _, err := h.svc.StartOwnTOTP(ctx, boss, "a brand new password", ""); err != nil {
		t.Fatalf("without codes: %v", err)
	}
	h.svc.Features = nil
	for _, action := range []string{"admin.password_changed", "admin.totp_changed"} {
		if !slices.Contains(h.actions(), action) {
			t.Fatalf("%s not audited: %v", action, h.actions())
		}
	}

	// exchangectl's generated password is changed at the first sign-in.
	secretCLI := totp.NewSecret()
	if _, err := NewAdmin(ctx, h.store, h.svc.Hasher, h.svc.Box, "cli@example.com", "CLI", domain.RoleAuditor, "a generated password", secretCLI,
		"cli:test", true, h.now); err != nil {
		t.Fatal(err)
	}
	h.secrets["cli@example.com"] = secretCLI
	token, _, signed, err := h.svc.Login(ctx, "cli@example.com", "a generated password", h.code("cli@example.com"), "192.0.2.1", "test")
	if err != nil || !signed.MustChangePassword {
		t.Fatalf("signs in, held to its password: %+v %v", signed, err)
	}
	p, err := h.svc.Authenticate(ctx, token)
	if err != nil || !p.Admin.MustChangePassword {
		t.Fatalf("the principal %+v %v", p, err)
	}
	if err := h.svc.ChangeOwnPassword(ctx, p, "a generated password", "the auditor's own one"); err != nil {
		t.Fatal(err)
	}
	if p, err := h.svc.Authenticate(ctx, token); err != nil || p.Admin.MustChangePassword {
		t.Fatalf("free once changed %+v %v", p, err)
	}
}

// TestTheLastADMINStays: two ADMINs demoting each other at once leave one
// (the roster is locked through the check, C5.5 ⑪); played in turn here,
// the second with the principal it signed in with.
func TestTheLastADMINStays(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "deputy@example.com", domain.RoleAdmin)
	boss, deputy := h.login(t, "boss@example.com"), h.login(t, "deputy@example.com")
	if _, err := h.svc.SetAdminRole(ctx, boss, deputy.Admin.ID, domain.RoleOperator, "one ADMIN is enough"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetAdminRole(ctx, deputy, boss.Admin.ID, domain.RoleAuditor, "and none at all"); code(err) != "ADMIN_LAST_ADMIN" {
		t.Fatalf("the last ADMIN demoted: %v", err)
	}
	if _, err := h.svc.SetAdminStatus(ctx, deputy, boss.Admin.ID, false, "and none at all"); code(err) != "ADMIN_LAST_ADMIN" {
		t.Fatalf("the last ADMIN disabled: %v", err)
	}
}
