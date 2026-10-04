package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// simPlan returns a threshold target's plan (ASTRA A6).
func (h *Handler) simPlan(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.SimPlan(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// simTargetPreview previews a threshold target for the form: direction,
// price, duration_seconds, starts_at (A6).
func (h *Handler) simTargetPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw, err := h.Svc.SimTargetPreview(r.Context(), principal(r), ports.SimTargetQuery{
		Direction: q.Get("direction"), Price: q.Get("price"), DurationSeconds: intParam(q, "duration_seconds"), StartsAt: q.Get("starts_at"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}
