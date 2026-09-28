package application

import (
	"context"
	"log/slog"
	"testing"
	"time"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/authtoken"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

const (
	terms    = "2026-09"
	password = "correct horse battery"
	otherDev = "device-0002"
)

var ctx = context.Background()

type accountFixture struct {
	*fixture
	acc   *AccountService
	users *fakeUsers
	revs  *fakeRevocations
	guard *memGuard
}

func newAccountFixture(t *testing.T) *accountFixture {
	t.Helper()
	f := newFixture(t)
	f.flags[flags.KeyRegistrationSMS] = flags.Flag{Enabled: true}
	a := &accountFixture{fixture: f, users: &fakeUsers{}, revs: &fakeRevocations{}, guard: &memGuard{}}
	a.acc = &AccountService{
		Store: f.store, Users: a.users, Tokens: fakeTokens{}, Revocations: a.revs, Guard: a.guard, Captcha: fakeCaptcha{},
		Passwords: domain.NewPasswordHasher(4, domain.PasswordCost{MemoryKiB: 64, Iterations: 1}),
		Config:    AccountConfig{TermsVersion: terms, RiskVersion: terms},
		Log:       slog.New(slog.DiscardHandler),
		Now:       func() time.Time { return f.now },
	}
	return a
}

func web(deviceID string) Client {
	return Client{DeviceID: deviceID, UserAgent: "test", IP: "203.0.113.9", ClientType: domain.ClientWeb}
}

// ticket runs otp/request and otp/verify and returns the OTP ticket.
func (a *accountFixture) ticket(t *testing.T, req RequestOTP) string {
	t.Helper()
	a.svc.Limiter = &memLimiter{} // quotas are tested elsewhere
	if req.DeviceID == "" {
		req.DeviceID = device
	}
	req.CaptchaToken, req.IP = "human", "203.0.113.9"
	view, err := a.svc.Request(ctx, req)
	if err != nil {
		t.Fatalf("otp request %s: %v", req.Scene, err)
	}
	d, ok := a.notifier.last()
	if !ok || d.ChallengeID != view.ChallengeID {
		t.Fatalf("no code was sent for %s", req.Scene)
	}
	tv, err := a.svc.Verify(ctx, VerifyOTP{ChallengeID: view.ChallengeID, Code: d.Code, DeviceID: req.DeviceID})
	if err != nil {
		t.Fatalf("otp verify %s: %v", req.Scene, err)
	}
	return tv.Ticket
}

func (a *accountFixture) register(t *testing.T, email string) Tokens {
	t.Helper()
	ticket := a.ticket(t, RequestOTP{Scene: "REGISTER", Channel: "EMAIL", Identifier: email})
	tok, err := a.acc.Register(ctx, RegisterInput{
		Ticket: ticket, Password: password, Country: "sg", Language: "en", Timezone: "Asia/Singapore",
		TermsVersion: terms, RiskVersion: terms, Client: web(device),
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return tok
}

func (a *accountFixture) stepUp(t *testing.T, tok Tokens, channel string) string {
	t.Helper()
	ticket := a.ticket(t, RequestOTP{Scene: "STEP_UP", Channel: channel, UserID: tok.UserID})
	su, _, err := a.acc.StepUp(ctx, tok.UserID, tok.SessionID, ticket, web(device))
	if err != nil {
		t.Fatalf("step up: %v", err)
	}
	return su
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestRegisterAndPasswordLogin(t *testing.T) {
	a := newAccountFixture(t)

	ticket := a.ticket(t, RequestOTP{Scene: "REGISTER", Channel: "EMAIL", Identifier: "Alice@Example.com"})
	in := RegisterInput{Ticket: ticket, Password: "alice12345", Country: "SG", TermsVersion: terms, RiskVersion: terms, Client: web(device)}
	_, err := a.acc.Register(ctx, in)
	wantCode(t, err, "AUTH_PASSWORD_WEAK") // contains the email's local part
	in.Password, in.TermsVersion = password, "2025-01"
	_, err = a.acc.Register(ctx, in)
	wantCode(t, err, "AUTH_TERMS_OUTDATED")

	// The failed attempts rolled back, so the ticket still works.
	in.TermsVersion = terms
	tok, err := a.acc.Register(ctx, in)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if tok.Scope != authtoken.ScopeFull || tok.RefreshToken == "" || tok.AccessToken == "" {
		t.Fatalf("tokens: %+v", tok)
	}
	if u := a.users.created[0]; u.ID != tok.UserID || u.Region != "SG" || u.Language != "zh-CN" || u.TermsVersion != terms {
		t.Fatalf("profile: %+v", u)
	}
	if id, _ := a.store.Read().Identities().Find(ctx, "EMAIL", "alice@example.com"); id == nil || id.UserID != tok.UserID {
		t.Fatalf("identity: %+v", id)
	}
	if ev := eventsOf[*authv1.UserRegistered](a.store); len(ev) != 1 || ev[0].GetIdentityMask() == "alice@example.com" {
		t.Fatalf("UserRegistered: %v", ev)
	}
	if ev := eventsOf[*authv1.LoginSucceeded](a.store); len(ev) != 1 || !ev[0].GetNewDevice() || ev[0].GetMethod() != "REGISTER" {
		t.Fatalf("LoginSucceeded: %v", ev)
	}
	_, err = a.acc.Register(ctx, in)
	wantCode(t, err, "AUTH_TICKET_INVALID")

	res, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "alice@example.com", Password: password, Client: web(otherDev)})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.Tokens.UserID != tok.UserID || res.Tokens.SessionID == tok.SessionID {
		t.Fatalf("login tokens: %+v", res.Tokens)
	}
	if ev := eventsOf[*authv1.LoginSucceeded](a.store); len(ev) != 2 || !ev[1].GetNewDevice() {
		t.Fatalf("second login should be from a new device: %v", ev)
	}

	_, err = a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "alice@example.com", Password: "wrong password!", Client: web(device)})
	wantCode(t, err, "AUTH_PASSWORD_INVALID")
	_, err = a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "nobody@example.com", Password: password, Client: web(device)})
	wantCode(t, err, "AUTH_PASSWORD_INVALID")

	a.users.setStatus(tok.UserID, domain.StatusFrozen)
	res, err = a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "alice@example.com", Password: password, Client: web(device)})
	if err != nil || res.Tokens.Scope != authtoken.ScopeRead {
		t.Fatalf("frozen login: %+v %v", res.Tokens, err)
	}
	a.users.setStatus(tok.UserID, domain.StatusClosed)
	_, err = a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "alice@example.com", Password: password, Client: web(device)})
	wantCode(t, err, "USER_CLOSED")
}

