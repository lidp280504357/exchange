package application

import (
	"context"
	"slices"
	"testing"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
)

// An operator resets an account's username or avatar on its own, with a
// reason, audited with what changed; an auditor may not (design
// 2026-10-07, avatars and usernames §1.6).
func TestUsernameAndAvatarResets(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	id := "0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e"
	h.users.known[id] = ports.User{ID: id, Username: "rude_name", AvatarURL: "/uploads/avatars/" + id + "/a.webp"}
	auditor, ops := h.login(t, "audit@example.com"), h.login(t, "ops@example.com")

	if _, err := h.svc.ResetUsername(ctx, auditor, id, "rude"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an auditor: %v", err)
	}
	if _, err := h.svc.ResetUsername(ctx, ops, id, ""); err == nil {
		t.Fatal("no reason")
	}
	u, err := h.svc.ResetUsername(ctx, ops, id, "rude")
	if err != nil || u.Username != "user_reset" || u.Tags == nil {
		t.Fatalf("username reset %+v %v", u, err)
	}
	u, err = h.svc.ResetAvatar(ctx, ops, id, "not allowed")
	if err != nil || u.AvatarURL != "" {
		t.Fatalf("avatar reset %+v %v", u, err)
	}
	actions := h.store.actions()
	if !slices.Contains(actions, "admin.users.username_reset") || !slices.Contains(actions, "admin.users.avatar_reset") {
		t.Fatalf("audit %v", actions)
	}
}
