package httpapi

import (
	"net/http"

	"github.com/skill/exchange/internal/platform/httpx"
)

// insuranceFunds answers GET /admin/v1/derivatives/insurance-funds: the
// insurance fund of every settlement asset (coin-margined design
// 2026-10-06 §2.7).
func (h *Handler) insuranceFunds(w http.ResponseWriter, r *http.Request) {
	funds, err := h.Svc.InsuranceFunds(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"funds": funds})
}
