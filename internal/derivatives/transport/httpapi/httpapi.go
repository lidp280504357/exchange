// Package httpapi serves the contract trading endpoints
// (api/openapi/derivatives.yaml) under /v1/derivatives.
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/derivatives/application"
	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Handler serves contract trading; every route needs the identity the
// gateway attaches.
type Handler struct {
	Svc *application.Service
	// Asset is the settlement asset of the contracts (USDT).
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
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request) {
	s, err := h.Svc.Account(r.Context(), httpx.UserID(r), h.Asset)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, accountJSON{
		Asset: s.Asset, WalletBalance: s.WalletBalance().String(), Available: s.Available.String(), Frozen: s.Frozen.String(),
		OrderMargin: s.OrderMargin.String(), PositionMargin: s.PositionMargin.String(), UnrealizedPnL: s.UnrealizedPnL.String(),
		CrossUnrealizedPnL: s.CrossUnrealizedPnL.String(), MarginBalance: s.MarginBalance().String(),
		Transferable: domain.Transferable(s.Available, s.CrossUnrealizedPnL).String(),
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
		PositionID: p.ID, Symbol: p.Symbol, PositionSide: string(p.Side), Quantity: p.Qty.String(), EntryPrice: p.EntryPrice().String(),
		MarkPrice: optional(v.Mark), Margin: p.Margin.String(), MarginMode: string(p.MarginMode), Leverage: p.Leverage,
		LiquidationPrice: optional(v.LiquidationPrice), RealizedPnL: p.RealizedPnL.String(), Funding: p.Funding.String(),
		UpdatedAt: stamp(p.UpdatedAt),
	}
	if v.Mark.IsPositive() {
		n, u, m := p.Notional(v.Mark).String(), v.UnrealizedPnL.String(), v.MaintenanceMargin.String()
		out.Notional, out.UnrealizedPnL, out.MaintenanceMargin = &n, &u, &m
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
	httpx.WriteJSON(w, http.StatusOK, toPositionJSON(application.PositionView{Position: p}))
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
	CancelReason   *string `json:"cancel_reason"`
	RejectReason   *string `json:"reject_reason"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toOrderJSON(o domain.Order) orderJSON {
	out := orderJSON{
		OrderID: o.ID, ClientOrderID: o.ClientOrderID, Symbol: o.Symbol, Side: string(o.Side), PositionSide: string(o.PositionSide),
		Type: string(o.Type), TimeInForce: string(o.TimeInForce), Price: o.Price.String(), Quantity: o.Qty.String(),
		ReduceOnly: o.ReduceOnly, Leverage: o.Leverage, MarginMode: string(o.MarginMode), Status: string(o.Status),
		FilledQuantity: o.Filled.String(), Fee: o.Fee.String(), RealizedPnL: o.RealizedPnL.String(), Reserved: o.Unreleased().String(),
		CancelReason: text(o.CancelReason), RejectReason: text(o.RejectReason), CreatedAt: stamp(o.CreatedAt), UpdatedAt: stamp(o.UpdatedAt),
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
	httpx.WriteJSON(w, http.StatusAccepted, toOrderJSON(o))
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
	for _, o := range list {
		out = append(out, toOrderJSON(o))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": text(next)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	o, err := h.Svc.Get(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toOrderJSON(o))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	o, err := h.Svc.Cancel(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, toOrderJSON(o))
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
	for _, f := range list {
		role := "TAKER"
		if f.Maker {
			role = "MAKER"
		}
		out = append(out, fillJSON{
			TradeID: f.TradeID, OrderID: f.OrderID, Symbol: f.Symbol, Side: string(f.Side), PositionSide: string(f.PositionSide),
			Role: role, Price: f.Price.String(), Quantity: f.Qty.String(), ClosedQuantity: f.ClosedQty.String(), Fee: f.Fee.String(),
			RealizedPnL: f.RealizedPnL.String(), Liquidation: f.Liquidation, Settled: f.Settled, ExecutedAt: stamp(f.ExecutedAt),
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
	for _, p := range list {
		out = append(out, fundingJSON{
			Symbol: p.Symbol, FundingTime: p.FundingTime.UTC().Format(time.RFC3339), PositionSide: string(p.Side), Quantity: p.Qty.String(),
			FundingRate: p.Rate.String(), MarkPrice: p.Mark.String(), Amount: p.Amount.String(),
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
