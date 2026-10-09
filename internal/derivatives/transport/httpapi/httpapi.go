// Package httpapi serves the contract trading endpoints
// (api/openapi/derivatives.yaml) under /v1/derivatives.
package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Handler serves contract trading; every route needs the identity the
// gateway attaches.
type Handler struct {
	Svc *application.Service
	// Asset is the account the account endpoint shows when not asked for
	// another settlement asset (USDT).
	Asset string
}

// Routes mounts the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if httpx.UserID(r) == "" {
					httpx.WriteError(w, r, apperr.Unauthorized("sign in first"))
					return
				}
				next.ServeHTTP(w, r)
			})
		})
		r.Get("/v1/derivatives/account", h.account)
		r.Get("/v1/derivatives/settings/{symbol}", h.settings)
		r.Put("/v1/derivatives/settings/{symbol}", h.updateSettings)
		r.Get("/v1/derivatives/positions", h.positions)
		r.Post("/v1/derivatives/positions/{symbol}/margin", h.margin)
		r.Post("/v1/derivatives/orders", h.place)
		r.Get("/v1/derivatives/orders", h.list)
		r.Delete("/v1/derivatives/orders", h.cancelAll)
		r.Get("/v1/derivatives/orders/{id}", h.get)
		r.Delete("/v1/derivatives/orders/{id}", h.cancel)
		r.Get("/v1/derivatives/fills", h.fills)
		r.Get("/v1/derivatives/funding", h.funding)
		r.Post("/v1/derivatives/conditional-orders", h.createConditional)
		r.Get("/v1/derivatives/conditional-orders", h.conditionals)
		r.Delete("/v1/derivatives/conditional-orders/{id}", h.cancelConditional)
	})
}

func symbol(r *http.Request) string { return strings.ToUpper(chi.URLParam(r, "symbol")) }