func TestPasswordFailuresNeedCaptchaThenLock(t *testing.T) {
	a := newAccountFixture(t)
	a.register(t, "bob@example.com")

	for _, identifier := range []string{"bob@example.com", "ghost@example.com"} {
		login := PasswordLogin{Identifier: identifier, Password: "wrong password!", Client: web(device)}
		for i := 1; i <= domain.LockAfterFailures; i++ {
			_, err := a.acc.LoginPassword(ctx, login)
			switch {
			case i <= domain.CaptchaAfterFailures:
				wantCode(t, err, "AUTH_PASSWORD_INVALID")
				if i == domain.CaptchaAfterFailures {
					_, err = a.acc.LoginPassword(ctx, login)
					wantCode(t, err, "AUTH_CAPTCHA_REQUIRED")
					login.CaptchaToken = "bot"
					_, err = a.acc.LoginPassword(ctx, login)
					wantCode(t, err, "AUTH_CAPTCHA_FAILED")
					login.CaptchaToken = "human"
				}
			case i < domain.LockAfterFailures:
				wantCode(t, err, "AUTH_PASSWORD_INVALID")
			default:
				wantCode(t, err, "AUTH_ACCOUNT_LOCKED") // unknown accounts lock the same way
			}
		}
	}
	_, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "bob@example.com", Password: password, CaptchaToken: "human", Client: web(device)})
	wantCode(t, err, "AUTH_ACCOUNT_LOCKED")
	if ev := eventsOf[*authv1.LoginFailed](a.store); len(ev) != domain.LockAfterFailures || !ev[len(ev)-1].GetLocked() {
		t.Fatalf("LoginFailed events: %d", len(ev))
	}
}

