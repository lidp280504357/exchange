package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/httpx"
)

// optTime is a time in the API's format, nil when zero.
func optTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := httpx.FormatTime(t)
	return &s
}

// IdentityJSON is one of an account's sign-in identities.
type IdentityJSON struct {
	Kind       string  `json:"kind"`
	Value      string  `json:"value"`
	VerifiedAt *string `json:"verified_at"`
	CreatedAt  *string `json:"created_at"`
}

func identitiesJSON(list []ports.Identity) []IdentityJSON {
	out := make([]IdentityJSON, 0, len(list))
	for _, id := range list {
		out = append(out, IdentityJSON{Kind: id.Kind, Value: id.Value, VerifiedAt: optTime(id.VerifiedAt), CreatedAt: optTime(id.CreatedAt)})
	}
	return out
}

// SessionJSON is a live session.
type SessionJSON struct {
	ID         string  `json:"id"`
	DeviceID   string  `json:"device_id"`
	ClientType string  `json:"client_type"`
	UserAgent  string  `json:"user_agent"`
	IP         string  `json:"ip"`
	CreatedAt  *string `json:"created_at"`
	LastSeenAt *string `json:"last_seen_at"`
}

// DeviceJSON is a device an account signed in from.
type DeviceJSON struct {
	DeviceID    string  `json:"device_id"`
	FirstSeenAt *string `json:"first_seen_at"`
	LastSeenAt  *string `json:"last_seen_at"`
}

// SecurityJSON is an account's sign-in security.
type SecurityJSON struct {
	Identities []IdentityJSON `json:"identities"`
	TOTP       struct {
		// ACTIVE, PENDING or NONE.
		Status      string  `json:"status"`
		ActivatedAt *string `json:"activated_at"`
		// ChangedAt is its latest removal; withdrawals wait for review
		// for a day after it.
		ChangedAt *string `json:"changed_at"`
	} `json:"totp"`
	PasswordChangedAt       *string       `json:"password_changed_at"`
	LastLoginAt             *string       `json:"last_login_at"`
	LockedSeconds           int           `json:"locked_seconds"`
	Sessions                []SessionJSON `json:"sessions"`
	Devices                 []DeviceJSON  `json:"devices"`
	PendingIdentityRequests int           `json:"pending_identity_requests"`
}

func (h *Handler) userSecurity(w http.ResponseWriter, r *http.Request) {
	sec, err := h.Svc.UserSecurity(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := SecurityJSON{
		Identities: identitiesJSON(sec.Identities), PasswordChangedAt: optTime(sec.PasswordChangedAt), LastLoginAt: optTime(sec.LastLoginAt),
		LockedSeconds: sec.LockedSeconds, Sessions: make([]SessionJSON, 0, len(sec.Sessions)), Devices: make([]DeviceJSON, 0, len(sec.Devices)),
		PendingIdentityRequests: sec.PendingIdentityRequests,
	}
	out.TOTP.Status, out.TOTP.ActivatedAt, out.TOTP.ChangedAt = sec.TOTP, optTime(sec.TOTPActivatedAt), optTime(sec.TOTPChangedAt)
	if out.TOTP.Status == "" {
		out.TOTP.Status = "NONE"
	}
	for _, x := range sec.Sessions {
		out.Sessions = append(out.Sessions, SessionJSON{
			ID: x.ID, DeviceID: x.DeviceID, ClientType: x.ClientType, UserAgent: x.UserAgent, IP: x.IP, CreatedAt: optTime(x.CreatedAt),
			LastSeenAt: optTime(x.LastSeenAt),
		})
	}
	for _, d := range sec.Devices {
		out.Devices = append(out.Devices, DeviceJSON{DeviceID: d.ID, FirstSeenAt: optTime(d.FirstSeenAt), LastSeenAt: optTime(d.LastSeenAt)})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) revealContacts(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.RevealContacts(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"identities": identitiesJSON(list)})
}

// LoginJSON is a sign-in attempt.
type LoginJSON struct {
	ID           int64   `json:"id"`
	Method       string  `json:"method"`
	Result       string  `json:"result"`
	IdentityMask string  `json:"identity_mask"`
	DeviceID     string  `json:"device_id"`
	UserAgent    string  `json:"user_agent"`
	IP           string  `json:"ip"`
	NewDevice    bool    `json:"new_device"`
	CreatedAt    *string `json:"created_at"`
}

func (h *Handler) loginHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, next, err := h.Svc.LoginHistory(r.Context(), principal(r), chi.URLParam(r, "id"), q.Get("cursor"), intParam(q, "limit"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]LoginJSON, 0, len(list))
	for _, e := range list {
		out = append(out, LoginJSON{
			ID: e.ID, Method: e.Method, Result: e.Result, IdentityMask: e.IdentityMask, DeviceID: e.DeviceID, UserAgent: e.UserAgent,
			IP: e.IP, NewDevice: e.NewDevice, CreatedAt: optTime(e.CreatedAt),
		})
	}
	writePage(w, out, next)
}

