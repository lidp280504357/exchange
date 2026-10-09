package httpapi

import (
	"net/http"
	"time"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The product lines (design 2026-10-07, product switches, K3; admin.yaml
// Products).

// ProductStateJSON is a product line.
type ProductStateJSON struct {
	Product       string  `json:"product"`
	Enabled       bool    `json:"enabled"`
	Flag          string  `json:"flag"`
	Version       int64   `json:"version"`
	SwitchedAt    *string `json:"switched_at"`
	SwitchedBy    *string `json:"switched_by"`
	ClosedAt      *string `json:"closed_at"`
	OpenOrders    *int    `json:"open_orders"`
	OpenPositions *int    `json:"open_positions"`
}

func productsJSON(ps application.Products) map[string]any {
	stamp := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		v := httpx.FormatTime(*t)
		return &v
	}
	lines := make([]ProductStateJSON, 0, len(ps.Lines))
	for _, l := range ps.Lines {
		lines = append(lines, ProductStateJSON{
			Product: l.Product, Enabled: l.Enabled, Flag: l.Flag, Version: l.Version, SwitchedAt: stamp(l.SwitchedAt),
			SwitchedBy: optional(l.SwitchedBy), ClosedAt: stamp(l.ClosedAt), OpenOrders: l.OpenOrders, OpenPositions: l.OpenPositions,
		})
	}
	return map[string]any{"products": lines, "partial": ps.Partial}
}

func (h *Handler) products(w http.ResponseWriter, r *http.Request) {
	ps, err := h.Svc.Products(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, productsJSON(ps))
}

func (h *Handler) setProduct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Product string `json:"product"`
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
	ps, err := h.Svc.SetProduct(r.Context(), principal(r), body.Product, *body.Enabled, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, productsChangeJSON(ps))
}

// productsChangeJSON is a switch's answer: the lines after it, the orders
// it canceled and how its cancel went (null when it asked none; a reason
// and an error only when it FAILED).
func productsChangeJSON(ps application.Products) map[string]any {
	out := productsJSON(ps)
	out["canceled_orders"], out["cancel"] = 0, nil
	if c := ps.Cancel; c != nil {
		out["canceled_orders"] = c.Canceled
		out["cancel"] = ProductCancelJSON{
			Status: c.Status, Canceled: c.Canceled, FailedUsers: c.FailedUsers, Reason: optional(c.Reason), Error: optional(c.Error),
		}
	}
	return out
}

// ProductCancelJSON is how canceling a closed line's open orders went (A85,
// A87).
type ProductCancelJSON struct {
	Status      string  `json:"status"`
	Canceled    int     `json:"canceled"`
	FailedUsers int     `json:"failed_users"`
	Reason      *string `json:"reason"`
	Error       *string `json:"error"`
}

// SpotReduceOnlyJSON is spot's last closure with the contracts it left
// reduce-only (A92; admin.yaml SpotReduceOnly).
type SpotReduceOnlyJSON struct {
	ClosedAt  *string               `json:"closed_at"`
	OpenedAt  *string               `json:"opened_at"`
	Contracts []ReducedContractJSON `json:"contracts"`
}

// ReducedContractJSON is a contract under reduce-only.
type ReducedContractJSON struct {
	Symbol string `json:"symbol"`
	Reason string `json:"reason"`
	Since  string `json:"since"`
}

// ContractLiftJSON is how lifting one contract's reduce-only went.
type ContractLiftJSON struct {
	Symbol string  `json:"symbol"`
	Lifted bool    `json:"lifted"`
	Error  *string `json:"error"`
}

func spotReduceOnlyJSON(c application.SpotClosure) SpotReduceOnlyJSON {
	stamp := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		v := httpx.FormatTime(*t)
		return &v
	}
	out := SpotReduceOnlyJSON{ClosedAt: stamp(c.ClosedAt), OpenedAt: stamp(c.OpenedAt), Contracts: make([]ReducedContractJSON, 0, len(c.Contracts))}
	for _, rc := range c.Contracts {
		out.Contracts = append(out.Contracts, ReducedContractJSON{Symbol: rc.Symbol, Reason: rc.Reason, Since: httpx.FormatTime(rc.Since)})
	}
	return out
}

func (h *Handler) spotReduceOnly(w http.ResponseWriter, r *http.Request) {
	c, err := h.Svc.SpotReduceOnly(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, spotReduceOnlyJSON(c))
}

func (h *Handler) liftSpotReduceOnly(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	lifts, err := h.Svc.LiftSpotReduceOnly(r.Context(), principal(r), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]ContractLiftJSON, 0, len(lifts))
	for _, l := range lifts {
		out = append(out, ContractLiftJSON{Symbol: l.Symbol, Lifted: l.Lifted, Error: optional(l.Error)})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"contracts": out})
}