func decimalField(name, s string, required bool) (decimal.Decimal, error) {
	if s == "" && !required {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, apperr.Invalid(name + " must be a decimal string")
	}
	return d, nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

type accountJSON struct {
	Asset              string `json:"asset"`
	WalletBalance      string `json:"wallet_balance"`
	Available          string `json:"available"`
	Frozen             string `json:"frozen"`
	OrderMargin        string `json:"order_margin"`
	PositionMargin     string `json:"position_margin"`
	UnrealizedPnL      string `json:"unrealized_pnl"`
	CrossUnrealizedPnL string `json:"cross_unrealized_pnl"`
	MarginBalance      string `json:"margin_balance"`
	Transferable       string `json:"transferable"`
	Liquidating        bool   `json:"liquidating"`
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request) {
	asset := strings.ToUpper(r.URL.Query().Get("asset"))
	if asset == "" {
		asset = h.Asset // USDT, the account from before the coin-margined contracts
	}
	s, err := h.Svc.Account(r.Context(), httpx.UserID(r), asset)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	transferable := domain.Transferable(s.Available, s.CrossUnrealizedPnL)
	if s.Liquidating {
		transferable = decimal.Zero
	}
	httpx.WriteJSON(w, http.StatusOK, accountJSON{
		Asset: s.Asset, WalletBalance: s.WalletBalance().String(), Available: s.Available.String(), Frozen: s.Frozen.String(),
		OrderMargin: s.OrderMargin.String(), PositionMargin: s.PositionMargin.String(), UnrealizedPnL: s.UnrealizedPnL.String(),
		CrossUnrealizedPnL: s.CrossUnrealizedPnL.String(), MarginBalance: s.MarginBalance().String(),
		Transferable: transferable.String(), Liquidating: s.Liquidating,
	})
}

type settingsJSON struct {
	Symbol       string  `json:"symbol"`
	PositionMode string  `json:"position_mode"`
	MarginMode   string  `json:"margin_mode"`
	Leverage     int32   `json:"leverage"`
	UpdatedAt    *string `json:"updated_at"`
}

func toSettingsJSON(s domain.Settings) settingsJSON {
	out := settingsJSON{Symbol: s.Symbol, PositionMode: string(s.PositionMode), MarginMode: string(s.MarginMode), Leverage: s.Leverage}
	if !s.UpdatedAt.IsZero() {
		at := stamp(s.UpdatedAt)
		out.UpdatedAt = &at
	}
	return out
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	s, err := h.Svc.Settings(r.Context(), httpx.UserID(r), symbol(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toSettingsJSON(s))
}

func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PositionMode *string `json:"position_mode"`
		MarginMode   *string `json:"margin_mode"`
		Leverage     *int32  `json:"leverage"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var ch application.SettingsChange
	if body.PositionMode != nil {
		m := domain.PositionMode(*body.PositionMode)
		ch.PositionMode = &m
	}
	if body.MarginMode != nil {
		m := domain.MarginMode(*body.MarginMode)
		ch.MarginMode = &m
	}
	ch.Leverage = body.Leverage
	s, err := h.Svc.UpdateSettings(r.Context(), httpx.UserID(r), symbol(r), ch)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toSettingsJSON(s))
}

type positionJSON struct {
	PositionID        string  `json:"position_id"`
	Symbol            string  `json:"symbol"`
	PositionSide      string  `json:"position_side"`
	Quantity          string  `json:"quantity"`
	EntryPrice        string  `json:"entry_price"`
	MarkPrice         *string `json:"mark_price"`
	Notional          *string `json:"notional"`
	UnrealizedPnL     *string `json:"unrealized_pnl"`
	Margin            string  `json:"margin"`
	MarginMode        string  `json:"margin_mode"`
	Leverage          int32   `json:"leverage"`
	MaintenanceMargin *string `json:"maintenance_margin"`
	LiquidationPrice  *string `json:"liquidation_price"`
	RealizedPnL       string  `json:"realized_pnl"`
	Funding           string  `json:"funding"`
	UpdatedAt         string  `json:"updated_at"`
	// The settlement asset; a coin-margined position's contracts (signed)
	// and value in coin and USD, the USD value of a linear one (G0 §3).
	SettleAsset string  `json:"settle_asset"`
	Contracts   *string `json:"contracts"`
	ValueCoin   *string `json:"value_coin"`
	ValueUSD    *string `json:"value_usd"`
}

func optional(d decimal.Decimal) *string {
	if d.IsZero() {
		return nil
	}
	s := d.String()
	return &s
}

func toPositionJSON(v application.PositionView) positionJSON {
	p := v.Position
	out := positionJSON{
		PositionID: p.ID, Symbol: p.Symbol, PositionSide: string(p.Side), Quantity: p.Qty.String(), EntryPrice: v.Entry.String(),
		MarkPrice: optional(v.Mark), Margin: p.Margin.String(), MarginMode: string(p.MarginMode), Leverage: p.Leverage,
		LiquidationPrice: optional(v.LiquidationPrice), RealizedPnL: p.RealizedPnL.String(), Funding: p.Funding.String(),
		UpdatedAt: stamp(p.UpdatedAt), SettleAsset: v.Settle,
	}
	if out.SettleAsset == "" {
		out.SettleAsset = "USDT"
	}
	inverse := v.ContractSize.IsPositive()
	if inverse {
		contracts, usd := p.Qty.String(), p.Qty.Abs().Mul(v.ContractSize).String()
		out.Contracts, out.ValueUSD = &contracts, &usd
	}
	if v.Mark.IsPositive() {
		n, u, m := v.Value.String(), v.UnrealizedPnL.String(), v.MaintenanceMargin.String()
		out.Notional, out.UnrealizedPnL, out.MaintenanceMargin = &n, &u, &m
		if inverse {
			out.ValueCoin = &n
		} else {
			out.ValueUSD = &n
		}
	}
	return out
}

func (h *Handler) positions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Positions(r.Context(), httpx.UserID(r), strings.ToUpper(r.URL.Query().Get("symbol")))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]positionJSON, 0, len(list))
	for _, v := range list {
		out = append(out, toPositionJSON(v))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"positions": out})
}

func (h *Handler) margin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PositionSide string `json:"position_side"`
		Amount       string `json:"amount"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	amount, err := decimalField("amount", body.Amount, true)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, err := h.Svc.AdjustMargin(r.Context(), httpx.UserID(r), symbol(r), domain.PositionSide(body.PositionSide), amount)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	views, err := h.Svc.Positions(r.Context(), httpx.UserID(r), p.Symbol)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	for _, v := range views {
		if v.ID == p.ID {
			httpx.WriteJSON(w, http.StatusOK, toPositionJSON(v))
			return
		}
	}
	v, err := h.Svc.View(r.Context(), p) // closed meanwhile: as it was
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPositionJSON(v))
}

