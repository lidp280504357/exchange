package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/httpx"
)

// ProductFlags reads the product lines' flags (flags.Client).
type ProductFlags interface {
	Get(key string) (flags.Flag, bool)
}

// The product lines (design 2026-10-07, product switches, K0;
// platform.yaml's /v1/platform/products): open unless an operator closed
// one, from the flags product.spot, product.usdt_m and product.coin_m, one
// not stored counting as open.
func (h *Handler) productRoutes(r chi.Router) {
	r.Get("/v1/platform/products", h.products)
}

// ProductLineJSON is a product line as the sites see it (platform.yaml's
// ProductLine).
type ProductLineJSON struct {
	Enabled  bool    `json:"enabled"`
	ClosedAt *string `json:"closed_at,omitempty"`
}

func (h *Handler) products(w http.ResponseWriter, r *http.Request) {
	out := make(map[string]ProductLineJSON, len(flags.ProductKeys))
	versions := make([]string, 0, len(flags.ProductKeys))
	for _, key := range flags.ProductKeys {
		f, ok := h.Products.Get(key)
		line := ProductLineJSON{Enabled: !ok || f.Enabled}
		if ok && !f.Enabled {
			at := httpx.FormatTime(f.UpdatedAt)
			line.ClosedAt = &at
		}
		out[flags.ProductNames[key]] = line
		versions = append(versions, strconv.FormatInt(f.Version, 10))
	}
	tag := `"` + strings.Join(versions, "-") + `"`
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.Header().Set("ETag", tag)
	if notModified(r, tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