func TestLoginChallengeAfterSilence(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "carol@example.com")
	a.now = a.now.Add(domain.LoginSilence + time.Hour)

	res, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "carol@example.com", Password: password, Client: web(device)})
	wantCode(t, err, "AUTH_LOGIN_CHALLENGE_REQUIRED")
	if res.LoginChallengeID == "" || len(res.Channels) != 1 || res.Channels[0].Channel != "EMAIL" || res.Channels[0].Target == "carol@example.com" {
		t.Fatalf("challenge: %+v", res)
	}

	// Another device cannot use the challenge.
	a.svc.Limiter = &memLimiter{}
	_, err = a.svc.Request(ctx, RequestOTP{Scene: "LOGIN_CHALLENGE", Channel: "EMAIL", LoginChallengeID: res.LoginChallengeID, CaptchaToken: "human", DeviceID: otherDev})
	wantCode(t, err, "AUTH_LOGIN_CHALLENGE_INVALID")

	ticket := a.ticket(t, RequestOTP{Scene: "LOGIN_CHALLENGE", Channel: "EMAIL", LoginChallengeID: res.LoginChallengeID})
	_, err = a.acc.CompleteLoginChallenge(ctx, "not-a-uuid", ticket, web(device))
	wantCode(t, err, "AUTH_LOGIN_CHALLENGE_INVALID")
	out, err := a.acc.CompleteLoginChallenge(ctx, res.LoginChallengeID, ticket, web(device))
	if err != nil || out.UserID != tok.UserID {
		t.Fatalf("complete: %+v %v", out, err)
	}

	a.svc.Limiter = &memLimiter{}
	_, err = a.svc.Request(ctx, RequestOTP{Scene: "LOGIN_CHALLENGE", Channel: "EMAIL", LoginChallengeID: res.LoginChallengeID, CaptchaToken: "human", DeviceID: device})
	wantCode(t, err, "AUTH_LOGIN_CHALLENGE_INVALID") // consumed

	// Having logged in, the next password login goes straight through.
	if _, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "carol@example.com", Password: password, Client: web(device)}); err != nil {
		t.Fatalf("login after challenge: %v", err)
	}
}

func TestOTPLogin(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "olive@example.com")
	ticket := a.ticket(t, RequestOTP{Scene: "LOGIN", Channel: "EMAIL", Identifier: "olive@example.com"})
	out, err := a.acc.LoginOTP(ctx, ticket, web(device))
	if err != nil || out.UserID != tok.UserID {
		t.Fatalf("otp login: %+v %v", out, err)
	}
	_, err = a.acc.LoginOTP(ctx, ticket, web(device))
	wantCode(t, err, "AUTH_TICKET_INVALID")
}