type orderJSON struct {
	OrderID        string  `json:"order_id"`
	ClientOrderID  string  `json:"client_order_id"`
	Symbol         string  `json:"symbol"`
	Side           string  `json:"side"`
	PositionSide   string  `json:"position_side"`
	Type           string  `json:"type"`
	TimeInForce    string  `json:"time_in_force"`
	Price          string  `json:"price"`
	Quantity       string  `json:"quantity"`
	ReduceOnly     bool    `json:"reduce_only"`
	Leverage       int32   `json:"leverage"`
	MarginMode     string  `json:"margin_mode"`
	Status         string  `json:"status"`
	FilledQuantity string  `json:"filled_quantity"`
	AveragePrice   *string `json:"average_price"`
	Fee            string  `json:"fee"`
	RealizedPnL    string  `json:"realized_pnl"`
	Reserved       string  `json:"reserved"`
	// CancelRequested is set once a cancel is asked for and the engine has
	// not confirmed it yet.
	CancelRequested bool    `json:"cancel_requested"`
	CancelReason    *string `json:"cancel_reason"`
	RejectReason    *string `json:"reject_reason"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	SettleAsset     string  `json:"settle_asset"`
}

// settles reads, once each, the settlement assets of the contracts an
// answer names; USDT, the linear contracts', when one cannot be read.
type settles struct {
	ctx   context.Context
	svc   *application.Service
	cache map[string]string
}

func (h *Handler) settles(ctx context.Context) *settles {
	return &settles{ctx: ctx, svc: h.Svc, cache: map[string]string{}}
}

func (s *settles) of(symbol string) string {
	if a, ok := s.cache[symbol]; ok {
		return a
	}
	a, err := s.svc.SettleAsset(s.ctx, symbol)
	if err != nil || a == "" {
		a = "USDT"
	}
	s.cache[symbol] = a
	return a
}

func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toOrderJSON(o domain.Order, settle string) orderJSON {
	out := orderJSON{
		OrderID: o.ID, ClientOrderID: o.ClientOrderID, Symbol: o.Symbol, Side: string(o.Side), PositionSide: string(o.PositionSide),
		Type: string(o.Type), TimeInForce: string(o.TimeInForce), Price: o.Price.String(), Quantity: o.Qty.String(),
		ReduceOnly: o.ReduceOnly, Leverage: o.Leverage, MarginMode: string(o.MarginMode), Status: string(o.Status),
		FilledQuantity: o.Filled.String(), Fee: o.Fee.String(), RealizedPnL: o.RealizedPnL.String(), Reserved: o.Unreleased().String(),
		CancelRequested: o.CancelRequested, CancelReason: text(o.CancelReason), RejectReason: text(o.RejectReason), CreatedAt: stamp(o.CreatedAt),
		UpdatedAt: stamp(o.UpdatedAt), SettleAsset: settle,
	}
	if o.Filled.IsPositive() {
		avg := o.FilledQuote.DivRound(o.Filled, 8).String()
		out.AveragePrice = &avg
	}
	return out
}

func (h *Handler) place(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		PositionSide  string `json:"position_side"`
		Type          string `json:"type"`
		TimeInForce   string `json:"time_in_force"`
		Price         string `json:"price"`
		Quantity      string `json:"quantity"`
		ReduceOnly    bool   `json:"reduce_only"`
		ClientOrderID string `json:"client_order_id"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	req := domain.Request{
		UserID: httpx.UserID(r), ClientOrderID: body.ClientOrderID, Symbol: strings.ToUpper(body.Symbol), Side: domain.Side(body.Side),
		PositionSide: domain.PositionSide(body.PositionSide), Type: domain.Type(body.Type), TimeInForce: domain.TimeInForce(body.TimeInForce),
		ReduceOnly: body.ReduceOnly,
	}
	var err error
	if req.Price, err = decimalField("price", body.Price, false); err == nil {
		req.Qty, err = decimalField("quantity", body.Quantity, true)
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	o, err := h.Svc.Place(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, toOrderJSON(o, h.settles(r.Context()).of(o.Symbol)))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.List(r.Context(), httpx.UserID(r), strings.ToUpper(q.Get("symbol")), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]orderJSON, 0, len(list))
	settle := h.settles(r.Context())
	for _, o := range list {
		out = append(out, toOrderJSON(o, settle.of(o.Symbol)))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": text(next)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	o, err := h.Svc.Get(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toOrderJSON(o, h.settles(r.Context()).of(o.Symbol)))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	o, err := h.Svc.Cancel(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, toOrderJSON(o, h.settles(r.Context()).of(o.Symbol)))
}

func (h *Handler) cancelAll(w http.ResponseWriter, r *http.Request) {
	n, err := h.Svc.CancelAll(r.Context(), httpx.UserID(r), strings.ToUpper(r.URL.Query().Get("symbol")))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"requested": n})
}