func (h *Handler) revokeSessions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Reason    string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	n, err := h.Svc.RevokeUserSessions(r.Context(), principal(r), chi.URLParam(r, "id"), body.SessionID, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"revoked": n})
}

func (h *Handler) resetTOTP(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	removed, err := h.Svc.ResetUserTOTP(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"removed": removed})
}

func (h *Handler) passwordReset(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	pw, n, err := h.Svc.TemporaryPassword(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"temporary_password": pw, "sessions_revoked": n})
}

// StatusChangeJSON is a move of an account to another status.
type StatusChangeJSON struct {
	FromStatus string  `json:"from_status"`
	ToStatus   string  `json:"to_status"`
	ReasonCode string  `json:"reason_code"`
	Actor      string  `json:"actor"`
	At         *string `json:"at"`
}

// ConsentJSON is a document version an account accepted.
type ConsentJSON struct {
	Document   string  `json:"document"`
	Version    string  `json:"version"`
	AcceptedAt *string `json:"accepted_at"`
}

func (h *Handler) userHistory(w http.ResponseWriter, r *http.Request) {
	changes, consents, err := h.Svc.UserHistory(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := struct {
		StatusChanges []StatusChangeJSON `json:"status_changes"`
		Consents      []ConsentJSON      `json:"consents"`
	}{make([]StatusChangeJSON, 0, len(changes)), make([]ConsentJSON, 0, len(consents))}
	for _, c := range changes {
		out.StatusChanges = append(out.StatusChanges, StatusChangeJSON{FromStatus: c.From, ToStatus: c.To, ReasonCode: c.Reason, Actor: c.Actor, At: optTime(c.At)})
	}
	for _, c := range consents {
		out.Consents = append(out.Consents, ConsentJSON{Document: c.Document, Version: c.Version, AcceptedAt: optTime(c.AcceptedAt)})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// RuleHitJSON is a risk rule that matched.
type RuleHitJSON struct {
	Rule   string `json:"rule"`
	Score  int    `json:"score"`
	Detail string `json:"detail"`
}

// AssessmentJSON is an assessment of the risk rules.
type AssessmentJSON struct {
	ID              string        `json:"id"`
	SourceEventType string        `json:"source_event_type"`
	Score           int           `json:"score"`
	Action          string        `json:"action"`
	Hits            []RuleHitJSON `json:"hits"`
	Enforced        bool          `json:"enforced"`
	CreatedAt       *string       `json:"created_at"`
}

func (h *Handler) userRisk(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.UserRisk(r.Context(), principal(r), chi.URLParam(r, "id"), intParam(r.URL.Query(), "limit"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]AssessmentJSON, 0, len(list))
	for _, a := range list {
		hits := make([]RuleHitJSON, 0, len(a.Hits))
		for _, x := range a.Hits {
			hits = append(hits, RuleHitJSON{Rule: x.Rule, Score: x.Score, Detail: x.Detail})
		}
		out = append(out, AssessmentJSON{
			ID: a.ID, SourceEventType: a.SourceEventType, Score: a.Score, Action: a.Action, Hits: hits, Enforced: a.Enforced,
			CreatedAt: optTime(a.CreatedAt),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assessments": out})
}

// IdentityRequestJSON is an identity rebind request.
type IdentityRequestJSON struct {
	ID           string  `json:"id"`
	UserID       string  `json:"user_id"`
	Kind         string  `json:"kind"`
	NewValue     string  `json:"new_value"`
	CurrentValue string  `json:"current_value"`
	Status       string  `json:"status"`
	CreatedAt    *string `json:"created_at"`
	DecidedAt    *string `json:"decided_at"`
	DecidedBy    string  `json:"decided_by"`
	Reason       string  `json:"reason"`
}

func identityRequestJSON(x ports.IdentityRequest) IdentityRequestJSON {
	return IdentityRequestJSON{
		ID: x.ID, UserID: x.UserID, Kind: x.Kind, NewValue: x.NewValue, CurrentValue: x.CurrentValue, Status: x.Status,
		CreatedAt: optTime(x.CreatedAt), DecidedAt: optTime(x.DecidedAt), DecidedBy: x.DecidedBy, Reason: x.Reason,
	}
}

func (h *Handler) identityRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, next, err := h.Svc.IdentityRequests(r.Context(), principal(r), ports.IdentityRequestQuery{
		Status: q.Get("status"), UserID: q.Get("user_id"), Cursor: q.Get("cursor"), Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]IdentityRequestJSON, 0, len(list))
	for _, x := range list {
		out = append(out, identityRequestJSON(x))
	}
	writePage(w, out, next)
}

func (h *Handler) decideIdentityRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	x, err := h.Svc.DecideIdentityRequest(r.Context(), principal(r), chi.URLParam(r, "id"), body.Approve, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, identityRequestJSON(x))
}
