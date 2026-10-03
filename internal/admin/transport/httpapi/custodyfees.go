package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// custodyFees pages through the custodians' withdrawal fees (C6).
func (h *Handler) custodyFees(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw, err := h.Svc.CustodyFees(r.Context(), principal(r), ports.FeeQuery{Status: q.Get("status"), Cursor: q.Get("cursor"), Limit: intParam(q, "limit")})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// bookCustodyFee books a held fee: {asset?, amount?, reason}.
func (h *Handler) bookCustodyFee(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Asset  string `json:"asset"`
		Amount string `json:"amount"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	amount := decimal.Zero
	if body.Amount != "" {
		var err error
		if amount, err = decimal.NewFromString(body.Amount); err != nil {
			httpx.WriteError(w, r, apperr.Invalid("the amount is not a number"))
			return
		}
	}
	raw, err := h.Svc.BookCustodyFee(r.Context(), principal(r), ports.FeeBooking{
		WithdrawalID: chi.URLParam(r, "withdrawal"), Asset: body.Asset, Amount: amount, Reason: body.Reason,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// writeOffCustodyFee writes a held fee off: {reason}.
func (h *Handler) writeOffCustodyFee(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.WriteOffCustodyFee(r.Context(), principal(r), chi.URLParam(r, "withdrawal"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}
