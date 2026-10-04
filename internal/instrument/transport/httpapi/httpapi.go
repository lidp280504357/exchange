// Package httpapi serves the public market reference endpoints
// (api/openapi/market.yaml).
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/instrument/application"
	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Handler serves assets, trading pairs and perpetual contracts; no sign-in
// needed.
type Handler struct {
	Svc *application.Service
}

// Routes mounts the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/v1/market/assets", h.assets)
	r.Get("/v1/market/assets/{code}/logo", h.logo)
	r.Get("/v1/market/pairs", h.pairs)
	r.Get("/v1/market/pairs/{symbol}", h.pair)
	r.Get("/v1/market/contracts", h.contracts)
	r.Get("/v1/market/contracts/{symbol}", h.contract)
}

type networkJSON struct {
	Network            string  `json:"network"`
	DisplayName        string  `json:"display_name"`
	Chain              string  `json:"chain"`
	AddressFormat      string  `json:"address_format"`
	ContractAddress    string  `json:"contract_address"`
	Confirmations      int32   `json:"confirmations"`
	ETAMinutes         int32   `json:"eta_minutes"`
	MinDeposit         string  `json:"min_deposit"`
	MinWithdraw        string  `json:"min_withdraw"`
	WithdrawFee        string  `json:"withdraw_fee"`
	MemoRequired       bool    `json:"memo_required"`
	DepositEnabled     bool    `json:"deposit_enabled"`
	WithdrawEnabled    bool    `json:"withdraw_enabled"`
	ExplorerTxURL      *string `json:"explorer_tx_url"`
	ExplorerAddressURL *string `json:"explorer_address_url"`
}

type assetJSON struct {
	AssetCode       string        `json:"asset_code"`
	Name            string        `json:"name"`
	Decimals        int32         `json:"decimals"`
	Rank            *int32        `json:"rank"`
	Categories      []string      `json:"categories"`
	DepositEnabled  bool          `json:"deposit_enabled"`
	WithdrawEnabled bool          `json:"withdraw_enabled"`
	TradingEnabled  bool          `json:"trading_enabled"`
	Networks        []networkJSON `json:"networks"`
	// The profile operators maintain (ASTRA design §5.3).
	DisplayName    *string           `json:"display_name"`
	Description    map[string]string `json:"description"`
	Links          map[string]string `json:"links"`
	LogoURL        *string           `json:"logo_url"`
	ProfileVersion int64             `json:"profile_version"`
}

type pairJSON struct {
	Symbol              string   `json:"symbol"`
	BaseAsset           string   `json:"base_asset"`
	QuoteAsset          string   `json:"quote_asset"`
	BaseName            string   `json:"base_name"`
	Rank                *int32   `json:"rank"`
	Categories          []string `json:"categories"`
	TickSize            string   `json:"tick_size"`
	LotSize             string   `json:"lot_size"`
	PriceDecimals       int32    `json:"price_decimals"`
	QtyDecimals         int32    `json:"qty_decimals"`
	MinQuantity         string   `json:"min_quantity"`
	MaxQuantity         string   `json:"max_quantity"`
	MinNotional         string   `json:"min_notional"`
	PriceBand           string   `json:"price_band"`
	MakerFeeRate        string   `json:"maker_fee_rate"`
	TakerFeeRate        string   `json:"taker_fee_rate"`
	Status              string   `json:"status"`
	ReferenceSymbol     *string  `json:"reference_symbol"`
	ReferenceMultiplier string   `json:"reference_multiplier"`
	ListedAt            string   `json:"listed_at"`
	BaseDisplayName     *string  `json:"base_display_name"`
	BaseLogoURL         *string  `json:"base_logo_url"`
}

