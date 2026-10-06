package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/platform/httpx"
)

// A coin's contracts closed or reopened at once (coin-margined design
// 2026-10-06 §3.5, A63): the preview, then the confirmed change (202).

// previewCoinStatus answers what closing (to CANCEL_ONLY) or reopening (to
// TRADING) a coin's contracts does (A63, §3.5).
func (h *Handler) previewCoinStatus(w http.ResponseWriter, r *http.Request) {
	var body statusBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	prev, err := h.Svc.PreviewCoinStatus(r.Context(), principal(r), chi.URLParam(r, "coin"), body.To)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prev)
}

// setCoinStatus records the change that closes or reopens a coin's
// contracts: 202 with the change that waits.
func (h *Handler) setCoinStatus(w http.ResponseWriter, r *http.Request) {
	var body statusBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.Svc.SetCoinStatus(r.Context(), principal(r), chi.URLParam(r, "coin"), body.To, body.Reason, body.Confirmation)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, struct {
		Coin      string                         `json:"coin"`
		To        string                         `json:"to"`
		Contracts []application.CoinContractMove `json:"contracts"`
		Change    InstrumentChangeJSON           `json:"change"`
	}{Coin: res.Coin, To: res.To, Contracts: res.Contracts, Change: instrumentChangeJSON(res.Change)})
}
