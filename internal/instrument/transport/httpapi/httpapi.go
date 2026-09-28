// Package httpapi serves the public market reference endpoints
// (api/openapi/market.yaml).
package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/instrument/application"
	"github.com/lidp280504357/exchange/internal/instrument/domain"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Handler serves assets and trading pairs; no sign-in needed.
type Handler struct {
	Svc *application.Service
}

// Routes mounts the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/v1/market/assets", h.assets)
	r.Get("/v1/market/pairs", h.pairs)
	r.Get("/v1/market/pairs/{symbol}", h.pair)
}

type networkJSON struct {
	Network         string `json:"network"`
	Chain           string `json:"chain"`
	ContractAddress string `json:"contract_address"`
	Confirmations   int32  `json:"confirmations"`
	MinDeposit      string `json:"min_deposit"`
	MinWithdraw     string `json:"min_withdraw"`
	WithdrawFee     string `json:"withdraw_fee"`
	MemoRequired    bool   `json:"memo_required"`
	DepositEnabled  bool   `json:"deposit_enabled"`
	WithdrawEnabled bool   `json:"withdraw_enabled"`
}

type assetJSON struct {
	AssetCode       string        `json:"asset_code"`
	Name            string        `json:"name"`
	Decimals        int32         `json:"decimals"`
	DepositEnabled  bool          `json:"deposit_enabled"`
	WithdrawEnabled bool          `json:"withdraw_enabled"`
	TradingEnabled  bool          `json:"trading_enabled"`
	Networks        []networkJSON `json:"networks"`
}

type pairJSON struct {
	Symbol       string `json:"symbol"`
	BaseAsset    string `json:"base_asset"`
	QuoteAsset   string `json:"quote_asset"`
	TickSize     string `json:"tick_size"`
	LotSize      string `json:"lot_size"`
	MinQuantity  string `json:"min_quantity"`
	MaxQuantity  string `json:"max_quantity"`
	MinNotional  string `json:"min_notional"`
	PriceBand    string `json:"price_band"`
	MakerFeeRate string `json:"maker_fee_rate"`
	TakerFeeRate string `json:"taker_fee_rate"`
	Status       string `json:"status"`
}

func toPairJSON(p application.PairView) pairJSON {
	return pairJSON{
		Symbol: p.Symbol, BaseAsset: p.BaseAsset, QuoteAsset: p.QuoteAsset, TickSize: p.TickSize.String(),
		LotSize: p.LotSize.String(), MinQuantity: p.MinQuantity.String(), MaxQuantity: p.MaxQuantity.String(),
		MinNotional: p.MinNotional.String(), PriceBand: p.PriceBand.String(), MakerFeeRate: p.MakerFeeRate.String(),
		TakerFeeRate: p.TakerFeeRate.String(), Status: p.Status,
	}
}

// cacheable lets browsers and Cloudflare keep reference data briefly.
func cacheable(w http.ResponseWriter) { w.Header().Set("Cache-Control", "public, max-age=10") }

func (h *Handler) assets(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Assets(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]assetJSON, 0, len(list))
	for _, a := range list {
		aj := assetJSON{
			AssetCode: a.Code, Name: a.Name, Decimals: a.Decimals, DepositEnabled: a.DepositEnabled,
			WithdrawEnabled: a.WithdrawEnabled, TradingEnabled: a.TradingEnabled, Networks: []networkJSON{},
		}
		for _, n := range a.Networks {
			aj.Networks = append(aj.Networks, networkJSON{
				Network: n.Network, Chain: n.Chain, ContractAddress: n.ContractAddress, Confirmations: n.Confirmations,
				MinDeposit: n.MinDeposit.String(), MinWithdraw: n.MinWithdraw.String(), WithdrawFee: n.WithdrawFee.String(),
				MemoRequired: n.MemoRequired, DepositEnabled: n.DepositEnabled, WithdrawEnabled: n.WithdrawEnabled,
			})
		}
		out = append(out, aj)
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assets": out})
}

// pairs lists every pair but the delisted ones.
func (h *Handler) pairs(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Pairs(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]pairJSON, 0, len(list))
	for _, p := range list {
		if p.Status != domain.StatusDelisted {
			out = append(out, toPairJSON(p))
		}
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"pairs": out})
}

func (h *Handler) pair(w http.ResponseWriter, r *http.Request) {
	p, err := h.Svc.Pair(r.Context(), strings.ToUpper(chi.URLParam(r, "symbol")))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, toPairJSON(p))
}
