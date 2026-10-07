// Package httpapi serves user-service's REST endpoints (api/openapi/user.yaml).
package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/tracing"
	"github.com/skill/exchange/internal/user/application"
	"github.com/skill/exchange/internal/user/domain"
)

const headerStepUp = "X-Step-Up-Token"

// Handler serves the profile and eligibility endpoints; every route needs
// the identity the gateway attaches.
type Handler struct {
	Svc *application.Service
}

// Routes mounts the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(requireUser)
		r.Get("/v1/user/profile", h.profile)
		r.Patch("/v1/user/profile", h.updateProfile)
		r.Get("/v1/user/eligibility", h.eligibility)
		r.Get("/v1/user/favorites", h.favorites)
		r.Put("/v1/user/favorites", h.setFavorites)
		r.Put("/v1/user/username", h.changeUsername)
		r.Post("/v1/user/avatar", h.uploadAvatar)
		r.Delete("/v1/user/avatar", h.deleteAvatar)
	})
}

type favoritesJSON struct {
	Symbols   []string `json:"symbols"`
	UpdatedAt *string  `json:"updated_at"`
}

func toFavorites(symbols []string, at time.Time) favoritesJSON {
	out := favoritesJSON{Symbols: symbols}
	if out.Symbols == nil {
		out.Symbols = []string{}
	}
	if !at.IsZero() {
		s := httpx.FormatTime(at)
		out.UpdatedAt = &s
	}
	return out
}

func (h *Handler) favorites(w http.ResponseWriter, r *http.Request) {
	list, at, err := h.Svc.Favorites(r.Context(), httpx.UserID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toFavorites(list, at))
}

func (h *Handler) setFavorites(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbols []string `json:"symbols"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	list, at, err := h.Svc.SetFavorites(r.Context(), httpx.UserID(r), body.Symbols)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toFavorites(list, at))
}

func requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httpx.UserID(r) == "" {
			httpx.WriteError(w, r, apperr.Unauthorized("sign in first"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type profileResponse struct {
	UserID            string  `json:"user_id"`
	Username          string  `json:"username"`
	UsernameChangedAt *string `json:"username_changed_at"`
	AvatarURL         *string `json:"avatar_url"`
	AvatarThumbURL    *string `json:"avatar_thumb_url"`
	Status            string  `json:"status"`
	Region            string  `json:"region"`
	Language          string  `json:"language"`
	Timezone          string  `json:"timezone"`
	AntiPhishingCode  string  `json:"anti_phishing_code"`
	KYCLevel          int     `json:"kyc_level"`
	Version           int64   `json:"version"`
	CreatedAt         string  `json:"created_at"`
}

func toResponse(u domain.User) profileResponse {
	out := profileResponse{
		UserID: u.ID, Username: u.Username, Status: u.Status, Region: u.Region, Language: u.Language, Timezone: u.Timezone,
		AntiPhishingCode: u.AntiPhishingCode, KYCLevel: u.KYCLevel, Version: u.Version, CreatedAt: httpx.FormatTime(u.CreatedAt),
	}
	if !u.UsernameChangedAt.IsZero() {
		at := httpx.FormatTime(u.UsernameChangedAt)
		out.UsernameChangedAt = &at
	}
	if u.Avatar != nil {
		url, thumb := u.Avatar.URL(), u.Avatar.ThumbURL()
		out.AvatarURL, out.AvatarThumbURL = &url, &thumb
	}
	return out
}

func (h *Handler) changeUsername(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, err := h.Svc.ChangeUsername(r.Context(), httpx.UserID(r), body.Username)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(u))
}

// maxUploadBytes bounds an upload's request: the image's 5 MB and the
// multipart's own (nginx takes 8m).
const maxUploadBytes = 8 << 20

// uploadAvatar takes one part, file (design 2026-10-07, avatars and
// usernames §1.2).
func (h *Handler) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	upload, err := readUpload(r)
	if errors.Is(err, domain.ErrAvatarTooLarge) {
		writeTooLarge(w, r)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, err := h.Svc.UploadAvatar(r.Context(), httpx.UserID(r), upload)
	if errors.Is(err, domain.ErrAvatarTooLarge) {
		writeTooLarge(w, r)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(u))
}

// readUpload reads the file part of a multipart upload, at most one byte
// over the limit (so that one too large is told apart).
func readUpload(r *http.Request) ([]byte, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, apperr.Invalid("the upload must be multipart/form-data with a file part")
	}
	for {
		part, err := mr.NextPart()
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			return nil, domain.ErrAvatarTooLarge
		case errors.Is(err, io.EOF):
			return nil, apperr.Invalid("the upload has no file part")
		case err != nil:
			return nil, apperr.Invalid("the upload is not readable multipart/form-data")
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, domain.MaxAvatarBytes+1))
		_ = part.Close()
		if errors.As(err, &tooBig) || len(data) > domain.MaxAvatarBytes {
			return nil, domain.ErrAvatarTooLarge
		}
		if err != nil {
			return nil, apperr.Invalid("the upload is not readable multipart/form-data")
		}
		return data, nil
	}
}

// writeTooLarge answers USER_AVATAR_TOO_LARGE with 413 (apperr has no
// such kind).
func writeTooLarge(w http.ResponseWriter, r *http.Request) {
	e := apperr.From(domain.ErrAvatarTooLarge)
	httpx.WriteJSON(w, domain.AvatarTooLargeStatus, httpx.ErrorBody{
		Code: e.Code, Message: e.Message, TraceID: tracing.TraceID(r.Context()),
	})
}

func (h *Handler) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	u, err := h.Svc.DeleteAvatar(r.Context(), httpx.UserID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(u))
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) {
	u, err := h.Svc.Get(r.Context(), httpx.UserID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(u))
}

type profilePatch struct {
	Language         *string `json:"language"`
	Timezone         *string `json:"timezone"`
	AntiPhishingCode *string `json:"anti_phishing_code"`
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	var body profilePatch
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, err := h.Svc.UpdateProfile(r.Context(), httpx.UserID(r), domain.ProfilePatch{
		Language: body.Language, Timezone: body.Timezone, AntiPhishingCode: body.AntiPhishingCode,
	}, r.Header.Get(headerStepUp))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(u))
}

type eligibilityResponse struct {
	Feature    string `json:"feature"`
	Allowed    bool   `json:"allowed"`
	ReasonCode string `json:"reason_code,omitempty"`
}

func (h *Handler) eligibility(w http.ResponseWriter, r *http.Request) {
	feature := r.URL.Query().Get("feature")
	allowed, reason, err := h.Svc.CheckEligibility(r.Context(), httpx.UserID(r), feature,
		r.URL.Query().Get("asset"), r.URL.Query().Get("symbol"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, eligibilityResponse{Feature: feature, Allowed: allowed, ReasonCode: reason})
}
