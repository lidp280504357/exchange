package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// fakeSecurity plays auth-, user- and risk-service for an account's page.
type fakeSecurity struct {
	sec        ports.Security
	logins     []ports.LoginEntry
	lastBefore int64
	revoked    []string // session IDs, "" for all
	totpResets int
	passwords  int
	requests   []ports.IdentityRequest
	// failRequests makes listing the identity requests fail.
	failRequests error
}

func newFakeSecurity() *fakeSecurity {
	return &fakeSecurity{sec: ports.Security{
		Identities: []ports.Identity{{Kind: "EMAIL", Value: "alice@example.com"}, {Kind: "PHONE", Value: "+6591234567"}},
		TOTP:       "ACTIVE",
	}}
}

func (f *fakeSecurity) Get(context.Context, string) (ports.Security, error) {
	out := f.sec
	out.Identities = slices.Clone(f.sec.Identities)
	return out, nil
}

func (f *fakeSecurity) LoginHistory(_ context.Context, _ string, before int64, limit int) ([]ports.LoginEntry, int64, error) {
	f.lastBefore = before
	var out []ports.LoginEntry
	for _, e := range f.logins {
		if before == 0 || e.ID < before {
			out = append(out, e)
		}
	}
	if len(out) > limit {
		return out[:limit], out[limit-1].ID, nil
	}
	return out, 0, nil
}

func (f *fakeSecurity) RevokeSessions(_ context.Context, _, sessionID, _, _ string) (int, error) {
	f.revoked = append(f.revoked, sessionID)
	if sessionID == "" {
		return 2, nil
	}
	return 1, nil
}

func (f *fakeSecurity) ResetTOTP(context.Context, string, string, string) (bool, error) {
	f.totpResets++
	return f.totpResets == 1, nil
}

func (f *fakeSecurity) TemporaryPassword(context.Context, string, string, string) (string, int, error) {
	f.passwords++
	return fakeTemporary, 3, nil
}

// fakeTemporary is the temporary password fakeSecurity hands out.
const fakeTemporary = "Abcd-Efgh-Jkmn-Pqrs" //nolint:gosec // a test value

func (f *fakeSecurity) IdentityRequests(_ context.Context, q ports.IdentityRequestQuery) ([]ports.IdentityRequest, string, error) {
	if f.failRequests != nil {
		return nil, "", f.failRequests
	}
	var out []ports.IdentityRequest
	for _, r := range f.requests {
		if q.Status == "" || r.Status == q.Status {
			out = append(out, r)
		}
	}
	return out, "", nil
}

func (f *fakeSecurity) DecideIdentityRequest(_ context.Context, id string, approve bool, actor, reason string) (ports.IdentityRequest, error) {
	for i, r := range f.requests {
		if r.ID != id {
			continue
		}
		if r.Status != RebindPending {
			return ports.IdentityRequest{}, apperr.New(apperr.KindConflict, apperr.CodeConflict, "the request was already decided")
		}
		r.Status, r.DecidedBy, r.Reason = RebindRejected, actor, reason
		if approve {
			r.Status, r.CurrentValue = RebindApproved, r.NewValue
		}
		f.requests[i] = r
		return r, nil
	}
	return ports.IdentityRequest{}, apperr.NotFound("no such request")
}

func (f *fakeSecurity) History(context.Context, string) ([]ports.StatusChange, []ports.Consent, error) {
	return []ports.StatusChange{{From: "ACTIVE", To: "FROZEN", Reason: "SUSPICIOUS_LOGIN", Actor: "ops@example.com"}},
		[]ports.Consent{{Document: "TERMS", Version: "v1"}}, nil
}

func (f *fakeSecurity) Assessments(context.Context, string, int) ([]ports.Assessment, error) {
	return []ports.Assessment{{ID: "a1", Score: 40, Action: "STEP_UP", Hits: []ports.RuleHit{{Rule: "new_device", Score: 40}}}}, nil
}

func (h *harness) auditsOf(action string) []string {
	var out []string
	for _, a := range h.store.audits {
		if a.GetAction() == action {
			out = append(out, a.GetTarget()+" "+a.GetReason()+" "+a.GetDetails())
		}
	}
	return out
}