func TestRefreshRotatesAndDetectsReplay(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "dave@example.com")

	a.now = a.now.Add(time.Minute)
	next, err := a.acc.Refresh(ctx, tok.RefreshToken, web(device))
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if next.RefreshToken == tok.RefreshToken || next.SessionID != tok.SessionID || next.AccessToken == "" {
		t.Fatalf("rotated tokens: %+v", next)
	}

	// Two tabs racing: the second use within the window is only retried.
	a.now = a.now.Add(2 * time.Second)
	_, err = a.acc.Refresh(ctx, tok.RefreshToken, web(device))
	wantCode(t, err, "AUTH_TOKEN_EXPIRED")

	// Later it is a replay: the whole session ends.
	a.now = a.now.Add(domain.RefreshRaceWindow)
	_, err = a.acc.Refresh(ctx, tok.RefreshToken, web(device))
	wantCode(t, err, "AUTH_SESSION_REVOKED")
	if !a.revs.has(tok.SessionID) {
		t.Fatal("the replayed session must be marked revoked for the gateway")
	}
	if ev := eventsOf[*authv1.SessionRevoked](a.store); len(ev) != 1 || ev[0].GetReason() != domain.RevokeReplay {
		t.Fatalf("SessionRevoked: %v", ev)
	}
	_, err = a.acc.Refresh(ctx, next.RefreshToken, web(device))
	wantCode(t, err, "AUTH_SESSION_REVOKED")
	_, err = a.acc.Refresh(ctx, "", web(device))
	wantCode(t, err, "AUTH_SESSION_REVOKED")

	// Expiry and account status.
	res, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "dave@example.com", Password: password, Client: web(device)})
	if err != nil {
		t.Fatal(err)
	}
	a.users.setStatus(tok.UserID, domain.StatusFrozen)
	frozen, err := a.acc.Refresh(ctx, res.Tokens.RefreshToken, web(device))
	if err != nil || frozen.Scope != authtoken.ScopeRead {
		t.Fatalf("frozen refresh: %+v %v", frozen, err)
	}
	a.users.setStatus(tok.UserID, domain.StatusClosed)
	_, err = a.acc.Refresh(ctx, frozen.RefreshToken, web(device))
	wantCode(t, err, "AUTH_SESSION_REVOKED")

	a.users.setStatus(tok.UserID, domain.StatusActive)
	res, err = a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "dave@example.com", Password: password, Client: web(device)})
	if err != nil {
		t.Fatal(err)
	}
	a.now = a.now.Add(domain.RefreshTTL)
	_, err = a.acc.Refresh(ctx, res.Tokens.RefreshToken, web(device))
	wantCode(t, err, "AUTH_TOKEN_EXPIRED")
}

func TestSessionsLogoutAndStepUp(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "erin@example.com")
	other, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "erin@example.com", Password: password, Client: web(otherDev)})
	if err != nil {
		t.Fatal(err)
	}

	list, err := a.acc.Sessions(ctx, tok.UserID, tok.SessionID)
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions: %v %v", list, err)
	}
	for _, s := range list {
		if s.Current != (s.ID == tok.SessionID) || s.IP == "203.0.113.9" {
			t.Fatalf("session view: %+v", s)
		}
	}

	err = a.acc.RevokeSession(ctx, tok.UserID, tok.SessionID, other.Tokens.SessionID, "")
	wantCode(t, err, "AUTH_STEP_UP_REQUIRED")
	err = a.acc.RevokeSession(ctx, tok.UserID, tok.SessionID, "0199a000-0000-7000-8000-000000000000", "x")
	wantCode(t, err, apperr.CodeNotFound)

	// A step-up token belongs to its user and works once.
	su := a.stepUp(t, tok, "EMAIL")
	_, _, err = a.acc.ConsumeStepUp(ctx, "someone-else", su)
	wantCode(t, err, "AUTH_STEP_UP_REQUIRED")
	if err := a.acc.RevokeSession(ctx, tok.UserID, tok.SessionID, other.Tokens.SessionID, su); err != nil {
		t.Fatalf("revoke other: %v", err)
	}
	err = a.acc.RevokeSession(ctx, tok.UserID, tok.SessionID, other.Tokens.SessionID, su)
	wantCode(t, err, apperr.CodeNotFound)
	if !a.revs.has(other.Tokens.SessionID) {
		t.Fatal("revoked session not marked")
	}
	_, err = a.acc.Refresh(ctx, other.Tokens.RefreshToken, web(otherDev))
	wantCode(t, err, "AUTH_SESSION_REVOKED")

	// A step-up expires.
	su = a.stepUp(t, tok, "EMAIL")
	a.now = a.now.Add(domain.StepUpTokenTTL)
	err = a.acc.LogoutOthers(ctx, tok.UserID, tok.SessionID, su)
	wantCode(t, err, "AUTH_STEP_UP_REQUIRED")

	if err := a.acc.Logout(ctx, tok.UserID, tok.SessionID); err != nil {
		t.Fatal(err)
	}
	if list, _ := a.acc.Sessions(ctx, tok.UserID, ""); len(list) != 0 {
		t.Fatalf("sessions after logout: %v", list)
	}

	// Login history pages newest first.
	events, next, err := a.acc.History(ctx, tok.UserID, 0, 1)
	if err != nil || len(events) != 1 || next == 0 || events[0].Method != "PASSWORD" || events[0].IP == "203.0.113.9" {
		t.Fatalf("history page 1: %+v %d %v", events, next, err)
	}
	events, next, err = a.acc.History(ctx, tok.UserID, next, 10)
	if err != nil || len(events) != 1 || next != 0 || events[0].Method != "REGISTER" {
		t.Fatalf("history page 2: %+v %d %v", events, next, err)
	}
}

