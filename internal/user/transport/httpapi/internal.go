package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// InternalRoutes mounts the account kinds' internal endpoints (L0,
// api/internal/users.yaml), on the compose network only: the gateway does
// not route /internal, and a request carrying a caller's X-User-Id came
// through it and is not served.
func (h *Handler) InternalRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(internalOnly)
		r.Put("/internal/users/{id}/kind", h.setKind)
		r.Get("/internal/users/ids", h.idsOfKinds)
	})
}

// internalOnly refuses a request that carries a caller's identity.
func internalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httpx.UserID(r) != "" {
			httpx.WriteError(w, r, apperr.NotFound("no such endpoint"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setKind sets an account's kind: {kind, reason, actor?}; the actor
// defaults to "internal". The kind the account has already changes
// nothing (changed false).
func (h *Handler) setKind(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason"`
		Actor  string `json:"actor"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.Actor == "" {
		body.Actor = "internal"
	}
	id := chi.URLParam(r, "id")
	c, changed, err := h.Svc.SetKind(r.Context(), id, body.Kind, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_id": id, "from": c.From, "to": c.To, "changed": changed})
}

// idsOfKinds returns the accounts of ?kind= (comma-separated or repeated):
// {user_ids}, with an ETag of the list and a minute's caching.
func (h *Handler) idsOfKinds(w http.ResponseWriter, r *http.Request) {
	var kinds []string
	for _, v := range r.URL.Query()["kind"] {
		for k := range strings.SplitSeq(v, ",") {
			if k = strings.TrimSpace(k); k != "" {
				kinds = append(kinds, k)
			}
		}
	}
	ids, err := h.Svc.IDsOfKinds(r.Context(), kinds)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	sum := sha256.Sum256([]byte(strings.Join(ids, ",")))
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "max-age=60")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_ids": ids})
}