func TestUserSecurityAndContacts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	auditor, fin := h.login(t, "audit@example.com"), h.login(t, "fin@example.com")

	sec, err := h.svc.UserSecurity(ctx, auditor, someUser)
	if err != nil || sec.Identities[0].Value != "a***@example.com" || sec.Identities[1].Value != "+65912****4567" || sec.TOTP != "ACTIVE" {
		t.Fatalf("masked security %+v %v", sec, err)
	}
	if _, err := h.svc.UserSecurity(ctx, auditor, "not-a-user"); code(err) != apperr.CodeNotFound {
		t.Fatalf("a bad user ID: %v", err)
	}
	if _, err := h.svc.RevealContacts(ctx, auditor, someUser); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an auditor reveals contacts: %v", err)
	}
	ids, err := h.svc.RevealContacts(ctx, fin, someUser)
	if err != nil || ids[0].Value != "alice@example.com" || ids[1].Value != "+6591234567" {
		t.Fatalf("revealed %+v %v", ids, err)
	}
	got := h.auditsOf("admin.users.contacts_revealed")
	if len(got) != 1 || !strings.Contains(got[0], `"kinds":["EMAIL","PHONE"]`) || strings.Contains(got[0], "alice") || strings.Contains(got[0], "6591234567") {
		t.Fatalf("the reveal's audit names the kinds, not the values: %v", got)
	}
}

func TestLoginHistoryCursor(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	for id := int64(5); id >= 1; id-- {
		h.security.logins = append(h.security.logins, ports.LoginEntry{ID: id, Method: "PASSWORD", Result: "SUCCESS"})
	}
	page, next, err := h.svc.LoginHistory(ctx, auditor, someUser, "", 2)
	if err != nil || len(page) != 2 || page[0].ID != 5 || next != "4" {
		t.Fatalf("first page %+v %q %v", page, next, err)
	}
	page, next, err = h.svc.LoginHistory(ctx, auditor, someUser, next, 3)
	if err != nil || len(page) != 3 || page[0].ID != 3 || next != "" || h.security.lastBefore != 4 {
		t.Fatalf("last page %+v %q %v", page, next, err)
	}
	for _, bad := range []string{"x", "-1", "0"} {
		if _, _, err := h.svc.LoginHistory(ctx, auditor, someUser, bad, 2); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("cursor %q: %v", bad, err)
		}
	}
}

func TestSecurityActions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	ops, fin := h.login(t, "ops@example.com"), h.login(t, "fin@example.com")

	if _, err := h.svc.RevokeUserSessions(ctx, fin, someUser, "", "stolen phone"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("finance ends sessions: %v", err)
	}
	if _, err := h.svc.RevokeUserSessions(ctx, ops, someUser, "", " "); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	if n, err := h.svc.RevokeUserSessions(ctx, ops, someUser, "", "stolen phone"); err != nil || n != 2 {
		t.Fatalf("all sessions: %d %v", n, err)
	}
	if n, err := h.svc.RevokeUserSessions(ctx, ops, someUser, " 0192a000-0000-7000-8000-000000000001 ", "unknown device"); err != nil || n != 1 {
		t.Fatalf("one session: %d %v", n, err)
	}
	if !slices.Equal(h.security.revoked, []string{"", "0192a000-0000-7000-8000-000000000001"}) {
		t.Fatalf("revoked %q", h.security.revoked)
	}
	if got := h.auditsOf("admin.users.sessions_revoked"); len(got) != 2 || !strings.Contains(got[0], `"session":"all"`) {
		t.Fatalf("sessions audit %v", got)
	}

	if removed, err := h.svc.ResetUserTOTP(ctx, ops, someUser, "lost the phone"); err != nil || !removed {
		t.Fatalf("totp reset %v %v", removed, err)
	}
	if removed, err := h.svc.ResetUserTOTP(ctx, ops, someUser, "lost the phone"); err != nil || removed {
		t.Fatalf("nothing left to reset %v %v", removed, err)
	}
	if got := h.auditsOf("admin.users.totp_reset"); len(got) != 2 || !strings.Contains(got[0], `"removed":true`) {
		t.Fatalf("totp audit %v", got)
	}

	if _, _, err := h.svc.TemporaryPassword(ctx, fin, someUser, "forgot it"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("finance resets a password: %v", err)
	}
	pw, n, err := h.svc.TemporaryPassword(ctx, ops, someUser, "forgot it, called in")
	if err != nil || pw != fakeTemporary || n != 3 || h.security.passwords != 1 {
		t.Fatalf("temporary password %q %d %v", pw, n, err)
	}
	got := h.auditsOf("admin.users.password_reset")
	if len(got) != 1 || strings.Contains(got[0], pw) || !strings.Contains(got[0], `"sessions_revoked":3`) {
		t.Fatalf("the password stays out of the audit: %v", got)
	}

	changes, consents, err := h.svc.UserHistory(ctx, fin, someUser)
	if err != nil || len(changes) != 1 || len(consents) != 1 {
		t.Fatalf("history %+v %+v %v", changes, consents, err)
	}
	if risk, err := h.svc.UserRisk(ctx, fin, someUser, 0); err != nil || len(risk) != 1 || risk[0].Hits[0].Rule != "new_device" {
		t.Fatalf("risk %+v %v", risk, err)
	}
}

