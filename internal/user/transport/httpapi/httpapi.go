// Package httpapi serves user-service's REST endpoints (api/openapi/user.yaml).
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
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
	UserID           string `json:"user_id"`
	Status           string `json:"status"`
	Region           string `json:"region"`
	Language         string `json:"language"`
	Timezone         string `json:"timezone"`
	AntiPhishingCode string `json:"anti_phishing_code"`
	KYCLevel         int    `json:"kyc_level"`
	Version          int64  `json:"version"`
	CreatedAt        string `json:"created_at"`
}

func toResponse(u domain.User) profileResponse {
	return profileResponse{
		UserID: u.ID, Status: u.Status, Region: u.Region, Language: u.Language, Timezone: u.Timezone,
		AntiPhishingCode: u.AntiPhishingCode, KYCLevel: u.KYCLevel, Version: u.Version, CreatedAt: httpx.FormatTime(u.CreatedAt),
	}
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
