package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/platform/httpx"
)

// HOUSE's caps (A69): read with the request that waits, the latest
// changes and the first version's caps; a change asked for answers 202
// with its request.

func (h *Handler) houseCaps(w http.ResponseWriter, r *http.Request) {
	v, err := h.Svc.HouseCapsOf(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := struct {
		Caps    application.HouseCaps         `json:"caps"`
		Pending *ApprovalJSON                 `json:"pending"`
		Changes []application.HouseCapsChange `json:"changes"`
		Initial json.RawMessage               `json:"initial"`
	}{Caps: v.Caps, Changes: v.Changes, Initial: v.Initial}
	if v.Pending != nil {
		a := approvalJSON(*v.Pending)
		out.Pending = &a
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) requestHouseCaps(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Caps    map[string]string `json:"caps"`
		Version int64             `json:"version"`
		Reason  string            `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.RequestHouseCaps(r.Context(), principal(r), application.HouseCapsRequest{Caps: body.Caps, Version: body.Version, Reason: body.Reason})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, approvalJSON(a))
}
