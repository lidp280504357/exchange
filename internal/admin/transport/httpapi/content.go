package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Operations content (design 2026-10-02 §4.5): the articles and in-app
// messages notification-service keeps, as it renders them.

func (h *Handler) articles(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.Articles(r.Context(), principal(r), r.URL.Query().Get("section"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) article(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.Article(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

type articleBody struct {
	Section  string              `json:"section"`
	Slug     string              `json:"slug"`
	Modes    string              `json:"modes"`
	Category string              `json:"category"`
	Pinned   bool                `json:"pinned"`
	Order    int                 `json:"order"`
	Texts    []ports.ArticleText `json:"texts"`
	Version  int                 `json:"version"`
	Reason   string              `json:"reason"`
}

func (b articleBody) write() ports.ArticleWrite {
	return ports.ArticleWrite{
		Section: b.Section, Slug: b.Slug, Modes: b.Modes, Category: b.Category, Pinned: b.Pinned, Order: b.Order, Texts: b.Texts, Version: b.Version,
	}
}

func (h *Handler) createArticle(w http.ResponseWriter, r *http.Request) {
	var body articleBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.CreateArticle(r.Context(), principal(r), body.write(), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, raw)
}

func (h *Handler) updateArticle(w http.ResponseWriter, r *http.Request) {
	var body articleBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.UpdateArticle(r.Context(), principal(r), chi.URLParam(r, "id"), body.write(), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) publishArticle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version   int        `json:"version"`
		PublishAt *time.Time `json:"publish_at"`
		Reason    string     `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.PublishArticle(r.Context(), principal(r), chi.URLParam(r, "id"), body.Version, body.PublishAt, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) archiveArticle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int    `json:"version"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.ArchiveArticle(r.Context(), principal(r), chi.URLParam(r, "id"), body.Version, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) broadcasts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	raw, err := h.Svc.Broadcasts(r.Context(), principal(r), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) broadcast(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.Broadcast(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) resumeBroadcast(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.ResumeBroadcast(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) sendBroadcast(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Audience string            `json:"audience"`
		UserID   string            `json:"user_id"`
		Tag      string            `json:"tag"`
		Title    map[string]string `json:"title"`
		Body     map[string]string `json:"body"`
		Link     string            `json:"link"`
		Email    bool              `json:"email"`
		Reason   string            `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.SendBroadcast(r.Context(), principal(r), application.BroadcastInput{
		Audience: body.Audience, UserID: body.UserID, Tag: body.Tag, Title: body.Title, Body: body.Body, Link: body.Link, Email: body.Email,
		Key: idemKey(r),
	}, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, raw)
}
