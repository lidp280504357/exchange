package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The platform's settings and the launch checklist (design 2026-10-04,
// D2): the profile the sites show and its images, the welcome credits,
// and what of the learning setup is still on.

func (h *Handler) platformProfile(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.PlatformProfile(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// updatePlatformProfile takes the profile's fields with expected_version
// and the reason, which is the audit's, not the profile's.
func (h *Handler) updatePlatformProfile(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var reason string
	if raw, ok := body["reason"]; ok && json.Unmarshal(raw, &reason) != nil {
		httpx.WriteError(w, r, apperr.Invalid("reason is a text"))
		return
	}
	delete(body, "reason")
	write, _ := json.Marshal(body)
	raw, err := h.Svc.UpdatePlatformProfile(r.Context(), principal(r), write, reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) putPlatformImage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data   string `json:"data"`
		MIME   string `json:"mime"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.PutPlatformImage(r.Context(), principal(r), chi.URLParam(r, "kind"), body.Data, body.MIME, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) deletePlatformImage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.DeletePlatformImage(r.Context(), principal(r), chi.URLParam(r, "kind"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) welcomeCredits(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.WelcomeCredits(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// setWelcomeCredits answers 200 with the setting when it applied, 202 with
// the request when a second ADMIN must approve it.
func (h *Handler) setWelcomeCredits(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Credits         []ports.WelcomeCredit `json:"credits"`
		ExpectedVersion *int64                `json:"expected_version"`
		Reason          string                `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.ExpectedVersion == nil {
		httpx.WriteError(w, r, apperr.Invalid("expected_version is the version read"))
		return
	}
	res, err := h.Svc.SetWelcomeCredits(r.Context(), principal(r), body.Credits, *body.ExpectedVersion, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if res.Approval != nil {
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"approval": approvalJSON(*res.Approval)})
		return
	}
	writeRaw(w, res.Setting)
}

// LaunchItemJSON is one item of the launch checklist.
type LaunchItemJSON struct {
	Key    string         `json:"key"`
	Status string         `json:"status"`
	Value  map[string]any `json:"value"`
}

// launchChecklist reads every item now; the console's host is the one the
// request came to (nginx passes it on).
func (h *Handler) launchChecklist(w http.ResponseWriter, r *http.Request) {
	c, err := h.Svc.LaunchChecklist(r.Context(), principal(r), r.Host)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]LaunchItemJSON, 0, len(c.Items))
	for _, it := range c.Items {
		items = append(items, LaunchItemJSON{Key: it.Key, Status: it.Status, Value: it.Value})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ready": c.Ready, "items": items, "checked_at": httpx.FormatTime(c.CheckedAt)})
}