func TestIdentityRequests(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	ops, auditor := h.login(t, "ops@example.com"), h.login(t, "audit@example.com")
	h.security.requests = []ports.IdentityRequest{
		{ID: "0192a000-0000-7000-8000-00000000000a", UserID: someUser, Kind: "EMAIL", NewValue: "bob@example.org", CurrentValue: "alice@example.com", Status: RebindPending, CreatedAt: h.now},
		{ID: "0192a000-0000-7000-8000-00000000000b", UserID: someUser, Kind: "PHONE", NewValue: "+6598765432", Status: RebindRejected, CreatedAt: h.now.Add(-time.Hour)},
	}

	list, _, err := h.svc.IdentityRequests(ctx, auditor, ports.IdentityRequestQuery{Status: RebindPending})
	if err != nil || len(list) != 1 || list[0].NewValue != "b***@example.org" || list[0].CurrentValue != "a***@example.com" {
		t.Fatalf("pending, masked %+v %v", list, err)
	}
	if _, _, err := h.svc.IdentityRequests(ctx, auditor, ports.IdentityRequestQuery{Status: "OPEN"}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a bad status: %v", err)
	}
	if todo, err := h.svc.Todo(ctx, ops); err != nil || todo.IdentityRequests != 1 {
		t.Fatalf("todo %+v %v", todo, err)
	}
	if todo, err := h.svc.Todo(ctx, auditor); err != nil || todo.IdentityRequests != 0 {
		t.Fatalf("an auditor decides nothing: %+v %v", todo, err)
	}

	id := h.security.requests[0].ID
	if _, err := h.svc.DecideIdentityRequest(ctx, auditor, id, true, "matches the KYC"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an auditor decides: %v", err)
	}
	if _, err := h.svc.DecideIdentityRequest(ctx, ops, id, true, ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	r, err := h.svc.DecideIdentityRequest(ctx, ops, id, true, "matches the KYC")
	if err != nil || r.Status != RebindApproved || r.CurrentValue != "b***@example.org" || h.security.requests[0].DecidedBy != "ops@example.com" {
		t.Fatalf("approved %+v %v", r, err)
	}
	if _, err := h.svc.DecideIdentityRequest(ctx, ops, id, false, "again"); code(err) != apperr.CodeConflict {
		t.Fatalf("decided twice: %v", err)
	}
	got := h.auditsOf("admin.users.identity_request_decided")
	if len(got) != 1 || !strings.HasPrefix(got[0], "user:"+someUser) || strings.Contains(got[0], "bob@") {
		t.Fatalf("decision audit %v", got)
	}

	h.security.failRequests = errors.New("auth-service is down")
	if todo, err := h.svc.Todo(ctx, ops); err != nil || !slices.Contains(todo.Partial, "identity_requests") {
		t.Fatalf("partial todo %+v %v", todo, err)
	}
}
