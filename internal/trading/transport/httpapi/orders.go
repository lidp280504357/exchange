// Package httpapi serves the order endpoints (api/openapi/trading.yaml).
package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/trading/application"
	"github.com/lidp280504357/exchange/internal/trading/domain"
)

// Handler serves orders; every route needs the identity the gateway
// attaches.
type Handler struct {
	Svc *application.Service
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
		r.Post("/v1/orders", h.place)
		r.Get("/v1/orders", h.list)
		r.Delete("/v1/orders", h.cancelAll)
		r.Get("/v1/orders/{id}", h.get)
		r.Delete("/v1/orders/{id}", h.cancel)
	})
}

type placeBody struct {
	Symbol              string `json:"symbol"`
	Side                string `json:"side"`
	Type                string `json:"type"`
	TimeInForce         string `json:"time_in_force"`
	Price               string `json:"price"`
	Quantity            string `json:"quantity"`
	QuoteAmount         string `json:"quote_amount"`
	ClientOrderID       string `json:"client_order_id"`
	SelfTradePrevention string `json:"self_trade_prevention"`
}

// optional parses an optional decimal field; absent is zero.
func optional(name, s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, apperr.Invalid(name + " must be a decimal string")
	}
	return d, nil
}

func (h *Handler) place(w http.ResponseWriter, r *http.Request) {
	var body placeBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	req := domain.Request{
		UserID: httpx.UserID(r), ClientOrderID: body.ClientOrderID, Symbol: body.Symbol,
		Side: domain.Side(body.Side), Type: domain.Type(body.Type), TimeInForce: domain.TimeInForce(body.TimeInForce),
		STP: domain.STP(body.SelfTradePrevention),
	}
	var err error
	if req.Price, err = optional("price", body.Price); err == nil {
		if req.Quantity, err = optional("quantity", body.Quantity); err == nil {
			req.QuoteAmount, err = optional("quote_amount", body.QuoteAmount)
		}
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
	httpx.WriteJSON(w, http.StatusAccepted, toJSON(o))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.List(r.Context(), httpx.UserID(r), q.Get("symbol"), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]orderJSON, 0, len(list))
	for _, o := range list {
		out = append(out, toJSON(o))
	}
	resp := map[string]any{"items": out, "next_cursor": nil}
	if next != "" {
		resp["next_cursor"] = next
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	o, err := h.Svc.Get(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toJSON(o))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	o, err := h.Svc.Cancel(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, toJSON(o))
}

func (h *Handler) cancelAll(w http.ResponseWriter, r *http.Request) {
	n, err := h.Svc.CancelAll(r.Context(), httpx.UserID(r), r.URL.Query().Get("symbol"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]int{"requested": n})
}

type orderJSON struct {
	OrderID             string `json:"order_id"`
	ClientOrderID       string `json:"client_order_id"`
	Symbol              string `json:"symbol"`
	Side                string `json:"side"`
	Type                string `json:"type"`
	TimeInForce         string `json:"time_in_force"`
	SelfTradePrevention string `json:"self_trade_prevention"`
	Price               string `json:"price,omitempty"`
	Quantity            string `json:"quantity,omitempty"`
	QuoteAmount         string `json:"quote_amount,omitempty"`
	Status              string `json:"status"`
	RejectReason        string `json:"reject_reason,omitempty"`
	FilledQuantity      string `json:"filled_quantity"`
	FilledQuote         string `json:"filled_quote"`
	FrozenAsset         string `json:"frozen_asset"`
	FrozenAmount        string `json:"frozen_amount"`
	CancelRequested     bool   `json:"cancel_requested"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

func toJSON(o domain.Order) orderJSON {
	opt := func(d decimal.Decimal) string {
		if d.IsZero() {
			return ""
		}
		return d.String()
	}
	return orderJSON{
		OrderID: o.ID, ClientOrderID: o.ClientOrderID, Symbol: o.Symbol, Side: string(o.Side), Type: string(o.Type),
		TimeInForce: string(o.TimeInForce), SelfTradePrevention: string(o.STP),
		Price: opt(o.Price), Quantity: opt(o.Quantity), QuoteAmount: opt(o.QuoteAmount),
		Status: string(o.Status), RejectReason: o.RejectReason,
		FilledQuantity: o.FilledQuantity.String(), FilledQuote: o.FilledQuote.String(),
		FrozenAsset: o.FrozenAsset, FrozenAmount: o.FrozenAmount.String(), CancelRequested: o.CancelRequested,
		CreatedAt: httpx.FormatTime(o.CreatedAt), UpdatedAt: httpx.FormatTime(o.UpdatedAt),
	}
}
