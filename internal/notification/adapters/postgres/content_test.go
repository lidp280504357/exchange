package postgres_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/notification/adapters/postgres"
	"github.com/skill/exchange/internal/notification/application"
	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/notification/ports"
	"github.com/skill/exchange/internal/notification/transport/httpapi"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/httpx"
)

func TestArticlesAndBroadcasts(t *testing.T) {
	db := setup(t)
	store := postgres.NewStore(db, event.NewFactory("notification-service", "test"))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	article := func(slug string, pinned bool, at time.Time, status string) domain.Article {
		return domain.Article{
			ID: uuid.Must(uuid.NewV7()).String(), Section: domain.SectionAnnouncement, Slug: slug, Category: "notice", Pinned: pinned,
			Modes: domain.ModeBoth, Status: status, PublishAt: at, Version: 1, UpdatedBy: "ops@example.com", CreatedAt: now, UpdatedAt: now,
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
	if slugs, err := store.Withdrawn(ctx, domain.SectionAnnouncement, false); err != nil || len(slugs) != 1 || slugs[0] != "gone" {
		t.Fatalf("withdrawn %v %v", slugs, err)
	}
	if slugs, err := store.Withdrawn(ctx, domain.SectionHelp, true); err != nil || slugs == nil || len(slugs) != 0 {
		t.Fatalf("no help withdrawn %#v %v", slugs, err)
	}
	if help, err := store.Articles(ctx, domain.SectionHelp, now); err != nil || len(help) != 0 {
		t.Fatalf("help %+v %v", help, err)
	}
	// The sites' pages read the head of each body only (C5.5 ⑫).
	long := article("long-read", false, now.Add(-30*time.Hour), domain.ArticlePublished)
	long.Texts = long.Texts[:1]
	long.Texts[0].Summary, long.Texts[0].Body = "", strings.Repeat("x", 5000)
	if err := store.CreateArticle(ctx, long); err != nil {
		t.Fatal(err)
	}
	page, err := store.PublishedPage(ctx, domain.SectionAnnouncement, false, now, 0, 2)
	if err != nil || len(page) != 2 || page[0].Slug != "pinned" || page[1].Slug != "fresh" {
		t.Fatalf("the first page %+v %v", page, err)
	}
	page, err = store.PublishedPage(ctx, domain.SectionAnnouncement, false, now, 2, 5)
	if err != nil || len(page) != 2 || page[0].Slug != "long-read" || page[1].Slug != "old-news" {
		t.Fatalf("the next page %+v %v", page, err)
	}
	if text, _ := page[0].Text(domain.LocaleZH); len(text.Body) != ports.HeadLength {
		t.Fatalf("the body's head only: %d", len(text.Body))
	}
	// An update replaces the texts and needs the version it read.
	fresh.Texts, fresh.Version = fresh.Texts[:1], 2
	if err := store.UpdateArticle(ctx, fresh, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateArticle(ctx, fresh, 1); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("a stale version: %v", err)
	}
	if got, err := store.Article(ctx, domain.SectionAnnouncement, "fresh", true); err != nil || got.Version != 2 || len(got.Texts) != 1 {
		t.Fatalf("updated %+v %v", got, err)
	}

	if got, err := store.ArticleByID(ctx, "nope"); err != nil || got != nil {
		t.Fatalf("a bad id %+v %v", got, err)
	}

	// The sites' endpoints: Chinese by default, English when asked, a draft
	// or a scheduled article unknown.
	content := &application.Content{Store: store, Mode: &application.Mode{Profile: liveMode{}, Now: time.Now}, Now: func() time.Time { return now }}
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
	if status != http.StatusOK || len(items) != 4 || header.Get("Cache-Control") != "public, max-age=15" || body["next_cursor"] != nil {
		t.Fatalf("list %d %v %v", status, body, header)
	}
	if status, body, _ := get("/v1/announcements?limit=3"); status != http.StatusOK || len(body["items"].([]any)) != 3 || body["next_cursor"] != "3" {
		t.Fatalf("a page of three %d %v", status, body)
	}
	if status, body, _ := get("/v1/announcements?limit=3&cursor=3"); status != http.StatusOK || len(body["items"].([]any)) != 1 || body["next_cursor"] != nil {
		t.Fatalf("the last page %d %v", status, body)
	}
	if status, _, _ := get("/v1/announcements?cursor=x"); status != http.StatusBadRequest {
		t.Fatalf("a bad cursor %d", status)
	}
	if long := items[2].(map[string]any); long["slug"] != "long-read" || len([]rune(long["summary"].(string))) != 140 {
		t.Fatalf("a summary from the body's head %v", long)
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

	// Content by mode (design 2026-10-04 §4.4): a slug's test and formal
	// pages side by side, one for both beside neither of them.
	testOnly := article("modes", false, now.Add(-2*time.Hour), domain.ArticlePublished)
	testOnly.Modes = domain.ModeTest
	formal := article("modes", false, now.Add(-3*time.Hour), domain.ArticlePublished)
	formal.Modes = domain.ModeFormal
	for _, a := range []domain.Article{testOnly, formal} {
		if err := store.CreateArticle(ctx, a); err != nil {
			t.Fatalf("%s: %v", a.Modes, err)
		}
	}
	both := article("modes", false, now, domain.ArticleDraft)
	if err := store.CreateArticle(ctx, both); !apperr.Is(err, "NOTIFY_ARTICLE_EXISTS") {
		t.Fatalf("BOTH beside a test and a formal page: %v", err)
	}
	for test, want := range map[bool]string{true: testOnly.ID, false: formal.ID} {
		if got, err := store.Article(ctx, domain.SectionAnnouncement, "modes", test); err != nil || got == nil || got.ID != want {
			t.Fatalf("test mode %v: %+v %v", test, got, err)
		}
	}
	if page, err := store.PublishedPage(ctx, domain.SectionAnnouncement, true, now, 0, 20); err != nil || len(page) != 5 ||
		slices.IndexFunc(page, func(a domain.Article) bool { return a.ID == formal.ID }) >= 0 {
		t.Fatalf("in test mode, the formal page is left out: %d %v", len(page), err)
	}
	formal.Status, formal.Version = domain.ArticleArchived, 2
	if err := store.UpdateArticle(ctx, formal, 1); err != nil {
		t.Fatal(err)
	}
	if slugs, err := store.Withdrawn(ctx, domain.SectionAnnouncement, true); err != nil || slices.Contains(slugs, "modes") {
		t.Fatalf("the formal page taken off is not withdrawn in test mode: %v %v", slugs, err)
	}
	if slugs, err := store.Withdrawn(ctx, domain.SectionAnnouncement, false); err != nil || !slices.Contains(slugs, "modes") {
		t.Fatalf("live it is: %v %v", slugs, err)
	}
	testOnly.Modes, testOnly.Version = domain.ModeBoth, 2
	if err := store.UpdateArticle(ctx, testOnly, 1); !apperr.Is(err, "NOTIFY_ARTICLE_EXISTS") {
		t.Fatalf("the test page made BOTH beside the formal one: %v", err)
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
	if sending, err := store.Sending(ctx, now); err != nil || len(sending) != 1 || len(sending[0].UserIDs) != 2 || sending[0].Title["zh-CN"] != "通知" {
		t.Fatalf("sending %+v %v", sending, err)
	}
	// A failed round waits; the broadcast's next round is due after it.
	if err := store.FailRound(ctx, b.ID, 1, "user-service unavailable", now.Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}
	if sending, err := store.Sending(ctx, now); err != nil || len(sending) != 0 {
		t.Fatalf("not due yet %+v %v", sending, err)
	}
	sending, err := store.Sending(ctx, now.Add(2*time.Minute))
	if err != nil || len(sending) != 1 || sending[0].Failures != 1 || sending[0].LastError != "user-service unavailable" || sending[0].RetryAt.IsZero() {
		t.Fatalf("due again %+v %v", sending, err)
	}
	// Each user's notice, the first one mailed: its mail waits in the queue.
	var mailed domain.Notice
	for i, u := range users {
		n := domain.Notice{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: u, Type: domain.NoticeBroadcast, Title: "通知", Body: "正文",
			Data: map[string]string{"broadcast_id": b.ID}, CreatedAt: now,
		}
		var mail *domain.Delivery
		if i == 0 {
			mailed = n
			mail = &domain.Delivery{ID: n.ID, Kind: domain.KindNotice, Channel: domain.ChannelEmail, Template: "notice.broadcast", TargetMask: "u***@example.com", UserID: u}
		}
		if _, err := store.CreateNotice(ctx, application.BroadcastConsumer, uuid.NewString(), n, mail); err != nil {
			t.Fatal(err)
		}
	}
	due, err := store.TakeDue(ctx, now, 5*time.Minute, 10)
	if err != nil || len(due) != 1 || due[0].ID != mailed.ID || due[0].Attempts != 0 || due[0].Channel != domain.ChannelEmail {
		t.Fatalf("the queued mail %+v %v", due, err)
	}
	if again, err := store.TakeDue(ctx, now.Add(time.Minute), 5*time.Minute, 10); err != nil || len(again) != 0 {
		t.Fatalf("held while it is sent %+v %v", again, err)
	}
	if err := store.Retry(ctx, mailed.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again, err := store.TakeDue(ctx, now.Add(2*time.Minute), 5*time.Minute, 10); err != nil || len(again) != 1 || again[0].Rounds != 1 {
		t.Fatalf("due again, a round failed (C5.5 ㉓) %+v %v", again, err)
	}
	if err := store.Settle(ctx, mailed.ID); err != nil {
		t.Fatal(err)
	}
	if again, err := store.TakeDue(ctx, now.Add(time.Hour), 5*time.Minute, 10); err != nil || len(again) != 0 {
		t.Fatalf("settled %+v %v", again, err)
	}
	if got, err := store.Notice(ctx, mailed.ID); err != nil || got == nil || got.UserID != users[0] || got.Data["broadcast_id"] != b.ID {
		t.Fatalf("the notice by its ID %+v %v", got, err)
	}
	// A queued mail the provider took is never taken again, even before it
	// is settled (C5.5 ㉓).
	if err := store.Retry(ctx, mailed.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAttempt(ctx, mailed.ID, domain.StatusSent, "mock", "", "m-1"); err != nil {
		t.Fatal(err)
	}
	if again, err := store.TakeDue(ctx, now.Add(time.Hour), 5*time.Minute, 10); err != nil || len(again) != 0 {
		t.Fatalf("sent, not taken again %+v %v", again, err)
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
	if got.Failures != 0 || got.LastError != "" || !got.RetryAt.IsZero() {
		t.Fatalf("a round starts the failures over %+v", got)
	}

	// Failing for good, a broadcast waits for an operator.
	stuck := b
	stuck.ID = uuid.Must(uuid.NewV7()).String()
	if err := store.CreateBroadcast(ctx, stuck); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Resume(ctx, stuck.ID); err != nil || ok {
		t.Fatalf("only a FAILED one resumes: %v %v", ok, err)
	}
	if err := store.FailRound(ctx, stuck.ID, domain.MaxRoundFailures, "down", now, true); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Broadcast(ctx, stuck.ID); err != nil || got.Status != domain.BroadcastFailed || got.Failures != domain.MaxRoundFailures {
		t.Fatalf("failed %+v %v", got, err)
	}
	if ok, err := store.Resume(ctx, stuck.ID); err != nil || !ok {
		t.Fatalf("resumed: %v %v", ok, err)
	}
	if got, err := store.Broadcast(ctx, stuck.ID); err != nil || got.Status != domain.BroadcastSending || got.Failures != 0 || got.LastError != "" {
		t.Fatalf("sending again %+v %v", got, err)
	}

	// Retention: past their keep, notices, broadcasts not sending and
	// delivery records waiting for nothing go.
	cutoff := now.Add(time.Second)
	if n, err := store.PurgeNotices(ctx, cutoff, 1); err != nil || n != 1 {
		t.Fatalf("a batch of one notice: %d %v", n, err)
	}
	if n, err := store.PurgeNotices(ctx, cutoff, 100); err != nil || n != 1 {
		t.Fatalf("the other notice: %d %v", n, err)
	}
	if n, err := store.PurgeBroadcasts(ctx, cutoff, 100); err != nil || n != 1 {
		t.Fatalf("the sent broadcast, not the sending one: %d %v", n, err)
	}
	if n, err := store.PurgeDeliveries(ctx, time.Now().Add(time.Minute), 100); err != nil || n != 1 {
		t.Fatalf("the settled mail: %d %v", n, err)
	}
}

// liveMode is a platform profile with the exchange live.
type liveMode struct{}

func (liveMode) TestMode(context.Context) (bool, error) { return false, nil }
