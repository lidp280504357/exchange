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
	out := productsJSON(ps)
	out["canceled_orders"] = ps.CanceledOrders
	httpx.WriteJSON(w, http.StatusOK, out)
}