// rank is null for an unranked asset.
func rank(r int32) *int32 {
	if r <= 0 {
		return nil
	}
	return &r
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func categories(c []string) []string {
	if c == nil {
		return []string{}
	}
	return c
}

// places is the number of decimal places of a step such as 0.01.
func places(d decimal.Decimal) int32 {
	_, frac, _ := strings.Cut(d.String(), ".")
	return int32(len(frac)) //nolint:gosec // a decimal string is short
}

func toPairJSON(p application.PairView) pairJSON {
	return pairJSON{
		Symbol: p.Symbol, BaseAsset: p.BaseAsset, QuoteAsset: p.QuoteAsset, BaseName: p.BaseName, Rank: rank(p.Rank),
		Categories: categories(p.Categories), TickSize: p.TickSize.String(), LotSize: p.LotSize.String(),
		PriceDecimals: places(p.TickSize), QtyDecimals: places(p.LotSize), MinQuantity: p.MinQuantity.String(),
		MaxQuantity: p.MaxQuantity.String(), MinNotional: p.MinNotional.String(), PriceBand: p.PriceBand.String(),
		MakerFeeRate: p.MakerFeeRate.String(), TakerFeeRate: p.TakerFeeRate.String(), Status: p.Status,
		ReferenceSymbol: optional(p.ReferenceSymbol), ReferenceMultiplier: p.ReferenceMultiplier.String(),
		ListedAt: p.ListedAt.UTC().Format(time.RFC3339), BaseDisplayName: optional(p.BaseProfile.DisplayName),
		BaseLogoURL: optional(application.LogoURL(p.BaseProfile)),
	}
}

func texts(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// displayName falls back to the network code.
func displayName(n domain.Network) string {
	if n.DisplayName != "" {
		return n.DisplayName
	}
	return n.Network
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
		if a.Hidden {
			continue // a test asset (ADR-0017): in no public list
		}
		aj := assetJSON{
			AssetCode: a.Code, Name: a.Name, Decimals: a.Decimals, Rank: rank(a.Rank), Categories: categories(a.Categories),
			DepositEnabled: a.DepositEnabled, WithdrawEnabled: a.WithdrawEnabled, TradingEnabled: a.TradingEnabled,
			Networks: []networkJSON{}, DisplayName: optional(a.Profile.DisplayName), Description: texts(a.Profile.Description),
			Links: texts(a.Profile.Links), LogoURL: optional(application.LogoURL(a.Profile)), ProfileVersion: a.Profile.Version,
		}
		for _, n := range a.Networks {
			aj.Networks = append(aj.Networks, networkJSON{
				Network: n.Network, DisplayName: displayName(n), Chain: n.Chain, AddressFormat: n.AddressFormat,
				ContractAddress: n.ContractAddress, Confirmations: n.Confirmations, ETAMinutes: n.ETAMinutes,
				MinDeposit: n.MinDeposit.String(), MinWithdraw: n.MinWithdraw.String(), WithdrawFee: n.WithdrawFee.String(),
				MemoRequired: n.MemoRequired, DepositEnabled: n.DepositEnabled, WithdrawEnabled: n.WithdrawEnabled,
				ExplorerTxURL: optional(n.ExplorerTxURL), ExplorerAddressURL: optional(n.ExplorerAddressURL),
			})
		}
		out = append(out, aj)
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assets": out})
}

// logo serves an asset's logo. Its URL carries the profile version
// (application.LogoURL): at the current version it is cached for good, an
// older one briefly (it gets the current logo). An SVG opened on its own
// runs nothing: no scripts or embeds, and nosniff keeps the type.
func (h *Handler) logo(w http.ResponseWriter, r *http.Request) {
	l, version, err := h.Svc.Logo(r.Context(), strings.ToUpper(chi.URLParam(r, "code")))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", l.MIME)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if r.URL.Query().Get("v") == strconv.FormatInt(version, 10) {
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hdr.Set("Cache-Control", "public, max-age=60")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(l.Data)
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

type riskTierJSON struct {
	MaxNotional string `json:"max_notional"`
	MaxLeverage int32  `json:"max_leverage"`
	MMR         string `json:"mmr"`
}

type contractJSON struct {
	Symbol               string         `json:"symbol"`
	Type                 string         `json:"type"`
	BaseAsset            string         `json:"base_asset"`
	QuoteAsset           string         `json:"quote_asset"`
	IndexSymbol          string         `json:"index_symbol"`
	TickSize             string         `json:"tick_size"`
	LotSize              string         `json:"lot_size"`
	MinQuantity          string         `json:"min_quantity"`
	MaxQuantity          string         `json:"max_quantity"`
	MinNotional          string         `json:"min_notional"`
	PriceBand            string         `json:"price_band"`
	MaxLeverage          int32          `json:"max_leverage"`
	RiskTiers            []riskTierJSON `json:"risk_tiers"`
	FundingIntervalHours int32          `json:"funding_interval_hours"`
	InterestRate         string         `json:"interest_rate"`
	FundingCap           string         `json:"funding_cap"`
	ImpactNotional       string         `json:"impact_notional"`
	MakerFeeRate         string         `json:"maker_fee_rate"`
	TakerFeeRate         string         `json:"taker_fee_rate"`
	Status               string         `json:"status"`
}

func toContractJSON(c application.ContractView) contractJSON {
	tiers := make([]riskTierJSON, 0, len(c.RiskTiers))
	for _, t := range c.RiskTiers {
		tiers = append(tiers, riskTierJSON{MaxNotional: t.MaxNotional.String(), MaxLeverage: t.MaxLeverage, MMR: t.MMR.String()})
	}
	return contractJSON{
		Symbol: c.Symbol, Type: c.Type, BaseAsset: c.BaseAsset, QuoteAsset: c.QuoteAsset, IndexSymbol: c.IndexSymbol,
		TickSize: c.TickSize.String(), LotSize: c.LotSize.String(), MinQuantity: c.MinQuantity.String(),
		MaxQuantity: c.MaxQuantity.String(), MinNotional: c.MinNotional.String(), PriceBand: c.PriceBand.String(),
		MaxLeverage: c.MaxLeverage(), RiskTiers: tiers, FundingIntervalHours: c.FundingIntervalHours,
		InterestRate: c.InterestRate.String(), FundingCap: c.FundingCap.String(), ImpactNotional: c.ImpactNotional.String(),
		MakerFeeRate: c.MakerFeeRate.String(), TakerFeeRate: c.TakerFeeRate.String(), Status: c.Status,
	}
}

// contracts lists every contract but the delisted ones.
func (h *Handler) contracts(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Contracts(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]contractJSON, 0, len(list))
	for _, c := range list {
		if c.Status != domain.StatusDelisted {
			out = append(out, toContractJSON(c))
		}
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"contracts": out})
}

func (h *Handler) contract(w http.ResponseWriter, r *http.Request) {
	c, err := h.Svc.Contract(r.Context(), strings.ToUpper(chi.URLParam(r, "symbol")))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, toContractJSON(c))
}