func TestSessionCap(t *testing.T) {
	a := newAccountFixture(t)
	first := a.register(t, "frank@example.com")
	for range domain.MaxSessions {
		a.now = a.now.Add(time.Second)
		if _, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "frank@example.com", Password: password, Client: web(device)}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := a.acc.Sessions(ctx, first.UserID, "")
	if len(list) != domain.MaxSessions {
		t.Fatalf("sessions: %d", len(list))
	}
	for _, s := range list {
		if s.ID == first.SessionID {
			t.Fatal("the oldest session should have been evicted")
		}
	}
	if !a.revs.has(first.SessionID) {
		t.Fatal("evicted session not marked")
	}
}

func TestPasswordResetAndChange(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "grace@example.com")
	other, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "grace@example.com", Password: password, Client: web(otherDev)})
	if err != nil {
		t.Fatal(err)
	}

	// Change: needs a step-up and the current password; ends other sessions.
	err = a.acc.ChangePassword(ctx, tok.UserID, tok.SessionID, password, "new password here", "")
	wantCode(t, err, "AUTH_STEP_UP_REQUIRED")
	su := a.stepUp(t, tok, "EMAIL")
	err = a.acc.ChangePassword(ctx, tok.UserID, tok.SessionID, "not my password", "new password here", su)
	wantCode(t, err, "AUTH_PASSWORD_INVALID")
	err = a.acc.ChangePassword(ctx, tok.UserID, tok.SessionID, password, "1234567890", su)
	wantCode(t, err, "AUTH_PASSWORD_WEAK")
	if err := a.acc.ChangePassword(ctx, tok.UserID, tok.SessionID, password, "new password here", su); err != nil {
		t.Fatalf("change: %v", err) // failed attempts rolled back, so su is still valid
	}
	if !a.revs.has(other.Tokens.SessionID) || a.revs.has(tok.SessionID) {
		t.Fatal("change must end the other sessions only")
	}
	if _, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "grace@example.com", Password: "new password here", Client: web(device)}); err != nil {
		t.Fatalf("login with the new password: %v", err)
	}

	// Reset: ends every session.
	ticket := a.ticket(t, RequestOTP{Scene: "PASSWORD_RESET", Channel: "EMAIL", Identifier: "grace@example.com"})
	if err := a.acc.ResetPassword(ctx, ticket, "reset password now", web(device)); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if list, _ := a.acc.Sessions(ctx, tok.UserID, ""); len(list) != 0 {
		t.Fatalf("sessions after reset: %d", len(list))
	}
	ev := eventsOf[*authv1.PasswordChanged](a.store)
	if len(ev) != 2 || ev[0].GetViaReset() || !ev[1].GetViaReset() {
		t.Fatalf("PasswordChanged: %v", ev)
	}
	_, err = a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "grace@example.com", Password: "new password here", Client: web(device)})
	wantCode(t, err, "AUTH_PASSWORD_INVALID")
	if _, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "grace@example.com", Password: "reset password now", Client: web(device)}); err != nil {
		t.Fatalf("login after reset: %v", err)
	}
}

