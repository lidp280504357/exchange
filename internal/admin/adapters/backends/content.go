package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/skill/exchange/internal/admin/ports"
)

// Notification implements ports.Content on notification-service's
// internal endpoints (design 2026-10-02 §4.5).
type Notification struct {
	REST
	Base string
}

// Articles returns a section's articles in every status.
func (n Notification) Articles(ctx context.Context, section string) (json.RawMessage, error) {
	return n.do(ctx, http.MethodGet, n.Base+"/internal/notification/articles?section="+url.QueryEscape(section), nil, nil)
}

// Article returns one article.
func (n Notification) Article(ctx context.Context, id string) (json.RawMessage, error) {
	return n.do(ctx, http.MethodGet, n.Base+"/internal/notification/articles/"+url.PathEscape(id), nil, nil)
}

// CreateArticle writes a draft.
func (n Notification) CreateArticle(ctx context.Context, a ports.ArticleWrite) (json.RawMessage, error) {
	return n.do(ctx, http.MethodPost, n.Base+"/internal/notification/articles", a, nil)
}

// UpdateArticle rewrites an article at its version.
func (n Notification) UpdateArticle(ctx context.Context, id string, a ports.ArticleWrite) (json.RawMessage, error) {
	return n.do(ctx, http.MethodPut, n.Base+"/internal/notification/articles/"+url.PathEscape(id), a, nil)
}

// PublishArticle shows an article from publishAt on.
func (n Notification) PublishArticle(ctx context.Context, id string, version int, publishAt *time.Time, actor string) (json.RawMessage, error) {
	body := map[string]any{"version": version, "actor": actor}
	if publishAt != nil {
		body["publish_at"] = publishAt.UTC().Format(time.RFC3339)
	}
	return n.do(ctx, http.MethodPost, n.Base+"/internal/notification/articles/"+url.PathEscape(id)+"/publish", body, nil)
}

// ArchiveArticle takes an article off the sites.
func (n Notification) ArchiveArticle(ctx context.Context, id string, version int, actor string) (json.RawMessage, error) {
	return n.do(ctx, http.MethodPost, n.Base+"/internal/notification/articles/"+url.PathEscape(id)+"/archive",
		map[string]any{"version": version, "actor": actor}, nil)
}

// Broadcasts pages through the in-app messages sent.
func (n Notification) Broadcasts(ctx context.Context, cursor string, limit int) (json.RawMessage, error) {
	v := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	return n.do(ctx, http.MethodGet, n.Base+"/internal/notification/broadcasts?"+v.Encode(), nil, nil)
}

// Broadcast returns one with its counts.
func (n Notification) Broadcast(ctx context.Context, id string) (json.RawMessage, error) {
	return n.do(ctx, http.MethodGet, n.Base+"/internal/notification/broadcasts/"+url.PathEscape(id), nil, nil)
}

// SendBroadcast records a message; notification-service delivers it.
func (n Notification) SendBroadcast(ctx context.Context, b ports.BroadcastWrite) (json.RawMessage, error) {
	return n.do(ctx, http.MethodPost, n.Base+"/internal/notification/broadcasts", b, nil)
}

// ResumeBroadcast sends a FAILED message again from where it stopped.
func (n Notification) ResumeBroadcast(ctx context.Context, id, actor string) (json.RawMessage, error) {
	return n.do(ctx, http.MethodPost, n.Base+"/internal/notification/broadcasts/"+url.PathEscape(id)+"/resume", map[string]string{"actor": actor}, nil)
}
