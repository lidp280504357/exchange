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

// The console's search box (A93): an exact ID, email address, phone number
// or username opens the user (B167); anything else is NOT_FOUND, and the
// list then takes it as a keyword - user-service matching usernames, with
// the accounts auth-service finds by email address or phone number. Only
// what no account could match is refused.
func TestUserSearch(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.users.known[someUser] = ports.User{ID: someUser, Status: "ACTIVE", Username: "user_8c6fbf82"}
	h.users.usernames = map[string]string{"user_8c6fbf82": someUser}
	h.users.matches = map[string][]string{"pacminer": {someUser}}
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")

	if v, err := h.svc.FindUser(ctx, auditor, " user_8c6fbf82 "); err != nil || v.User.ID != someUser {
		t.Fatalf("a username %+v %v", v, err)
	}
	if _, err := h.svc.FindUser(ctx, auditor, "pacminer"); code(err) != apperr.CodeNotFound {
		t.Fatalf("a keyword is no user: %v", err)
	}
	for name, q := range map[string]string{"empty": "  ", "too long": strings.Repeat("a", 255), "a control character": "pac\x00miner"} {
		if _, err := h.svc.FindUser(ctx, auditor, q); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("lookup, %s: %v", name, err)
		}
	}
	if len(h.users.asked) != 2 {
		t.Fatalf("asked %q", h.users.asked)
	}

	if _, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{Q: " pacminer ", Region: "sg"}); err != nil ||
		h.users.listed.Q != "pacminer" || !slices.Equal(h.users.listed.UserIDs, []string{someUser}) || h.users.listed.Region != "SG" {
		t.Fatalf("a keyword listed %+v %v", h.users.listed, err)
	}
	if _, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{UserIDs: []string{someUser}}); err != nil || h.users.listed.UserIDs != nil {
		t.Fatalf("no keyword, no IDs %+v %v", h.users.listed, err)
	}
	// A keyword has 2 to 64 characters, as auth-service and user-service take it (B170), counted as characters.
	for name, q := range map[string]string{"one character": " a ", "65 characters": strings.Repeat("a", 65), "255 characters": strings.Repeat("a", 255)} {
		if _, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{Q: q}); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("a keyword of %s: %v", name, err)
		}
	}
	if _, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{Q: strings.Repeat("矿", 64)}); err != nil || h.users.listed.Q != strings.Repeat("矿", 64) {
		t.Fatalf("a keyword of 64 characters: %+v %v", h.users.listed, err)
	}
}

func TestNotesAndTags(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.users.known[someUser] = ports.User{ID: someUser, Status: "ACTIVE"}
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	ops, auditor := h.login(t, "ops@example.com"), h.login(t, "audit@example.com")

	if _, err := h.svc.AddNote(ctx, auditor, someUser, "looks fine"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an auditor writes a note: %v", err)
	}
	if _, err := h.svc.AddNote(ctx, ops, someUser, "   "); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an empty note: %v", err)
	}
	if _, err := h.svc.AddNote(ctx, ops, "01929c3e-0000-7000-8000-000000000000", "who?"); code(err) != apperr.CodeNotFound {
		t.Fatalf("a note on nobody: %v", err)
	}
	first, err := h.svc.AddNote(ctx, ops, someUser, " called about a missing deposit ")
	if err != nil || first.Body != "called about a missing deposit" || first.AdminEmail != "ops@example.com" {
		t.Fatalf("note %+v %v", first, err)
	}
	h.now = h.now.Add(time.Minute)
	if _, err := h.svc.AddNote(ctx, ops, someUser, "deposit found on another network"); err != nil {
		t.Fatal(err)
	}
	list, next, err := h.svc.Notes(ctx, auditor, someUser, "", 1)
	if err != nil || len(list) != 1 || list[0].Body != "deposit found on another network" || next == "" {
		t.Fatalf("newest first, a page of one: %+v %q %v", list, next, err)
	}
	rest, next, err := h.svc.Notes(ctx, auditor, someUser, next, 1)
	if err != nil || len(rest) != 1 || rest[0].ID != first.ID || next != "" {
		t.Fatalf("the next page: %+v %q %v", rest, next, err)
	}

	if _, err := h.svc.SetTags(ctx, ops, someUser, []string{"vip", "not a tag"}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a bad tag: %v", err)
	}
	tags, err := h.svc.SetTags(ctx, ops, someUser, []string{" vip", "TEST", "VIP"})
	if err != nil || !slices.Equal(tags, []string{"TEST", "VIP"}) {
		t.Fatalf("tags %v %v", tags, err)
	}
	u, err := h.svc.UserDetail(ctx, auditor, someUser)
	if err != nil || !slices.Equal(u.Tags, []string{"TEST", "VIP"}) {
		t.Fatalf("detail %+v %v", u, err)
	}
	listed, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{})
	if err != nil || len(listed) != 1 || !slices.Equal(listed[0].Tags, []string{"TEST", "VIP"}) {
		t.Fatalf("the list carries the tags: %+v %v", listed, err)
	}
	// Setting the same tags again changes nothing.
	if _, err := h.svc.SetTags(ctx, ops, someUser, []string{"VIP", "TEST"}); err != nil {
		t.Fatal(err)
	}
	audits := 0
	for _, a := range h.store.audits {
		if a.GetTarget() == "user:"+someUser {
			audits++
			if a.GetAction() == "admin.users.tags_changed" && !strings.Contains(a.GetDetails(), `"after":["TEST","VIP"]`) {
				t.Fatalf("tags audit %v", a)
			}
		}
	}
	if audits != 3 {
		t.Fatalf("audited %d times, want two notes and one change of tags", audits)
	}
}

