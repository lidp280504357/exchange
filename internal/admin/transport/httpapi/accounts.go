package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// BalanceJSON is one of a user's balances with its worth.
type BalanceJSON struct {
	AccountType string  `json:"account_type"`
	Asset       string  `json:"asset"`
	Available   string  `json:"available"`
	Frozen      string  `json:"frozen"`
	Total       string  `json:"total"`
	ValueUSDT   *string `json:"value_usdt"`
}

func (h *Handler) balances(w http.ResponseWriter, r *http.Request) {
	b, err := h.Svc.Balances(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]BalanceJSON, 0, len(b.Balances))
	for _, x := range b.Balances {
		row := BalanceJSON{AccountType: x.AccountType, Asset: x.Asset, Available: x.Available, Frozen: x.Frozen, Total: x.Total.String()}
		if x.ValueUSDT != nil {
			v := x.ValueUSDT.String()
			row.ValueUSDT = &v
		}
		out = append(out, row)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"balances": out, "total_usdt": b.TotalUSDT.String(), "unpriced": b.Unpriced})
}

// HoldJSON is an administrator's hold on part of a user's SPOT balance.
type HoldJSON struct {
	ID               string  `json:"id"`
	AccountType      string  `json:"account_type"`
	Asset            string  `json:"asset"`
	Amount           string  `json:"amount"`
	Reason           string  `json:"reason"`
	Actor            string  `json:"actor"`
	JournalID        string  `json:"journal_id"`
	CreatedAt        *string `json:"created_at"`
	Active           bool    `json:"active"`
	ReleasedAt       *string `json:"released_at"`
	ReleasedBy       string  `json:"released_by"`
	ReleaseReason    string  `json:"release_reason"`
	ReleaseJournalID *string `json:"release_journal_id"`
}

func holdJSON(x ports.Hold) HoldJSON {
	out := HoldJSON{
		ID: x.ID, AccountType: x.AccountType, Asset: x.Asset, Amount: x.Amount, Reason: x.Reason, Actor: x.Actor, JournalID: x.JournalID,
		CreatedAt: optTime(x.CreatedAt), Active: x.ReleasedAt.IsZero(), ReleasedAt: optTime(x.ReleasedAt), ReleasedBy: x.ReleasedBy,
		ReleaseReason: x.ReleaseReason,
	}
	if x.ReleaseJournalID != "" {
		out.ReleaseJournalID = &x.ReleaseJournalID
	}
	return out
}

func (h *Handler) holds(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Holds(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]HoldJSON, 0, len(list))
	for _, x := range list {
		out = append(out, holdJSON(x))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"holds": out})
}

func (h *Handler) placeHold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Asset  string `json:"asset"`
		Amount string `json:"amount"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	amount, err := decimal.NewFromString(body.Amount)
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("amount must be a decimal string"))
		return
	}
	x, err := h.Svc.PlaceHold(r.Context(), principal(r), idemKey(r), chi.URLParam(r, "id"), body.Asset, amount, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, holdJSON(x))
}

func (h *Handler) releaseHold(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	x, err := h.Svc.ReleaseHold(r.Context(), principal(r), idemKey(r), chi.URLParam(r, "id"), chi.URLParam(r, "hold"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, holdJSON(x))
}

func (h *Handler) cancelOrder(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.CancelOrder(r.Context(), principal(r), chi.URLParam(r, "id"), chi.URLParam(r, "order"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, raw)
}

func (h *Handler) contractOrders(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.ContractOrders(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) cancelContractOrder(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.CancelContractOrder(r.Context(), principal(r), chi.URLParam(r, "id"), chi.URLParam(r, "order"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, raw)
}

func (h *Handler) userPositions(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.Positions(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]json.RawMessage{"positions": raw})
}

// futuresMargin previews a debit of a user's FUTURES balance (?debit=).
func (h *Handler) futuresMargin(w http.ResponseWriter, r *http.Request) {
	debit := decimal.Zero
	if v := r.URL.Query().Get("debit"); v != "" {
		var err error
		if debit, err = decimal.NewFromString(v); err != nil {
			httpx.WriteError(w, r, apperr.Invalid("debit must be a decimal string"))
			return
		}
	}
	raw, err := h.Svc.FuturesMargin(r.Context(), principal(r), chi.URLParam(r, "id"), debit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) closePosition(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol       string `json:"symbol"`
		PositionSide string `json:"position_side"`
		Reason       string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.ClosePosition(r.Context(), principal(r), idemKey(r), chi.URLParam(r, "id"), body.Symbol, body.PositionSide, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}