func TestBindAndRebind(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "heidi@example.com")

	// A single-identity user cannot rebind alone: the request goes to review.
	su := a.stepUp(t, tok, "EMAIL")
	ticket := a.ticket(t, RequestOTP{Scene: "REBIND_IDENTITY", Channel: "EMAIL", Identifier: "heidi2@example.com", UserID: tok.UserID})
	res, err := a.acc.RebindIdentity(ctx, tok.UserID, ticket, su, web(device))
	if err != nil || res != RebindPendingReview || len(a.store.rebinds) != 1 {
		t.Fatalf("single-identity rebind: %v %v %v", res, err, a.store.rebinds)
	}

	su = a.stepUp(t, tok, "EMAIL")
	ticket = a.ticket(t, RequestOTP{Scene: "BIND_IDENTITY", Channel: "SMS", Identifier: "+6591234567", UserID: tok.UserID})
	if err := a.acc.BindIdentity(ctx, tok.UserID, ticket, su, web(device)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if ids, _ := a.acc.Contacts(ctx, tok.UserID); len(ids) != 2 || ids[1].Value != "+6591234567" {
		t.Fatalf("contacts: %+v", ids)
	}
	su = a.stepUp(t, tok, "EMAIL")
	ticket = a.ticket(t, RequestOTP{Scene: "BIND_IDENTITY", Channel: "SMS", Identifier: "+6598765432", UserID: tok.UserID})
	err = a.acc.BindIdentity(ctx, tok.UserID, ticket, su, web(device))
	wantCode(t, err, "AUTH_IDENTITY_KIND_BOUND")

	// With both identities, rebinding the email needs a step-up by phone.
	su = a.stepUp(t, tok, "EMAIL")
	ticket = a.ticket(t, RequestOTP{Scene: "REBIND_IDENTITY", Channel: "EMAIL", Identifier: "heidi3@example.com", UserID: tok.UserID})
	_, err = a.acc.RebindIdentity(ctx, tok.UserID, ticket, su, web(device))
	wantCode(t, err, "AUTH_STEP_UP_REQUIRED")
	su = a.stepUp(t, tok, "SMS")
	res, err = a.acc.RebindIdentity(ctx, tok.UserID, ticket, su, web(device))
	if err != nil || res != RebindDone {
		t.Fatalf("rebind: %v %v", res, err)
	}
	if id, _ := a.store.Read().Identities().Find(ctx, "EMAIL", "heidi3@example.com"); id == nil || id.UserID != tok.UserID {
		t.Fatalf("rebound identity: %+v", id)
	}
	if ev := eventsOf[*authv1.IdentityRebound](a.store); len(ev) != 1 || ev[0].GetNewMask() == "heidi3@example.com" {
		t.Fatalf("IdentityRebound: %v", ev)
	}
	if _, err := a.acc.LoginPassword(ctx, PasswordLogin{Identifier: "heidi3@example.com", Password: password, Client: web(device)}); err != nil {
		t.Fatalf("login with the new email: %v", err)
	}

	// Another user's step-up does not count.
	other := a.register(t, "ivan@example.com")
	su = a.stepUp(t, other, "EMAIL")
	ticket = a.ticket(t, RequestOTP{Scene: "BIND_IDENTITY", Channel: "SMS", Identifier: "+6590000000", UserID: tok.UserID})
	err = a.acc.BindIdentity(ctx, tok.UserID, ticket, su, web(device))
	wantCode(t, err, "AUTH_STEP_UP_REQUIRED")
}

func TestUserStatusChanges(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "judy@example.com")
	at := a.now.Add(-time.Second)

	if err := a.acc.OnUserStatusChanged(ctx, "ev-1", tok.UserID, domain.StatusFrozen, at); err != nil {
		t.Fatal(err)
	}
	if !a.revs.stale[tok.UserID].Equal(at.Add(StaleMargin)) || a.revs.has(tok.SessionID) {
		t.Fatalf("freezing marks tokens stale without ending sessions: %v", a.revs.stale)
	}
	if err := a.acc.OnUserStatusChanged(ctx, "ev-2", tok.UserID, domain.StatusClosed, at); err != nil {
		t.Fatal(err)
	}
	if !a.revs.has(tok.SessionID) {
		t.Fatal("closing ends every session")
	}
	ev := eventsOf[*authv1.SessionRevoked](a.store)
	if len(ev) != 1 || ev[0].GetReason() != domain.RevokeClosed {
		t.Fatalf("SessionRevoked: %v", ev)
	}
	// A redelivery changes nothing.
	if err := a.acc.OnUserStatusChanged(ctx, "ev-2", tok.UserID, domain.StatusClosed, at); err != nil {
		t.Fatal(err)
	}
	if len(eventsOf[*authv1.SessionRevoked](a.store)) != 1 {
		t.Fatal("redelivery revoked again")
	}
}
