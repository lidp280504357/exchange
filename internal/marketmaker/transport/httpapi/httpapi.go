// Package httpapi is market-maker's internal API: HOUSE's runtime caps
// (review C45), for the console (admin-service; the gateway does not pass
// /internal). Amounts are decimal strings in USDT; the leverage a number
// of times.
package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketmaker/application"
	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Handler serves the caps.
type Handler struct{ Caps *application.Caps }

// Routes registers the handlers on r.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/internal/house/caps", h.get)
	r.Put("/internal/house/caps", h.put)
	r.Get("/internal/house/caps/changes", h.changes)
}

// capsJSON are the caps as the API gives them.
type capsJSON struct {
	Level            string `json:"level"`
	Symbol           string `json:"symbol"`
	Total            string `json:"total"`
	Contract         string `json:"contract"`
	Safety           string `json:"safety"`
	ContractLeverage string `json:"contract_leverage"`
}

func toJSON(c domain.Caps) capsJSON {
	return capsJSON{
		Level: c.Level.String(), Symbol: c.Symbol.String(), Total: c.Total.String(), Contract: c.Contract.String(),
		Safety: c.Safety.String(), ContractLeverage: c.ContractLeverage.String(),
	}
}

type storedJSON struct {
	capsJSON
	Version   int64  `json:"version"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

func stored(s domain.StoredCaps) storedJSON {
	return storedJSON{capsJSON: toJSON(s.Caps), Version: s.Version, UpdatedBy: s.UpdatedBy, UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func (h *Handler) get(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, stored(h.Caps.Get()))
}

// putBody changes the caps of version: the caps given (decimal strings),
// the others as they are.
type putBody struct {
	Level            *string `json:"level"`
	Symbol           *string `json:"symbol"`
	Total            *string `json:"total"`
	Contract         *string `json:"contract"`
	Safety           *string `json:"safety"`
	ContractLeverage *string `json:"contract_leverage"`
	Version          int64   `json:"version"`
	Actor            string  `json:"actor"`
	Approver         string  `json:"approver"`
	ApprovalID       string  `json:"approval_id"`
	Reason           string  `json:"reason"`
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var b putBody
	if err := httpx.DecodeJSON(w, r, &b); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	caps := h.Caps.Get().Caps
	for name, f := range map[string]struct {
		in  *string
		out *decimal.Decimal
	}{
		"level": {b.Level, &caps.Level}, "symbol": {b.Symbol, &caps.Symbol}, "total": {b.Total, &caps.Total},
		"contract": {b.Contract, &caps.Contract}, "safety": {b.Safety, &caps.Safety}, "contract_leverage": {b.ContractLeverage, &caps.ContractLeverage},
	} {
		if f.in == nil {
			continue
		}
		v, err := decimal.NewFromString(*f.in)
		if err != nil {
			httpx.WriteError(w, r, apperr.Invalid(name+" must be a decimal string"))
			return
		}
		*f.out = v
	}
	s, err := h.Caps.Change(r.Context(), domain.CapsChange{
		Caps: caps, Version: b.Version, Actor: b.Actor, Approver: b.Approver, ApprovalID: b.ApprovalID, Reason: b.Reason,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stored(s))
}

type changeJSON struct {
	Version    int64     `json:"version"`
	Caps       capsJSON  `json:"caps"`
	Previous   *capsJSON `json:"previous"`
	Actor      string    `json:"actor"`
	Approver   string    `json:"approver"`
	ApprovalID string    `json:"approval_id"`
	Reason     string    `json:"reason"`
	At         string    `json:"at"`
}

func (h *Handler) changes(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := h.Caps.Changes(r.Context(), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]changeJSON, 0, len(list))
	for _, c := range list {
		j := changeJSON{
			Version: c.Version, Caps: toJSON(c.Caps), Actor: c.Actor, Approver: c.Approver, ApprovalID: c.ApprovalID, Reason: c.Reason,
			At: c.At.UTC().Format(time.RFC3339Nano),
		}
		if c.Previous != nil {
			p := toJSON(*c.Previous)
			j.Previous = &p
		}
		out = append(out, j)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}
