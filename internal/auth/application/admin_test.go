package application

import (
	"encoding/base32"
	"strings"
	"testing"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/auth/domain"
)

func TestAdminSecurityAndSessions(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "ivan@example.com")
	if _, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "ivan@example.com", Password: password, Client: web(otherDev)}); err != nil {
		t.Fatal(err)
	}
	// Three wrong passwords count against the identity, not yet a lock.
	for range 3 {
		_, _ = a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "ivan@example.com", Password: "wrong horse battery", CaptchaToken: "human", Client: web(device)})
	}
	sec, err := a.acc.AdminSecurity(ctx, tok.UserID)
	if err != nil || len(sec.Identities) != 1 || sec.Identities[0].Value != "ivan@example.com" || len(sec.Sessions) != 2 || sec.LockedFor != 0 ||
		sec.Credential.PasswordHash != "" || len(sec.Devices) != 2 {
		t.Fatalf("security %+v %v", sec, err)
	}
	if sec.Sessions[0].IP == "203.0.113.9" {
		t.Fatalf("session addresses are masked: %+v", sec.Sessions[0])
	}

	if _, err := a.acc.AdminRevokeSessions(ctx, tok.UserID, "", "ops@example.com", ""); !strings.Contains(err.Error(), "reason") {
		t.Fatalf("no reason: %v", err)
	}
	n, err := a.acc.AdminRevokeSessions(ctx, tok.UserID, tok.SessionID, "ops@example.com", "lost phone")
	if err != nil || n != 1 {
		t.Fatalf("one session: %d %v", n, err)
	}
	if _, err := a.acc.AdminRevokeSessions(ctx, tok.UserID, tok.SessionID, "ops@example.com", "lost phone"); !strings.Contains(err.Error(), "no such session") {
		t.Fatalf("an ended session: %v", err)
	}
	if n, err := a.acc.AdminRevokeSessions(ctx, tok.UserID, "", "ops@example.com", "account takeover"); err != nil || n != 1 {
		t.Fatalf("the rest: %d %v", n, err)
	}
	for _, ev := range eventsOf[*authv1.SessionRevoked](a.store) {
		if ev.GetReason() != domain.RevokeAdmin {
			t.Fatalf("revoked for %s, want ADMIN", ev.GetReason())
		}
	}
	if len(a.revs.ids) != 2 {
		t.Fatalf("the gateway was told about %v", a.revs.ids)
	}
}

// bindTOTP binds an authenticator app to the user.
func bindTOTP(t *testing.T, a *accountFixture, tok Tokens) {
	t.Helper()
	secretB32, _, err := a.acc.SetupTOTP(ctx, tok.UserID, a.stepUp(t, tok, "EMAIL"))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretB32)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.acc.ConfirmTOTP(ctx, tok.UserID, domain.TOTPCode(secret, domain.TOTPStep(a.now))); err != nil {
		t.Fatal(err)
	}
}

func TestAdminTemporaryPassword(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "judy@example.com")
	for range domain.LockAfterFailures {
		_, _ = a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "judy@example.com", Password: "wrong horse battery", CaptchaToken: "human", Client: web(device)})
	}
	if sec, err := a.acc.AdminSecurity(ctx, tok.UserID); err != nil || sec.LockedFor == 0 {
		t.Fatalf("locked: %+v %v", sec, err)
	}
	pw, n, err := a.acc.AdminTemporaryPassword(ctx, tok.UserID, "ops@example.com", "forgot the password")
	if err != nil || n != 1 || len(pw) != 19 || strings.Count(pw, "-") != 3 {
		t.Fatalf("temporary password %q, %d sessions, %v", pw, n, err)
	}
	if sec, err := a.acc.AdminSecurity(ctx, tok.UserID); err != nil || sec.LockedFor != 0 {
		t.Fatalf("the lock is cleared: %+v %v", sec, err)
	}
	if _, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "judy@example.com", Password: pw, Client: web(device)}); err != nil {
		t.Fatalf("signing in with it: %v", err)
	}
	if ev := eventsOf[*authv1.PasswordChanged](a.store); len(ev) != 1 || !ev[0].GetViaReset() {
		t.Fatalf("PasswordChanged %v", ev)
	}
}

