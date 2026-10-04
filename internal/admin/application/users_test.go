package application

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

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
