package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/notification/application"
	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Content serves the announcements and help articles to the sites (public,
// through the gateway) and to the admin console (internal), and takes the
// operators' in-app messages (design 2026-10-02 §4.5).
type Content struct {
	Svc        *application.Content
	Broadcasts *application.Broadcasts
}

// Routes mounts the endpoints on r.
func (h *Content) Routes(r chi.Router) {
	for path, section := range map[string]string{"/v1/announcements": domain.SectionAnnouncement, "/v1/help": domain.SectionHelp} {
		r.Get(path, h.published(section))
		r.Get(path+"/{slug}", h.publishedArticle(section))
	}
	r.Get("/internal/notification/articles", h.articles)
	r.Post("/internal/notification/articles", h.createArticle)
	r.Get("/internal/notification/articles/{id}", h.article)
	r.Put("/internal/notification/articles/{id}", h.updateArticle)
	r.Post("/internal/notification/articles/{id}/publish", h.publish)
	r.Post("/internal/notification/articles/{id}/archive", h.archive)
	r.Get("/internal/notification/broadcasts", h.broadcasts)
	r.Post("/internal/notification/broadcasts", h.sendBroadcast)
	r.Get("/internal/notification/broadcasts/{id}", h.broadcast)
}

// locale is the sites' language: en, else Chinese.
func locale(r *http.Request) string {
	if strings.HasPrefix(r.URL.Query().Get("locale"), "en") {
		return domain.LocaleEN
	}
	return domain.LocaleZH
}

// summaryJSON is an article as a list shows it, in one language.
type summaryJSON struct {
	Slug        string `json:"slug"`
	Category    string `json:"category"`
	Pinned      bool   `json:"pinned"`
	Order       int    `json:"order"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	PublishedAt string `json:"published_at"`
	Locale      string `json:"locale"`
	// Fallback: no text in the language asked, the Chinese one instead.
	Fallback bool `json:"fallback"`
	Version  int  `json:"version"`
}

func summaryOf(a domain.Article, loc string) summaryJSON {
	t, ok := a.Text(loc)
	if strings.TrimSpace(t.Summary) == "" {
		// A list carries no body: its summary, else the body's first paragraph.
		t.Summary = domain.Excerpt(t.Body)
	}
	return summaryJSON{
		Slug: a.Slug, Category: a.Category, Pinned: a.Pinned, Order: a.Order, Title: t.Title, Summary: t.Summary,
		PublishedAt: httpx.FormatTime(a.PublishAt), Locale: t.Locale, Fallback: !ok, Version: a.Version,
	}
}

// The sites ask again within a minute (design 2026-10-02 §9).
const contentCache = "public, max-age=15"

func (h *Content) published(section string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := h.Svc.Published(r.Context(), section)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		withdrawn, err := h.Svc.Withdrawn(r.Context(), section)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		loc := locale(r)
		out := make([]summaryJSON, 0, len(list))
		for _, a := range list {
			out = append(out, summaryOf(a, loc))
		}
		w.Header().Set("Cache-Control", contentCache)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "withdrawn": withdrawn})
	}
}

func (h *Content) publishedArticle(section string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := h.Svc.PublishedArticle(r.Context(), section, chi.URLParam(r, "slug"))
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		loc := locale(r)
		t, _ := a.Text(loc)
		w.Header().Set("Cache-Control", contentCache)
		httpx.WriteJSON(w, http.StatusOK, struct {
			summaryJSON
			Body string `json:"body"`
		}{summaryOf(a, loc), t.Body})
	}
}

// articleJSON is an article as the console edits it: every text, its
// status and version.
type articleJSON struct {
	ID        string     `json:"id"`
	Section   string     `json:"section"`
	Slug      string     `json:"slug"`
	Category  string     `json:"category"`
	Pinned    bool       `json:"pinned"`
	Order     int        `json:"order"`
	Status    string     `json:"status"`
	PublishAt *string    `json:"publish_at"`
	Version   int        `json:"version"`
	UpdatedBy string     `json:"updated_by"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
	Texts     []textJSON `json:"texts"`
}

type textJSON struct {
	Locale  string `json:"locale"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Body    string `json:"body"`
}

func articleOf(a domain.Article) articleJSON {
	out := articleJSON{
		ID: a.ID, Section: a.Section, Slug: a.Slug, Category: a.Category, Pinned: a.Pinned, Order: a.Order, Status: a.Status,
		Version: a.Version, UpdatedBy: a.UpdatedBy, CreatedAt: httpx.FormatTime(a.CreatedAt), UpdatedAt: httpx.FormatTime(a.UpdatedAt),
		Texts: []textJSON{},
	}
	if !a.PublishAt.IsZero() {
		at := httpx.FormatTime(a.PublishAt)
		out.PublishAt = &at
	}
	for _, t := range a.Texts {
		out.Texts = append(out.Texts, textJSON(t))
	}
	return out
}

func (h *Content) articles(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.All(r.Context(), strings.ToUpper(r.URL.Query().Get("section")))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]articleJSON, 0, len(list))
	for _, a := range list {
		out = append(out, articleOf(a))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"articles": out})
}

func (h *Content) article(w http.ResponseWriter, r *http.Request) {
	a, err := h.Svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, articleOf(a))
}

// articleBody is what the console sends of an article.
type articleBody struct {
	Section  string     `json:"section"`
	Slug     string     `json:"slug"`
	Category string     `json:"category"`
	Pinned   bool       `json:"pinned"`
	Order    int        `json:"order"`
	Texts    []textJSON `json:"texts"`
	Version  int        `json:"version"`
	Actor    string     `json:"actor"`
}

func (b articleBody) input() application.ArticleInput {
	texts := make([]domain.ArticleText, 0, len(b.Texts))
	for _, t := range b.Texts {
		texts = append(texts, domain.ArticleText(t))
	}
	return application.ArticleInput{Section: b.Section, Slug: b.Slug, Category: b.Category, Pinned: b.Pinned, Order: b.Order, Texts: texts}
}

func needActor(actor string) error {
	if strings.TrimSpace(actor) == "" {
		return apperr.Invalid("actor is required")
	}
	return nil
}

func (h *Content) createArticle(w http.ResponseWriter, r *http.Request) {
	var body articleBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := needActor(body.Actor); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.Create(r.Context(), body.input(), body.Actor)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, articleOf(a))
}

func (h *Content) updateArticle(w http.ResponseWriter, r *http.Request) {
	var body articleBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := needActor(body.Actor); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.Update(r.Context(), chi.URLParam(r, "id"), body.Version, body.input(), body.Actor)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, articleOf(a))
}

func (h *Content) publish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version   int        `json:"version"`
		PublishAt *time.Time `json:"publish_at"`
		Actor     string     `json:"actor"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := needActor(body.Actor); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var at time.Time
	if body.PublishAt != nil {
		at = *body.PublishAt
	}
	a, err := h.Svc.Publish(r.Context(), chi.URLParam(r, "id"), body.Version, at, body.Actor)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, articleOf(a))
}

