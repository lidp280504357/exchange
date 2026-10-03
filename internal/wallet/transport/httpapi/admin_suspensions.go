package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/wallet/application"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

// The admin console's view of suspended withdrawals (custody shortfall
// suspension, review B4; C5.5 ⑯): the operations and audit events of
// exchangectl wallet withdrawals-suspended|suspend|resume, with the
// administrator as actor.

// SuspensionJSON is an asset whose withdrawals are suspended: new
// requests are refused and approved ones wait until it is lifted.
type SuspensionJSON struct {
	Asset string `json:"asset"`
	// Shortfall is what the custody checks found missing (0 for an
	// operator's suspension).
	Shortfall   string `json:"shortfall"`
	Reason      string `json:"reason"`
	SuspendedBy string `json:"suspended_by"`
	SuspendedAt string `json:"suspended_at"`
}

// SuspensionJSONOf renders a suspension.
func SuspensionJSONOf(x domain.Suspension) SuspensionJSON {
	return SuspensionJSON{
		Asset: x.Asset, Shortfall: x.Shortfall.String(), Reason: x.Reason, SuspendedBy: x.SuspendedBy, SuspendedAt: httpx.FormatTime(x.SuspendedAt),
	}
}

func (h *Handler) suspensionRoutes(r chi.Router) {
	r.Get("/internal/wallet/suspensions", h.adminSuspensions)
	r.Post("/internal/wallet/suspensions/{asset}/suspend", h.adminSuspend)
	r.Post("/internal/wallet/suspensions/{asset}/resume", h.adminResume)
}

func (h *Handler) adminSuspensions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Suspensions(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]SuspensionJSON, 0, len(list))
	for _, x := range list {
		items = append(items, SuspensionJSONOf(x))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type suspensionBody struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

// adminSuspend stops an asset's withdrawals (409 when they are stopped
// already, 404 for an asset no network withdraws).
func (h *Handler) adminSuspend(w http.ResponseWriter, r *http.Request) {
	var body suspensionBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	x, err := application.SuspendWithdrawals(r.Context(), h.Svc.Store, h.Svc.Withdrawable, chi.URLParam(r, "asset"), body.Actor, body.Reason,
		h.Svc.Now())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, SuspensionJSONOf(x))
}

// adminResume lifts an asset's suspension (404 when there is none); the
// approved withdrawals go out within a round, and the custody checks
// start over.
func (h *Handler) adminResume(w http.ResponseWriter, r *http.Request) {
	var body suspensionBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	x, err := application.ResumeWithdrawals(r.Context(), h.Svc.Store, application.Resume{
		Asset: chi.URLParam(r, "asset"), Actor: body.Actor, Reason: body.Reason,
	}, h.Svc.Now())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, SuspensionJSONOf(x))
}
