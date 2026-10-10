package httpapi

import (
	"net/http"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The console's access switches (design 2026-10-02, N1): whether sign-in
// asks for the authenticator code, on the settings page; removing one's
// own authenticator on the account page.

func accessJSON(v application.AccessView) map[string]any {
	unbound := make([]map[string]string, 0, len(v.Unbound))
	for _, a := range v.Unbound {
		unbound = append(unbound, map[string]string{"id": a.ID, "email": a.Email, "name": a.Name, "role": a.Role})
	}
	return map[string]any{
		"require_totp": v.RequireTOTP, "updated_by": optText(v.UpdatedBy), "updated_at": optTime(v.UpdatedAt),
		"you_bound": v.YouBound, "bound_admins": v.BoundAdmins, "unbound": unbound, "unbound_count": v.UnboundCount,
	}
}

func (h *Handler) access(w http.ResponseWriter, r *http.Request) {
	v, err := h.Svc.Access(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, accessJSON(v))
}

func (h *Handler) setRequireTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool  `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.Enabled == nil {
		httpx.WriteError(w, r, apperr.Invalid("enabled is required"))
		return
	}
	v, err := h.Svc.SetRequireTOTP(r.Context(), principal(r), *body.Enabled, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, accessJSON(v))
}

func (h *Handler) removeOwnTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		Code    string `json:"totp_code"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.throttle(w, r, RuleOwn, principal(r).Admin.ID) {
		return
	}
	if err := h.Svc.RemoveOwnTOTP(r.Context(), principal(r), body.Current, body.Code); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
