package postgres_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/notification/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/notification/application"
	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/notification/transport/httpapi"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

func TestArticlesAndBroadcasts(t *testing.T) {
	db := setup(t)
	store := postgres.NewStore(db, event.NewFactory("notification-service", "test"))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	article := func(slug string, pinned bool, at time.Time, status string) domain.Article {
		return domain.Article{
			ID: uuid.Must(uuid.NewV7()).String(), Section: domain.SectionAnnouncement, Slug: slug, Category: "notice", Pinned: pinned,
			Status: status, PublishAt: at, Version: 1, UpdatedBy: "ops@example.com", CreatedAt: now, UpdatedAt: now,
			Texts: []domain.ArticleText{
				{Locale: domain.LocaleZH, Title: "标题 " + slug, Summary: "摘要", Body: "正文"},
				{Locale: domain.LocaleEN, Title: "Title " + slug, Body: "Body"},
			},
		}
	}
	old := article("old-news", false, now.Add(-48*time.Hour), domain.ArticlePublished)
	pinned := article("pinned", true, now.Add(-72*time.Hour), domain.ArticlePublished)
	fresh := article("fresh", false, now.Add(-time.Hour), domain.ArticlePublished)
	later := article("later", false, now.Add(time.Hour), domain.ArticlePublished)
	draft := article("draft", false, time.Time{}, domain.ArticleDraft)
	gone := article("gone", false, now.Add(-24*time.Hour), domain.ArticleArchived)
	for _, a := range []domain.Article{old, pinned, fresh, later, draft, gone} {
		if err := store.CreateArticle(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateArticle(ctx, article("fresh", false, now, domain.ArticleDraft)); !apperr.Is(err, "NOTIFY_ARTICLE_EXISTS") {
		t.Fatalf("a slug twice: %v", err)
	}
	shown, err := store.Articles(ctx, domain.SectionAnnouncement, now)
	if err != nil || len(shown) != 3 || shown[0].Slug != "pinned" || shown[1].Slug != "fresh" || shown[2].Slug != "old-news" || len(shown[1].Texts) != 2 {
		t.Fatalf("shown, pinned first then newest: %+v %v", shown, err)
	}
	if all, err := store.Articles(ctx, domain.SectionAnnouncement, time.Time{}); err != nil || len(all) != 6 {
		t.Fatalf("all %d %v", len(all), err)
	}
	if slugs, err := store.Withdrawn(ctx, domain.SectionAnnouncement); err != nil || len(slugs) != 1 || slugs[0] != "gone" {
		t.Fatalf("withdrawn %v %v", slugs, err)
	}
	if slugs, err := store.Withdrawn(ctx, domain.SectionHelp); err != nil || slugs == nil || len(slugs) != 0 {
		t.Fatalf("no help withdrawn %#v %v", slugs, err)
	}
	if help, err := store.Articles(ctx, domain.SectionHelp, now); err != nil || len(help) != 0 {
		t.Fatalf("help %+v %v", help, err)
	}
	// An update replaces the texts and needs the version it read.
	fresh.Texts, fresh.Version = fresh.Texts[:1], 2
	if err := store.UpdateArticle(ctx, fresh, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateArticle(ctx, fresh, 1); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("a stale version: %v", err)
	}
	if got, err := store.Article(ctx, domain.SectionAnnouncement, "fresh"); err != nil || got.Version != 2 || len(got.Texts) != 1 {
		t.Fatalf("updated %+v %v", got, err)
	}
	if got, err := store.ArticleByID(ctx, "nope"); err != nil || got != nil {
		t.Fatalf("a bad id %+v %v", got, err)
	}

	// The sites' endpoints: Chinese by default, English when asked, a draft
	// or a scheduled article unknown.
	content := &application.Content{Store: store, Now: func() time.Time { return now }}
	r := httpx.NewRouter(httpx.RouterOptions{Logger: slog.New(slog.DiscardHandler)})
	(&httpapi.Content{Svc: content, Broadcasts: &application.Broadcasts{Store: store, Now: time.Now}}).Routes(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	get := func(path string) (int, map[string]any, http.Header) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, http.NoBody)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out, res.Header
	}
	status, body, header := get("/v1/announcements?locale=en")
	items, _ := body["items"].([]any)
	if status != http.StatusOK || len(items) != 3 || header.Get("Cache-Control") != "public, max-age=15" {
		t.Fatalf("list %d %v %v", status, body, header)
	}
	if withdrawn, _ := body["withdrawn"].([]any); len(withdrawn) != 1 || withdrawn[0] != "gone" {
		t.Fatalf("the slugs taken off %v", body["withdrawn"])
	}
	if first := items[0].(map[string]any); first["title"] != "Title pinned" || first["fallback"] != false || first["pinned"] != true {
		t.Fatalf("first %v", first)
	}
	if second := items[1].(map[string]any); second["title"] != "标题 fresh" || second["fallback"] != true {
		t.Fatalf("English falls back %v", second)
	}
	if status, body, _ := get("/v1/announcements/fresh"); status != http.StatusOK || body["body"] != "正文" {
		t.Fatalf("article %d %v", status, body)
	}
	for _, slug := range []string{"draft", "later", "nope"} {
		if status, body, _ := get("/v1/announcements/" + slug); status != http.StatusNotFound || body["code"] != "COMMON_NOT_FOUND" {
			t.Fatalf("%s: %d %v", slug, status, body)
		}
	}
	// One taken off says so: the sites do not show their own file instead.
	if status, body, _ := get("/v1/announcements/gone"); status != http.StatusNotFound || body["code"] != "NOTIFY_ARTICLE_WITHDRAWN" {
		t.Fatalf("withdrawn: %d %v", status, body)
	}

	// A broadcast with its read count.
	users := []string{uuid.NewString(), uuid.NewString()}
	b := domain.Broadcast{
		ID: uuid.Must(uuid.NewV7()).String(), Audience: domain.AudienceUsers, UserIDs: users, Title: map[string]string{"zh-CN": "通知"},
		Body: map[string]string{"zh-CN": "正文"}, Status: domain.BroadcastSending, CreatedBy: "ops@example.com", CreatedAt: now,
	}
	if err := store.CreateBroadcast(ctx, b); err != nil {
		t.Fatal(err)
	}
	if sending, err := store.Sending(ctx); err != nil || len(sending) != 1 || len(sending[0].UserIDs) != 2 || sending[0].Title["zh-CN"] != "通知" {
		t.Fatalf("sending %+v %v", sending, err)
	}
	for _, u := range users {
		n := domain.Notice{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: u, Type: domain.NoticeBroadcast, Title: "通知", Body: "正文",
			Data: map[string]string{"broadcast_id": b.ID}, CreatedAt: now,
		}
		if _, err := store.CreateNotice(ctx, application.BroadcastConsumer, uuid.NewString(), n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.MarkRead(ctx, users[0], nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Advance(ctx, b.ID, "2", 2, true, now); err != nil {
		t.Fatal(err)
	}
	got, err := store.Broadcast(ctx, b.ID)
	if err != nil || got.Status != domain.BroadcastSent || got.Recipients != 2 || got.Read != 1 || got.FinishedAt.IsZero() {
		t.Fatalf("sent %+v %v", got, err)
	}
	if list, err := store.Broadcasts(ctx, "", 10); err != nil || len(list) != 1 || list[0].Read != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
}