func (h *Content) archive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int    `json:"version"`
		Actor   string `json:"actor"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := needActor(body.Actor); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.Archive(r.Context(), chi.URLParam(r, "id"), body.Version, body.Actor)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, articleOf(a))
}

// broadcastJSON is an operator's in-app message with how it went.
type broadcastJSON struct {
	ID         string            `json:"id"`
	Audience   string            `json:"audience"`
	Users      int               `json:"users"`
	Title      map[string]string `json:"title"`
	Body       map[string]string `json:"body"`
	Link       string            `json:"link"`
	Email      bool              `json:"email"`
	Status     string            `json:"status"`
	Recipients int               `json:"recipients"`
	Read       int               `json:"read"`
	CreatedBy  string            `json:"created_by"`
	CreatedAt  string            `json:"created_at"`
	FinishedAt *string           `json:"finished_at"`
}

func broadcastOf(b domain.Broadcast) broadcastJSON {
	out := broadcastJSON{
		ID: b.ID, Audience: b.Audience, Users: len(b.UserIDs), Title: b.Title, Body: b.Body, Link: b.Link, Email: b.Email, Status: b.Status,
		Recipients: b.Recipients, Read: b.Read, CreatedBy: b.CreatedBy, CreatedAt: httpx.FormatTime(b.CreatedAt),
	}
	if !b.FinishedAt.IsZero() {
		at := httpx.FormatTime(b.FinishedAt)
		out.FinishedAt = &at
	}
	return out
}

func (h *Content) broadcasts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, next, err := h.Broadcasts.List(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]broadcastJSON, 0, len(list))
	for _, b := range list {
		items = append(items, broadcastOf(b))
	}
	resp := map[string]any{"items": items, "next_cursor": nil}
	if next != "" {
		resp["next_cursor"] = next
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Content) broadcast(w http.ResponseWriter, r *http.Request) {
	b, err := h.Broadcasts.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, broadcastOf(b))
}

func (h *Content) sendBroadcast(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Audience string            `json:"audience"`
		UserIDs  []string          `json:"user_ids"`
		Title    map[string]string `json:"title"`
		Body     map[string]string `json:"body"`
		Link     string            `json:"link"`
		Email    bool              `json:"email"`
		Actor    string            `json:"actor"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := needActor(body.Actor); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	b, err := h.Broadcasts.Send(r.Context(), application.BroadcastInput{
		Audience: body.Audience, UserIDs: body.UserIDs, Title: body.Title, Body: body.Body, Link: body.Link, Email: body.Email,
	}, body.Actor)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, broadcastOf(b))
}
