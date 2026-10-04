package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/notification/application"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Notices serves the in-app inbox; every route needs the identity the
// gateway attaches.
type Notices struct {
	Svc *application.Notices
}

// Routes mounts the endpoints on r.
func (h *Notices) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if httpx.UserID(r) == "" {
					httpx.WriteError(w, r, apperr.Unauthorized("sign in first"))
					return
				}
				next.ServeHTTP(w, r)
			})
		})
		r.Get("/v1/notifications", h.list)
		r.Post("/v1/notifications/read", h.read)
	})
}

type noticeItem struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Data      map[string]string `json:"data"`
	Read      bool              `json:"read"`
	CreatedAt string            `json:"created_at"`
}

func (h *Notices) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, unread, err := h.Svc.List(r.Context(), httpx.UserID(r), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]noticeItem, 0, len(items))
	for _, n := range items {
		data := n.Data
		if data == nil {
			data = map[string]string{}
		}
		out = append(out, noticeItem{
			ID: n.ID, Type: n.Type, Title: n.Title, Body: n.Body, Data: data, Read: !n.ReadAt.IsZero(), CreatedAt: httpx.FormatTime(n.CreatedAt),
		})
	}
	resp := map[string]any{"items": out, "next_cursor": nil, "unread_count": unread}
	if next != "" {
		resp["next_cursor"] = next
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type readBody struct {
	IDs []string `json:"ids"`
	All bool     `json:"all"`
}

func (h *Notices) read(w http.ResponseWriter, r *http.Request) {
	var body readBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.All == (len(body.IDs) > 0) {
		httpx.WriteError(w, r, apperr.Invalid("give either ids or all=true"))
		return
	}
	n, err := h.Svc.MarkRead(r.Context(), httpx.UserID(r), body.IDs)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int64{"updated": n})
}
