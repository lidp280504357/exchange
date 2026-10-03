package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// The administrators and their roles (design 2026-10-02 §4.6, C4).

// ManagedAdminJSON is an administrator as the console lists them.
type ManagedAdminJSON struct {
	AdminJSON
	Status         string  `json:"status"`
	FailedAttempts int     `json:"failed_attempts"`
	LockedUntil    *string `json:"locked_until"`
	LastLoginAt    *string `json:"last_login_at"`
	CreatedAt      string  `json:"created_at"`
	Sessions       int     `json:"sessions"`
}

func managedAdminJSON(a domain.Admin, sessions int) ManagedAdminJSON {
	return ManagedAdminJSON{
		AdminJSON: adminJSON(a), Status: a.Status, FailedAttempts: a.FailedAttempts, LockedUntil: optTime(a.LockedUntil),
		LastLoginAt: optTime(a.LastLoginAt), CreatedAt: httpx.FormatTime(a.CreatedAt), Sessions: sessions,
	}
}

func optText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (h *Handler) roles(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"roles": h.Svc.Roles()})
}

func (h *Handler) admins(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Admins(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]ManagedAdminJSON, 0, len(list))
	for _, a := range list {
		out = append(out, managedAdminJSON(a.Admin, a.Sessions))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"admins": out})
}

func (h *Handler) createAdmin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email  string `json:"email"`
		Name   string `json:"name"`
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, c, err := h.Svc.CreateAdmin(r.Context(), principal(r), body.Email, body.Name, body.Role, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	view := managedAdminJSON(a, 0)
	writeSetup(w, http.StatusCreated, &view, c)
}

func (h *Handler) adminStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.SetAdminStatus(r.Context(), principal(r), chi.URLParam(r, "id"), body.Enabled, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, managedAdminJSON(a, 0))
}

func (h *Handler) adminRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.SetAdminRole(r.Context(), principal(r), chi.URLParam(r, "id"), body.Role, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, managedAdminJSON(a, 0))
}

func (h *Handler) adminPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.Svc.ResetAdminPassword(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeSetup(w, http.StatusOK, nil, c)
}

func (h *Handler) adminTOTPReset(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.Svc.ResetAdminTOTP(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeSetup(w, http.StatusOK, nil, c)
}

func (h *Handler) adminSessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.AdminSessions(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type sessionJSON struct {
		IP         string `json:"ip"`
		UserAgent  string `json:"user_agent"`
		CreatedAt  string `json:"created_at"`
		LastSeenAt string `json:"last_seen_at"`
		ExpiresAt  string `json:"expires_at"`
	}
	out := make([]sessionJSON, 0, len(list))
	for _, s := range list {
		out = append(out, sessionJSON{
			IP: s.IP, UserAgent: s.UserAgent, CreatedAt: httpx.FormatTime(s.CreatedAt), LastSeenAt: httpx.FormatTime(s.LastSeenAt),
			ExpiresAt: httpx.FormatTime(s.ExpiresAt),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (h *Handler) revokeAdminSessions(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.Svc.RevokeAdminSessions(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