// The accounts' kinds (L1): a list without kind has the humans only; ALL,
// every kind; named kinds once each, whatever their case; another, 400.
func TestUserKinds(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	for name, c := range map[string]struct {
		in   []string
		want []string
	}{
		"none":       {nil, []string{"HUMAN"}},
		"all":        {[]string{"bot", "ALL"}, nil},
		"named":      {[]string{" bot ", "TEST", "Bot"}, []string{"BOT", "TEST"}},
		"the system": {[]string{"SYSTEM"}, []string{"SYSTEM"}},
	} {
		if _, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{Kinds: c.in}); err != nil || !slices.Equal(h.users.listed.Kinds, c.want) {
			t.Fatalf("%s: listed %v, want %v (%v)", name, h.users.listed.Kinds, c.want, err)
		}
	}
	if _, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{Kinds: []string{"ROBOT"}}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown kind: %v", err)
	}
}

// The test accounts cleared out (L4) are listed when asked for; their
// money is left alone: an adjustment or a deposit credited to one is
// refused, and one requested before it was cleared out is not approved
// (only rejected).
func TestPurgedAccounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "second@example.com", domain.RoleAdmin)
	auditor, boss, second := h.login(t, "audit@example.com"), h.login(t, "boss@example.com"), h.login(t, "second@example.com")
	if _, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{Kinds: []string{"TEST"}, IncludePurged: true}); err != nil ||
		!h.users.listed.IncludePurged {
		t.Fatalf("asked for the accounts cleared out: listed %+v (%v)", h.users.listed, err)
	}
	if _, _, err := h.svc.ListUsers(ctx, auditor, ports.UserQuery{}); err != nil || h.users.listed.IncludePurged {
		t.Fatalf("by default: listed %+v (%v)", h.users.listed, err)
	}

	asked, err := h.svc.RequestAdjustment(ctx, boss, Adjustment{UserID: someUser, Asset: "USDT", Amount: usdt(1), Reason: "before"})
	if err != nil || asked.Status != domain.ApprovalPending {
		t.Fatalf("an account user-service does not know is the ledger's to judge: %+v %v", asked, err)
	}
	at := h.now.Add(-time.Hour)
	h.users.known[someUser] = ports.User{ID: someUser, Kind: "TEST", Status: "CLOSED", PurgedAt: &at}
	var e *apperr.Error
	_, err = h.svc.RequestAdjustment(ctx, boss, Adjustment{UserID: someUser, Asset: "USDT", Amount: usdt(1), Reason: "after"})
	if !errors.As(err, &e) || e.Code != "ADMIN_USER_PURGED" || e.Details["purged_at"] != at.UTC().Format(time.RFC3339) {
		t.Fatalf("an adjustment of an account cleared out: %v", err)
	}
	if _, err := h.svc.DecideApproval(ctx, second, asked.ID, true, "approve"); code(err) != "ADMIN_USER_PURGED" {
		t.Fatalf("approving one asked for before: %v", err)
	}
	if a := h.store.approvals[asked.ID]; a.Status != domain.ApprovalPending || !a.AttemptedAt.IsZero() || len(h.ledger.calls) != 0 {
		t.Fatalf("left as it was, unbooked: %+v, %d ledger calls", a, len(h.ledger.calls))
	}
	if r, err := h.svc.DecideApproval(ctx, second, asked.ID, false, "cleared out"); err != nil || r.Status != domain.ApprovalRejected {
		t.Fatalf("rejected: %+v %v", r, err)
	}
	if _, err := h.svc.SubmitFunds(ctx, boss, FundRequest{
		Kind: domain.KindDepositAssign, DepositID: someUser, UserID: someUser, Reason: "credit it",
	}); code(err) != "ADMIN_USER_PURGED" {
		t.Fatalf("a deposit credited to an account cleared out: %v", err)
	}
}