func TestAdminResetTOTP(t *testing.T) {
	a := newAccountFixture(t)
	withTOTP(t, a)
	tok := a.register(t, "kim@example.com")
	if removed, err := a.acc.AdminResetTOTP(ctx, tok.UserID, "ops@example.com", "lost the phone"); err != nil || removed {
		t.Fatalf("nothing to remove: %v %v", removed, err)
	}
	if sec, _ := a.acc.AdminSecurity(ctx, tok.UserID); !sec.Credential.TOTPChangedAt.IsZero() {
		t.Fatalf("nothing changed %+v", sec.Credential)
	}
	bindTOTP(t, a, tok)
	if sec, _ := a.acc.AdminSecurity(ctx, tok.UserID); sec.TOTP != domain.TOTPActive {
		t.Fatalf("bound: %q", sec.TOTP)
	}
	removed, err := a.acc.AdminResetTOTP(ctx, tok.UserID, "ops@example.com", "lost the phone")
	if err != nil || !removed {
		t.Fatalf("removed: %v %v", removed, err)
	}
	if st, _ := a.acc.TOTPStatus(ctx, tok.UserID); st.Enabled || st.Pending {
		t.Fatalf("status after %+v", st)
	}
	if ev := eventsOf[*authv1.TotpDisabled](a.store); len(ev) != 1 {
		t.Fatalf("TotpDisabled %v", ev)
	}
	// Recorded for the withdrawals' review and the console (C5.5 ⑤).
	if sec, _ := a.acc.AdminSecurity(ctx, tok.UserID); !sec.Credential.TOTPChangedAt.Equal(a.now) {
		t.Fatalf("the reset is recorded %+v", sec.Credential)
	}
}

func TestAdminDecidesRebindRequests(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "liam@example.com")
	request := func(to string) {
		su := a.stepUp(t, tok, "EMAIL")
		ticket := a.ticket(t, RequestOTP{Scene: "REBIND_IDENTITY", Channel: "EMAIL", Identifier: to, UserID: tok.UserID})
		if res, err := a.acc.RebindIdentity(ctx, tok.UserID, ticket, su, web(device)); err != nil || res != RebindPendingReview {
			t.Fatalf("rebind request: %v %v", res, err)
		}
	}
	request("liam2@example.com")
	list, next, err := a.acc.AdminIdentityRequests(ctx, domain.RebindPending, "", "", 10)
	if err != nil || len(list) != 1 || list[0].Current != "liam@example.com" || list[0].NewValue != "liam2@example.com" || next != "" {
		t.Fatalf("pending %+v %q %v", list, next, err)
	}
	rejected, err := a.acc.AdminDecideIdentityRequest(ctx, list[0].ID, false, "ops@example.com", "could not confirm the caller")
	if err != nil || rejected.Status != domain.RebindRejected || rejected.DecidedBy != "ops@example.com" {
		t.Fatalf("rejected %+v %v", rejected, err)
	}
	if _, err := a.acc.AdminDecideIdentityRequest(ctx, list[0].ID, true, "ops@example.com", "changed my mind"); err == nil {
		t.Fatal("decided twice")
	}
	request("liam3@example.com")
	list, _, _ = a.acc.AdminIdentityRequests(ctx, domain.RebindPending, tok.UserID, "", 10)
	approved, err := a.acc.AdminDecideIdentityRequest(ctx, list[0].ID, true, "ops@example.com", "called back on the old number")
	if err != nil || approved.Status != domain.RebindApproved || approved.Current != "liam3@example.com" {
		t.Fatalf("approved %+v %v", approved, err)
	}
	if id, _ := a.store.Read().Identities().Find(ctx, "EMAIL", "liam3@example.com"); id == nil || id.UserID != tok.UserID {
		t.Fatalf("the identity took the new value: %+v", id)
	}
	if ev := eventsOf[*authv1.IdentityRebound](a.store); len(ev) != 1 {
		t.Fatalf("IdentityRebound %v", ev)
	}
}
