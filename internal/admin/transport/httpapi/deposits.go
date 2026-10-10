package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

func (h *Handler) withdrawalDetail(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.WithdrawalDetail(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) holdWithdrawal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hold bool   `json:"hold"`
		Note string `json:"note"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.HoldWithdrawal(r.Context(), principal(r), chi.URLParam(r, "id"), body.Hold, body.Note)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// withdrawalSuspensions lists the assets whose withdrawals are suspended.
func (h *Handler) withdrawalSuspensions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.WithdrawalSuspensions(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

// resumeWithdrawals lifts an asset's withdrawal suspension (ADMIN).
func (h *Handler) resumeWithdrawals(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	x, err := h.Svc.ResumeWithdrawals(r.Context(), principal(r), chi.URLParam(r, "asset"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, x)
}

// depositReviews pages through wallet-service's deposits: attention=true
// for those waiting for a decision, manual_pending=true for the
// backfilled ones without a callback yet.
func (h *Handler) depositReviews(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw, err := h.Svc.DepositsForReview(r.Context(), principal(r), ports.DepositReviewQuery{
		UserID: q.Get("user_id"), Status: q.Get("status"), Network: q.Get("network"), Attention: q.Get("attention") == "true",
		ManualPending: q.Get("manual_pending") == "true", Cursor: q.Get("cursor"), Limit: intParam(q, "limit"), Kinds: kindsParam(q),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) depositDetail(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.DepositDetail(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) creditDeposit(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.CreditDeposit(r.Context(), principal(r), idemKey(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// assignDeposit credits a deposit of nobody to the user named (C5.5 ㉑);
// the answer is the operation, as a backfill's.
func (h *Handler) assignDeposit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.AssignDeposit(r.Context(), principal(r), idemKey(r), chi.URLParam(r, "id"), body.UserID, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, approvalJSON(a))
}

func (h *Handler) rejectDeposit(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.DismissDeposit(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

type backfillBody struct {
	Network string `json:"network"`
	TradeID string `json:"trade_id"`
	Address string `json:"address"`
	TxHash  string `json:"tx_hash"`
	Amount  string `json:"amount"`
	Reason  string `json:"reason"`
}

func (b backfillBody) deposit() (ports.ManualDeposit, error) {
	amount, err := decimal.NewFromString(b.Amount)
	if err != nil {
		return ports.ManualDeposit{}, apperr.Invalid("amount must be a decimal string")
	}
	return ports.ManualDeposit{Network: b.Network, TradeID: b.TradeID, Address: b.Address, TxHash: b.TxHash, Amount: amount}, nil
}

func (h *Handler) checkBackfill(w http.ResponseWriter, r *http.Request) {
	var body backfillBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := body.deposit()
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.Svc.CheckBackfill(r.Context(), principal(r), m)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var value *string
	if c.ValueUSDT != nil {
		v := c.ValueUSDT.String()
		value = &v
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id": c.UserID, "asset": c.Asset, "unclaimed": c.Unclaimed, "value_usdt": value,
	})
}

func (h *Handler) backfill(w http.ResponseWriter, r *http.Request) {
	var body backfillBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := body.deposit()
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.Backfill(r.Context(), principal(r), idemKey(r), m, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, approvalJSON(a))
}
