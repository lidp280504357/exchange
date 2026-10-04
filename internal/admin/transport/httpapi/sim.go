package httpapi

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The simulated market of the platform coin (ASTRA design §6, C5): reads
// as market-sim renders them; a change done answers 200/201, one waiting
// for a second administrator 202 with its approval.

func (h *Handler) simStatus(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.SimStatus(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) simHistory(w http.ResponseWriter, r *http.Request) {
	minutes, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
	raw, err := h.Svc.SimHistory(r.Context(), principal(r), minutes)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) simEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	raw, err := h.Svc.SimEvents(r.Context(), principal(r), q.Get("all") == "true" || q.Get("all") == "1", limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) simImpact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Price string `json:"price"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.SimImpact(r.Context(), principal(r), body.Price)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// writeSim answers a change: done (status), or waiting for its approval
// (202).
func writeSim(w http.ResponseWriter, done int, res application.SimResult) {
	switch {
	case res.Approval != nil:
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"approval": approvalJSON(*res.Approval)})
	case res.Event != nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(done)
		_, _ = w.Write(append([]byte(`{"event":`), append(res.Event, '}')...))
	default:
		httpx.WriteJSON(w, done, map[string]any{"version": res.Version})
	}
}

func (h *Handler) createSimEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		application.SimEventInput
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.Svc.CreateSimEvent(r.Context(), principal(r), body.SimEventInput, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeSim(w, http.StatusCreated, res)
}

func (h *Handler) endSimEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.EndSimEvent(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// SimPreviewJSON is a simulated market's request measured now.
type SimPreviewJSON struct {
	ExpiresAt     string          `json:"expires_at"`
	Expired       bool            `json:"expired"`
	TargetPrice   *string         `json:"target_price"`
	ExpectedPrice *string         `json:"expected_price"`
	Move          *float64        `json:"move"`
	RequestedMove *string         `json:"requested_move"`
	Impact        json.RawMessage `json:"impact"`
}

func (h *Handler) simPreview(w http.ResponseWriter, r *http.Request) {
	pv, err := h.Svc.SimApprovalPreview(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := SimPreviewJSON{ExpiresAt: httpx.FormatTime(pv.ExpiresAt), Expired: pv.Expired, Move: pv.Move, Impact: pv.Impact}
	if pv.Target != nil {
		v := pv.Target.String()
		out.TargetPrice = &v
	}
	if pv.Expected != nil {
		v := pv.Expected.Round(8).String()
		out.ExpectedPrice = &v
	}
	if pv.RequestedMove != "" {
		out.RequestedMove = &pv.RequestedMove
	}
	if out.Impact == nil {
		out.Impact = json.RawMessage("null")
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// SimTokenJSON is who holds the simulated market's coin.
type SimTokenJSON struct {
	Asset    string           `json:"asset"`
	Price    *string          `json:"price"`
	Bots     HoldingJSON      `json:"bots"`
	Users    HoldingJSON      `json:"users"`
	Platform []SystemHoldJSON `json:"platform"`
	Issued   string           `json:"issued"`
	Top      []HolderJSON     `json:"top"`
	At       string           `json:"at"`
}

// HoldingJSON is what a kind of account holds and how many hold some.
type HoldingJSON struct {
	Amount  string `json:"amount"`
	Holders uint64 `json:"holders"`
}

// SystemHoldJSON is a system account's balance.
type SystemHoldJSON struct {
	AccountType string `json:"account_type"`
	Amount      string `json:"amount"`
}

// HolderJSON is one of the largest holders.
type HolderJSON struct {
	UserID string `json:"user_id"`
	Amount string `json:"amount"`
}

// adjustmentAccount owes what manual adjustments created: its negative
// balance is what they issued.
const adjustmentAccount = "ADJUSTMENT"

func (h *Handler) simToken(w http.ResponseWriter, r *http.Request) {
	t, err := h.Svc.SimTokenHoldings(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := SimTokenJSON{
		Asset: t.Asset, Bots: HoldingJSON{Amount: t.Bots.String(), Holders: t.BotHolders},
		Users: HoldingJSON{Amount: t.Users.String(), Holders: t.UserHolders}, Platform: []SystemHoldJSON{},
		Issued: t.System[adjustmentAccount].Neg().String(), Top: []HolderJSON{}, At: httpx.FormatTime(t.At),
	}
	if t.Price != nil {
		v := t.Price.String()
		out.Price = &v
	}
	for _, account := range slices.Sorted(maps.Keys(t.System)) {
		if account != adjustmentAccount {
			out.Platform = append(out.Platform, SystemHoldJSON{AccountType: account, Amount: t.System[account].String()})
		}
	}
	for _, hd := range t.Top {
		out.Top = append(out.Top, HolderJSON{UserID: hd.UserID, Amount: hd.Amount.String()})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// simMint books more of the coin or USDT for the bots: a fund operation,
// at once within the single-person limits or waiting for a second
// administrator (201 either way, with the operation).
func (h *Handler) simMint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Asset     string `json:"asset"`
		Amount    string `json:"amount"`
		Role      string `json:"role"`
		Reason    string `json:"reason"`
		Reference string `json:"reference"`
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
	a, err := h.Svc.MintSimBots(r.Context(), principal(r), application.SimMintInput{
		Asset: body.Asset, Amount: amount, Role: body.Role, Reason: body.Reason, Reference: body.Reference, Key: idemKey(r),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, approvalJSON(a))
}

func (h *Handler) updateSimParams(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Params json.RawMessage `json:"params"`
		Reason string          `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.Svc.UpdateSimParams(r.Context(), principal(r), body.Params, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeSim(w, http.StatusOK, res)
}
