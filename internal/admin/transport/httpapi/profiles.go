package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/platform/httpx"
)

// An asset's profile (ASTRA design §5.3, C4c).

func (h *Handler) assetProfile(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.AssetProfile(r.Context(), principal(r), chi.URLParam(r, "code"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) updateAssetProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		application.ProfileInput
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.UpdateAssetProfile(r.Context(), principal(r), chi.URLParam(r, "code"), body.ProfileInput, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}
