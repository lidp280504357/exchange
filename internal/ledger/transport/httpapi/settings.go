package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/ledger/application"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The admin console's ledger settings (design 2026-10-04 §4.2) and
// instrument-service's read of them for the platform profile; the gateway
// does not route /internal.
func (h *Handler) settingsRoutes(r chi.Router) {
	r.Get("/internal/ledger/settings/welcome-credits", h.welcomeCredits)
	r.Put("/internal/ledger/settings/welcome-credits", h.setWelcomeCredits)
}

// WelcomeCreditJSON is an asset and its amount (a decimal string).
type WelcomeCreditJSON struct {
	Asset  string `json:"asset"`
	Amount string `json:"amount"`
}

// WelcomeCreditsJSON is the welcome credits setting.
type WelcomeCreditsJSON struct {
	Credits []WelcomeCreditJSON `json:"credits"`
	// FlagEnabled is ledger.welcome_credit for everyone, the master switch.
	FlagEnabled bool    `json:"flag_enabled"`
	Version     int64   `json:"version"`
	UpdatedBy   string  `json:"updated_by"`
	UpdatedAt   *string `json:"updated_at"`
}

// WelcomeCreditsJSONOf renders the setting with the flag's state.
func WelcomeCreditsJSONOf(w domain.WelcomeCredits, flagEnabled bool) WelcomeCreditsJSON {
	out := WelcomeCreditsJSON{Credits: []WelcomeCreditJSON{}, FlagEnabled: flagEnabled, Version: w.Version, UpdatedBy: w.UpdatedBy}
	for _, c := range w.Credits {
		out.Credits = append(out.Credits, WelcomeCreditJSON{Asset: c.Asset, Amount: c.Amount.String()})
	}
	if !w.UpdatedAt.IsZero() {
		at := httpx.FormatTime(w.UpdatedAt)
		out.UpdatedAt = &at
	}
	return out
}

func (h *Handler) flagOn() bool {
	return h.Svc.Flags != nil && h.Svc.Flags.Enabled(flags.KeyWelcomeCredit, flags.Subject{})
}

func (h *Handler) welcomeCredits(w http.ResponseWriter, r *http.Request) {
	s, err := h.Svc.WelcomeCredits(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, WelcomeCreditsJSONOf(s, h.flagOn()))
}

func (h *Handler) setWelcomeCredits(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Credits         []WelcomeCreditJSON `json:"credits"`
		ExpectedVersion *int64              `json:"expected_version"`
		Actor           string              `json:"actor"`
		Reason          string              `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.ExpectedVersion == nil || body.Credits == nil {
		httpx.WriteError(w, r, apperr.Invalid("credits and expected_version are required"))
		return
	}
	credits := make([]application.Credit, 0, len(body.Credits))
	for _, c := range body.Credits {
		amount, err := decimal.NewFromString(c.Amount)
		if err != nil {
			httpx.WriteError(w, r, apperr.Invalid("credits: amount "+c.Amount+" is not a decimal"))
			return
		}
		credits = append(credits, application.Credit{Asset: c.Asset, Amount: amount})
	}
	s, err := h.Svc.SetWelcomeCredits(r.Context(), credits, *body.ExpectedVersion, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, WelcomeCreditsJSONOf(s, h.flagOn()))
}