type fillJSON struct {
	TradeID        string `json:"trade_id"`
	OrderID        string `json:"order_id"`
	Symbol         string `json:"symbol"`
	Side           string `json:"side"`
	PositionSide   string `json:"position_side"`
	Role           string `json:"role"`
	Price          string `json:"price"`
	Quantity       string `json:"quantity"`
	ClosedQuantity string `json:"closed_quantity"`
	Fee            string `json:"fee"`
	RealizedPnL    string `json:"realized_pnl"`
	Liquidation    bool   `json:"liquidation"`
	Settled        bool   `json:"settled"`
	ExecutedAt     string `json:"executed_at"`
	SettleAsset    string `json:"settle_asset"`
}

func (h *Handler) fills(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.Fills(r.Context(), httpx.UserID(r), strings.ToUpper(q.Get("symbol")), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]fillJSON, 0, len(list))
	settle := h.settles(r.Context())
	for _, f := range list {
		role := "TAKER"
		if f.Maker {
			role = "MAKER"
		}
		out = append(out, fillJSON{
			TradeID: f.TradeID, OrderID: f.OrderID, Symbol: f.Symbol, Side: string(f.Side), PositionSide: string(f.PositionSide),
			Role: role, Price: f.Price.String(), Quantity: f.Qty.String(), ClosedQuantity: f.ClosedQty.String(), Fee: f.Fee.String(),
			RealizedPnL: f.RealizedPnL.String(), Liquidation: f.Liquidation, Settled: f.Settled, ExecutedAt: stamp(f.ExecutedAt),
			SettleAsset: settle.of(f.Symbol),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": text(next)})
}

type fundingJSON struct {
	Symbol       string `json:"symbol"`
	FundingTime  string `json:"funding_time"`
	PositionSide string `json:"position_side"`
	Quantity     string `json:"quantity"`
	FundingRate  string `json:"funding_rate"`
	MarkPrice    string `json:"mark_price"`
	Amount       string `json:"amount"`
	SettleAsset  string `json:"settle_asset"`
}

func (h *Handler) funding(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.FundingPayments(r.Context(), httpx.UserID(r), strings.ToUpper(q.Get("symbol")), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]fundingJSON, 0, len(list))
	settle := h.settles(r.Context())
	for _, p := range list {
		out = append(out, fundingJSON{
			Symbol: p.Symbol, FundingTime: p.FundingTime.UTC().Format(time.RFC3339), PositionSide: string(p.Side), Quantity: p.Qty.String(),
			FundingRate: p.Rate.String(), MarkPrice: p.Mark.String(), Amount: p.Amount.String(), SettleAsset: settle.of(p.Symbol),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": text(next)})
}

type conditionalJSON struct {
	ConditionalID string  `json:"conditional_id"`
	Symbol        string  `json:"symbol"`
	PositionSide  string  `json:"position_side"`
	Side          string  `json:"side"`
	Kind          string  `json:"kind"`
	TriggerPrice  string  `json:"trigger_price"`
	TriggerBy     string  `json:"trigger_by"`
	OrderType     string  `json:"order_type"`
	Price         *string `json:"price"`
	Quantity      *string `json:"quantity"`
	Status        string  `json:"status"`
	Reason        *string `json:"reason"`
	OrderID       *string `json:"order_id"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func toConditionalJSON(c domain.Conditional) conditionalJSON {
	return conditionalJSON{
		ConditionalID: c.ID, Symbol: c.Symbol, PositionSide: string(c.PositionSide), Side: string(c.Side), Kind: c.Kind,
		TriggerPrice: c.TriggerPrice.String(), TriggerBy: c.TriggerBy, OrderType: string(c.OrderType), Price: optional(c.Price),
		Quantity: optional(c.Qty), Status: c.Status, Reason: text(c.Reason), OrderID: text(c.OrderID),
		CreatedAt: stamp(c.CreatedAt), UpdatedAt: stamp(c.UpdatedAt),
	}
}

func (h *Handler) createConditional(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol       string `json:"symbol"`
		PositionSide string `json:"position_side"`
		Kind         string `json:"kind"`
		TriggerPrice string `json:"trigger_price"`
		TriggerBy    string `json:"trigger_by"`
		OrderType    string `json:"order_type"`
		Price        string `json:"price"`
		Quantity     string `json:"quantity"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	req := domain.ConditionalRequest{
		UserID: httpx.UserID(r), Symbol: strings.ToUpper(body.Symbol), PositionSide: domain.PositionSide(body.PositionSide),
		Kind: body.Kind, TriggerBy: body.TriggerBy, OrderType: domain.Type(body.OrderType),
	}
	var err error
	if req.TriggerPrice, err = decimalField("trigger_price", body.TriggerPrice, true); err == nil {
		if req.Price, err = decimalField("price", body.Price, false); err == nil {
			req.Qty, err = decimalField("quantity", body.Quantity, false)
		}
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.Svc.CreateConditional(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toConditionalJSON(c))
}

func (h *Handler) conditionals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.Conditionals(r.Context(), httpx.UserID(r), strings.ToUpper(q.Get("symbol")), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]conditionalJSON, 0, len(list))
	for _, c := range list {
		out = append(out, toConditionalJSON(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": text(next)})
}

func (h *Handler) cancelConditional(w http.ResponseWriter, r *http.Request) {
	c, err := h.Svc.CancelConditional(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toConditionalJSON(c))
}

// InternalRoutes serves the admin console (the gateway does not route
// /internal): the contracts with their reduce-only state, mark price and
// open interest; lifting reduce-only; the positions close to or in
// liquidation; every user's open positions; closing a user's position at
// the market; a contract product line's counts and, as the console closes
// it, its open orders canceled (design 2026-10-07, product switches). A
// request carrying a caller's X-User-Id came through the gateway and is
// not served (404, as spot-trading-service's; api/internal/products.yaml).
func (h *Handler) InternalRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(internalOnly)
		r.Get("/internal/products/{product}", h.product)
		r.Post("/internal/products/{product}/cancel-open", h.cancelProduct)
		r.Get("/internal/derivatives/contracts", h.overview)
		r.Post("/internal/derivatives/contracts/{symbol}/lift-reduce-only", h.liftReduceOnly)
		r.Get("/internal/derivatives/risk", h.risk)
		r.Get("/internal/derivatives/positions", h.openPositions)
		r.Post("/internal/derivatives/positions/close", h.adminClose)
		r.Get("/internal/derivatives/users/{id}/cross-margin", h.crossMargin)
		r.Post("/internal/derivatives/contracts/{symbol}/tier-impact", h.tierImpact)
		r.Post("/internal/derivatives/contracts/{symbol}/price-impact", h.priceImpact)
	})
}

// internalOnly refuses a request that carries a caller's identity: only
// what comes over the compose network is served (review C63 ①).
func internalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httpx.UserID(r) != "" {
			httpx.WriteError(w, r, apperr.NotFound("no such endpoint"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// product counts what closing a contract product line (usdt_m, coin_m)
// touches now: the open orders it would cancel (take-profits and
// stop-losses included) and the open positions that stay, HOUSE's and the
// market makers' not counted.
func (h *Handler) product(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "product")
	key, err := application.ProductKey(name)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	orders, positions, err := h.Svc.ProductCounts(r.Context(), key)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"product": name, "closed": h.Svc.ProductClosed(key), "open_orders": orders, "open_positions": positions,
	})
}

// cancelProduct cancels a closed contract product line's open orders as
// the console closes it ({actor, reason}): 202 {canceled, orders:
// [{order_id, user_id, symbol, type: ORDER or CONDITIONAL}]}, the engine
// confirming the cancels; 409 COMMON_CONFLICT while the line is open.
func (h *Handler) cancelProduct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	key, err := application.ProductKey(chi.URLParam(r, "product"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	list, err := h.Svc.CancelProduct(r.Context(), key, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type orderJSON struct {
		OrderID string `json:"order_id"`
		UserID  string `json:"user_id"`
		Symbol  string `json:"symbol"`
		Type    string `json:"type"`
	}
	out := make([]orderJSON, 0, len(list))
	for _, o := range list {
		kind := "ORDER"
		if o.Conditional {
			kind = "CONDITIONAL"
		}
		out = append(out, orderJSON{OrderID: o.ID, UserID: o.UserID, Symbol: o.Symbol, Type: kind})
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"canceled": len(out), "orders": out})
}

func (h *Handler) adminClose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID        string `json:"user_id"`
		Symbol        string `json:"symbol"`
		PositionSide  string `json:"position_side"`
		ClientOrderID string `json:"client_order_id"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	o, err := h.Svc.AdminClose(r.Context(), body.UserID, strings.ToUpper(strings.TrimSpace(body.Symbol)),
		domain.PositionSide(strings.ToUpper(body.PositionSide)), body.ClientOrderID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toOrderJSON(o, h.settles(r.Context()).of(o.Symbol)))
}

// marginStates names the margin states for the console.
var marginStates = map[domain.MarginState]string{
	domain.MarginHealthy: "HEALTHY", domain.MarginWarning: "WARNING", domain.MarginLiquidate: "LIQUIDATE",
}

// crossMargin measures a user's cross margin account and a debit of it
// (?debit=, default 0) for the admin console's adjustment preview.
func (h *Handler) crossMargin(w http.ResponseWriter, r *http.Request) {
	debit := decimal.Zero
	if v := r.URL.Query().Get("debit"); v != "" {
		var err error
		if debit, err = decimal.NewFromString(v); err != nil {
			httpx.WriteError(w, r, apperr.Invalid("debit must be a decimal"))
			return
		}
	}
	asset := strings.ToUpper(r.URL.Query().Get("asset"))
	if asset == "" {
		asset = h.Asset
	}
	m, err := h.Svc.CrossMargin(r.Context(), chi.URLParam(r, "id"), asset, debit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"asset": m.Asset, "positions": m.Positions, "unmeasured": m.Unmeasured, "equity": m.Equity.String(),
		"maintenance": m.Maintenance.String(), "state": marginStates[m.State], "equity_after": m.EquityAfter.String(),
		"state_after": marginStates[m.StateAfter],
	})
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Overview(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type contractJSON struct {
		Symbol           string  `json:"symbol"`
		Status           string  `json:"status"`
		ReduceOnly       bool    `json:"reduce_only"`
		ReduceOnlyReason string  `json:"reduce_only_reason"`
		ReduceOnlySince  *string `json:"reduce_only_since"`
		LiftedBy         string  `json:"lifted_by"`
		MarkPrice        *string `json:"mark_price"`
		MarkAt           *string `json:"mark_at"`
		MarkFresh        bool    `json:"mark_fresh"`
		OpenInterest     string  `json:"open_interest"`
		Positions        int     `json:"positions"`
	}
	out := make([]contractJSON, 0, len(list))
	for _, v := range list {
		c := contractJSON{
			Symbol: v.Contract.Symbol, Status: v.Contract.Status, ReduceOnly: v.State.ReduceOnly, ReduceOnlyReason: v.State.Reason,
			LiftedBy: v.State.LiftedBy, MarkFresh: v.MarkFresh, OpenInterest: v.OpenInterest.String(), Positions: v.Positions,
		}
		if !v.State.Since.IsZero() {
			at := stamp(v.State.Since)
			c.ReduceOnlySince = &at
		}
		if v.Mark.Price.IsPositive() {
			price, at := v.Mark.Price.String(), stamp(v.Mark.At)
			c.MarkPrice, c.MarkAt = &price, &at
		}
		out = append(out, c)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"contracts": out})
}

func (h *Handler) liftReduceOnly(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Actor string `json:"actor"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if strings.TrimSpace(body.Actor) == "" {
		httpx.WriteError(w, r, apperr.Invalid("actor is required"))
		return
	}
	lifted, err := h.Svc.LiftReduceOnly(r.Context(), symbol(r), body.Actor)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"symbol": symbol(r), "lifted": lifted})
}

// risk lists the positions under watch, of only user_ids or of all but
// exclude_user_ids (review L3).
func (h *Handler) risk(w http.ResponseWriter, r *http.Request) {
	only, except, err := httpx.UserIDsFrom(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	list, err := h.Svc.RiskPositions(r.Context(), ports.UserFilter{Only: only, Except: except})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"positions": riskRows(list)})
}

