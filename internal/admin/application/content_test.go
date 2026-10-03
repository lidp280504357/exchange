package application

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// fakeContent answers as notification-service does and records the writes.
type fakeContent struct {
	ports.Content
	articles   []ports.ArticleWrite
	published  []string
	broadcasts []ports.BroadcastWrite
}

func (f *fakeContent) CreateArticle(_ context.Context, a ports.ArticleWrite) (json.RawMessage, error) {
	f.articles = append(f.articles, a)
	return json.Marshal(map[string]any{"id": uuid.NewString(), "section": a.Section, "slug": a.Slug, "status": "DRAFT", "version": 1})
}

func (f *fakeContent) PublishArticle(_ context.Context, id string, version int, _ *time.Time, actor string) (json.RawMessage, error) {
	f.published = append(f.published, id+" "+actor)
	return json.Marshal(map[string]any{"id": id, "section": "ANNOUNCEMENT", "slug": "maintenance", "status": "PUBLISHED", "version": version + 1})
}

func (f *fakeContent) SendBroadcast(_ context.Context, b ports.BroadcastWrite) (json.RawMessage, error) {
	f.broadcasts = append(f.broadcasts, b)
	return json.Marshal(map[string]any{"id": b.ID, "audience": b.Audience, "users": len(b.UserIDs), "status": "SENDING"})
}

func TestArticlesAndMessagesFromTheConsole(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	content := &fakeContent{}
	h.svc.Content = content
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	ops, fin := h.login(t, "ops@example.com"), h.login(t, "fin@example.com")
	text := []ports.ArticleText{{Locale: "zh-CN", Title: "维护通知", Body: "今晚维护。"}}

	if _, err := h.svc.CreateArticle(ctx, fin, ports.ArticleWrite{Section: "announcement", Slug: "maintenance", Texts: text}, "tonight"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("FINANCE writes no article: %v", err)
	}
	if _, err := h.svc.CreateArticle(ctx, ops, ports.ArticleWrite{Section: "blog", Slug: "x", Texts: text}, "tonight"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown section: %v", err)
	}
	if _, err := h.svc.CreateArticle(ctx, ops, ports.ArticleWrite{Section: "announcements", Slug: "maintenance", Texts: text}, "tonight's maintenance"); err != nil {
		t.Fatal(err)
	}
	if a := content.articles[0]; a.Section != "ANNOUNCEMENT" || a.Actor != "ops@example.com" {
		t.Fatalf("written %+v", a)
	}
	id := uuid.NewString()
	if _, err := h.svc.PublishArticle(ctx, ops, id, 1, nil, "publish it"); err != nil || content.published[0] != id+" ops@example.com" {
		t.Fatalf("published %v %v", content.published, err)
	}
	last := h.store.audits[len(h.store.audits)-1]
	if last.GetAction() != "admin.content.published" || last.GetTarget() != "announcement:maintenance" || !strings.Contains(last.GetDetails(), `"status":"PUBLISHED"`) {
		t.Fatalf("audited %v", last)
	}

	// Messages: to one user, to a tag's users, to everyone.
	msg := BroadcastInput{Title: map[string]string{"zh-CN": "通知"}, Body: map[string]string{"zh-CN": "正文"}}
	if _, err := h.svc.SendBroadcast(ctx, fin, BroadcastInput{Audience: "ALL", Title: msg.Title, Body: msg.Body}, "everyone"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("FINANCE sends nothing: %v", err)
	}
	user := uuid.NewString()
	if _, err := h.svc.SendBroadcast(ctx, ops, BroadcastInput{Audience: "user", UserID: user, Title: msg.Title, Body: msg.Body}, "your ticket"); err != nil {
		t.Fatal(err)
	}
	if b := content.broadcasts[0]; b.Audience != "USERS" || !slices.Equal(b.UserIDs, []string{user}) || b.Actor != "ops@example.com" {
		t.Fatalf("to one user %+v", b)
	}
	if _, err := h.svc.SendBroadcast(ctx, ops, BroadcastInput{Audience: "TAG", Tag: "vip", Title: msg.Title, Body: msg.Body}, "vips"); code(err) != "ADMIN_TAG_EMPTY" {
		t.Fatalf("a tag nobody has: %v", err)
	}
	vips := []string{uuid.NewString(), uuid.NewString()}
	for _, v := range vips {
		if err := h.store.Tags().Set(ctx, v, []string{"VIP"}, "", h.now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.svc.SendBroadcast(ctx, ops, BroadcastInput{Audience: "TAG", Tag: "vip", Title: msg.Title, Body: msg.Body}, "vips first"); err != nil {
		t.Fatal(err)
	}
	slices.Sort(vips)
	if b := content.broadcasts[1]; !slices.Equal(b.UserIDs, vips) {
		t.Fatalf("to the tag's users %+v", b)
	}
	if _, err := h.svc.SendBroadcast(ctx, ops, BroadcastInput{Audience: "ALL", Title: msg.Title, Body: msg.Body}, "everyone"); err != nil {
		t.Fatal(err)
	}
	if b := content.broadcasts[2]; b.Audience != "ALL" || len(b.UserIDs) != 0 {
		t.Fatalf("to everyone %+v", b)
	}
	if _, err := h.svc.SendBroadcast(ctx, ops, BroadcastInput{Audience: "SOME", Title: msg.Title, Body: msg.Body}, "who"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown audience: %v", err)
	}
	if got := h.store.audits[len(h.store.audits)-1]; got.GetAction() != "admin.notices.sent" || !strings.HasPrefix(got.GetTarget(), "broadcast:") ||
		!strings.Contains(got.GetDetails(), `"audience":"ALL"`) {
		t.Fatalf("audited %v", got)
	}
}
