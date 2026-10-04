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

	"github.com/skill/exchange/internal/notification/adapters/postgres"
	"github.com/skill/exchange/internal/notification/application"
	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/notification/transport/httpapi"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The legal pages and the home page's blocks (design 2026-10-04 §4.4):
// stored in their sections (migration notify 00005) and served on
// /v1/legal and /v1/home like the help articles.
func TestLegalAndHomeSections(t *testing.T) {
	db := setup(t)
	store := postgres.NewStore(db, event.NewFactory("notification-service", "test"))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, a := range []domain.Article{
		{Section: domain.SectionLegal, Slug: "terms", Texts: []domain.ArticleText{{Locale: domain.LocaleZH, Title: "用户条款", Body: "条款正文"}}},
		{Section: domain.SectionHome, Slug: "home-hero", Texts: []domain.ArticleText{
			{Locale: domain.LocaleZH, Title: "交易，从这里开始", Summary: "副标题", Body: "[立即注册](/register)"},
		}},
	} {
		a.ID, a.Status, a.PublishAt, a.Version, a.UpdatedBy, a.CreatedAt, a.UpdatedAt =
			uuid.Must(uuid.NewV7()).String(), domain.ArticlePublished, now.Add(-time.Minute), 1, "ops@example.com", now, now
		if err := a.Validate(); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateArticle(ctx, a); err != nil {
			t.Fatalf("%s/%s: %v", a.Section, a.Slug, err)
		}
	}

	content := &application.Content{Store: store, Now: func() time.Time { return now }}
	r := httpx.NewRouter(httpx.RouterOptions{Logger: slog.New(slog.DiscardHandler)})
	(&httpapi.Content{Svc: content, Broadcasts: &application.Broadcasts{Store: store, Now: time.Now}}).Routes(r)
	get := func(path string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if status, body := get("/v1/legal"); status != http.StatusOK || len(body["items"].([]any)) != 1 {
		t.Fatalf("the legal pages %d %v", status, body)
	}
	if status, body := get("/v1/legal/terms"); status != http.StatusOK || body["title"] != "用户条款" || body["body"] != "条款正文" {
		t.Fatalf("the terms %d %v", status, body)
	}
	if status, _ := get("/v1/legal/privacy"); status != http.StatusNotFound {
		t.Fatalf("an unpublished page %d", status)
	}
	if status, body := get("/v1/home/home-hero"); status != http.StatusOK || body["summary"] != "副标题" || body["body"] != "[立即注册](/register)" {
		t.Fatalf("the hero %d %v", status, body)
	}
	if status, _ := get("/v1/help/terms"); status != http.StatusNotFound {
		t.Fatalf("the terms are no help article %d", status)
	}
}