// openPositions lists every user's open positions for the admin console,
// riskiest first: symbol, user_id, user_ids or exclude_user_ids (review
// L3), watch=true (only those under watch), limit (default 200, at most
// 1000).
func (h *Handler) openPositions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	only, except, err := httpx.UserIDsFrom(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	switch {
	case limit <= 0:
		limit = 200
	case limit > 1000:
		limit = 1000 // at most, not back to the default (C5.5 ⑨)
	}
	list, cut, err := h.Svc.OpenPositions(r.Context(), application.PositionFilter{
		Symbol: strings.ToUpper(q.Get("symbol")), UserID: q.Get("user_id"), Users: ports.UserFilter{Only: only, Except: except},
		Watch: q.Get("watch") == "true", Limit: limit,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"positions": riskRows(list), "truncated": cut})
}

// tierImpact measures a new risk ladder against the contract's open
// positions (the admin console's preview of a ladder change).
func (h *Handler) tierImpact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RiskTiers []struct {
			MaxNotional string `json:"max_notional"`
			MaxLeverage int32  `json:"max_leverage"`
			MMR         string `json:"mmr"`
		} `json:"risk_tiers"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	tiers := make([]domain.RiskTier, 0, len(body.RiskTiers))
	for _, t := range body.RiskTiers {
		notional, err1 := decimal.NewFromString(t.MaxNotional)
		mmr, err2 := decimal.NewFromString(t.MMR)
		if err1 != nil || err2 != nil {
			httpx.WriteError(w, r, apperr.Invalid("max_notional and mmr must be decimal strings"))
			return
		}
		tiers = append(tiers, domain.RiskTier{MaxNotional: notional, MaxLeverage: t.MaxLeverage, MMR: mmr})
	}
	imp, err := h.Svc.TierImpact(r.Context(), strings.ToUpper(chi.URLParam(r, "symbol")), tiers)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type exampleJSON struct {
		UserID            string `json:"user_id"`
		Symbol            string `json:"symbol"`
		PositionSide      string `json:"position_side"`
		Cross             bool   `json:"cross"`
		Notional          string `json:"notional"`
		MarginBalance     string `json:"margin_balance"`
		MaintenanceBefore string `json:"maintenance_before"`
		MaintenanceAfter  string `json:"maintenance_after"`
	}
	examples := make([]exampleJSON, 0, len(imp.Examples))
	for _, e := range imp.Examples {
		examples = append(examples, exampleJSON{
			UserID: e.UserID, Symbol: e.Symbol, PositionSide: string(e.PositionSide), Cross: e.Cross, Notional: e.Notional.StringFixed(2),
			MarginBalance: e.MarginBalance.StringFixed(2), MaintenanceBefore: e.MaintenanceBefore.StringFixed(2),
			MaintenanceAfter: e.MaintenanceAfter.StringFixed(2),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"symbol": imp.Symbol, "positions": imp.Positions, "liquidated": imp.Liquidated, "notional": imp.Notional.StringFixed(2),
		"accounts": imp.Accounts, "warned": imp.Warned, "over_limit": imp.OverLimit, "unmeasured": imp.Unmeasured, "examples": examples,
	})
}

type riskJSON struct {
	positionJSON
	UserID              string  `json:"user_id"`
	Liquidating         bool    `json:"liquidating"`
	LiquidationAttempts int     `json:"liquidation_attempts"`
	WarnedAt            *string `json:"warned_at"`
	MarginRatio         *string `json:"margin_ratio"`
	// MarkFresh is false while the mark price is stale: the figures stand
	// still until it moves again.
	MarkFresh bool `json:"mark_fresh"`
}

// riskRows renders positions across users with their liquidation state
// and margin ratio.
func riskRows(list []application.PositionView) []riskJSON {
	out := make([]riskJSON, 0, len(list))
	for _, v := range list {
		row := riskJSON{
			positionJSON: toPositionJSON(v), UserID: v.UserID, Liquidating: v.Liquidating, LiquidationAttempts: v.LiquidationAttempts,
			MarkFresh: v.MarkFresh,
		}
		if !v.WarnedAt.IsZero() {
			at := stamp(v.WarnedAt)
			row.WarnedAt = &at
		}
		if balance := v.Margin.Add(v.UnrealizedPnL); balance.IsPositive() && v.MaintenanceMargin.IsPositive() {
			ratio := v.MaintenanceMargin.DivRound(balance, 4).String()
			row.MarginRatio = &ratio
		}
		out = append(out, row)
	}
	return out
}

// priceImpact answers what a mark price would do to a contract's open
// positions (the admin console's confirmation of a simulated-market price
// event): {"target_price"} → the positions it would liquidate, their
// notional at the target, their accounts, what the insurance fund would
// bear and the positions it could not measure.
func (h *Handler) priceImpact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetPrice string `json:"target_price"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	target, err := decimal.NewFromString(body.TargetPrice)
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("target_price must be a decimal string"))
		return
	}
	imp, err := h.Svc.PriceImpact(r.Context(), strings.ToUpper(chi.URLParam(r, "symbol")), target)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type exampleJSON struct {
		UserID            string `json:"user_id"`
		Symbol            string `json:"symbol"`
		PositionSide      string `json:"position_side"`
		Cross             bool   `json:"cross"`
		Notional          string `json:"notional"`
		MarginBalance     string `json:"margin_balance"`
		MaintenanceBefore string `json:"maintenance_before"`
		MaintenanceAfter  string `json:"maintenance_after"`
	}
	examples := make([]exampleJSON, 0, len(imp.Examples))
	for _, e := range imp.Examples {
		examples = append(examples, exampleJSON{
			UserID: e.UserID, Symbol: e.Symbol, PositionSide: string(e.PositionSide), Cross: e.Cross, Notional: e.Notional.StringFixed(2),
			MarginBalance: e.MarginBalance.StringFixed(2), MaintenanceBefore: e.MaintenanceBefore.StringFixed(2),
			MaintenanceAfter: e.MaintenanceAfter.StringFixed(2),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"symbol": imp.Symbol, "target_price": imp.Target.String(), "positions": imp.Positions, "liquidated": imp.Liquidated,
		"notional": imp.Notional.StringFixed(2), "accounts": imp.Accounts, "insurance_cost": imp.InsuranceCost.StringFixed(2),
		"unmeasured": imp.Unmeasured, "examples": examples,
	})
}
