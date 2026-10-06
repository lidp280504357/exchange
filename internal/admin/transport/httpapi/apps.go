package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The apps to download (design 2026-10-07, App download page; batch H1):
// the platforms' settings, the uploads in parts and the files kept.

func (h *Handler) appRoutes(r chi.Router) {
	r.Get("/platform/apps", h.platformApps)
	r.Put("/platform/apps/{platform}", h.setPlatformApp)
	r.Post("/platform/apps/{platform}/uploads", h.startAppUpload)
	r.Get("/platform/apps/{platform}/uploads/{upload_id}", h.appUpload)
	r.Delete("/platform/apps/{platform}/uploads/{upload_id}", h.dropAppUpload)
	r.Put("/platform/apps/{platform}/uploads/{upload_id}/parts/{n}", h.putAppUploadPart)
	r.Post("/platform/apps/{platform}/uploads/{upload_id}/complete", h.completeAppUpload)
	r.Delete("/platform/apps/{platform}/files/{file_id}", h.deleteAppFile)
}

// AppUploadJSON is an upload as the console sees it.
type AppUploadJSON struct {
	UploadID  string `json:"upload_id"`
	Platform  string `json:"platform"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	PartSize  int64  `json:"part_size"`
	Parts     int    `json:"parts"`
	Received  []int  `json:"received"`
	ExpiresAt string `json:"expires_at"`
	StartedBy string `json:"started_by"`
}

func appUploadJSON(u domain.AppUpload) AppUploadJSON {
	received := u.Received
	if received == nil {
		received = []int{}
	}
	return AppUploadJSON{
		UploadID: u.ID, Platform: u.Platform, Kind: u.Kind, Name: u.Name, Size: u.Size, SHA256: u.SHA256, PartSize: u.PartSize, Parts: u.Parts(),
		Received: received, ExpiresAt: httpx.FormatTime(u.ExpiresAt), StartedBy: u.StartedBy,
	}
}

func (h *Handler) platformApps(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.PlatformApps(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) setPlatformApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode            string            `json:"mode"`
		LinkURL         string            `json:"link_url"`
		Notes           map[string]string `json:"notes"`
		Enabled         *bool             `json:"enabled"`
		ExpectedVersion *int64            `json:"expected_version"`
		Reason          string            `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.Enabled == nil || body.ExpectedVersion == nil {
		httpx.WriteError(w, r, apperr.Invalid("enabled and expected_version are required"))
		return
	}
	write, _ := json.Marshal(map[string]any{
		"mode": body.Mode, "link_url": body.LinkURL, "notes": body.Notes, "enabled": *body.Enabled, "expected_version": *body.ExpectedVersion,
	})
	raw, err := h.Svc.SetPlatformApp(r.Context(), principal(r), chi.URLParam(r, "platform"), write, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) startAppUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, err := h.Svc.StartAppUpload(r.Context(), principal(r), chi.URLParam(r, "platform"), body.Kind, body.Name, body.Size, body.SHA256)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, appUploadJSON(u))
}

func (h *Handler) appUpload(w http.ResponseWriter, r *http.Request) {
	u, err := h.Svc.AppUpload(r.Context(), principal(r), chi.URLParam(r, "platform"), chi.URLParam(r, "upload_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, appUploadJSON(u))
}

func (h *Handler) dropAppUpload(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.DropAppUpload(r.Context(), principal(r), chi.URLParam(r, "platform"), chi.URLParam(r, "upload_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putAppUploadPart takes a part's bytes as they come (application/
// octet-stream), at most a part's size and one byte more: the service
// refuses any but the exact length.
func (h *Handler) putAppUploadPart(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("n is a part number"))
		return
	}
	body := http.MaxBytesReader(w, r.Body, domain.AppPartSize+1)
	u, err := h.Svc.PutAppUploadPart(r.Context(), principal(r), chi.URLParam(r, "platform"), chi.URLParam(r, "upload_id"), n, body)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, appUploadJSON(u))
}

func (h *Handler) completeAppUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.CompleteAppUpload(r.Context(), principal(r), chi.URLParam(r, "platform"), chi.URLParam(r, "upload_id"), body.Reason, r.Host)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) deleteAppFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.DeleteAppFile(r.Context(), principal(r), chi.URLParam(r, "platform"), chi.URLParam(r, "file_id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}
