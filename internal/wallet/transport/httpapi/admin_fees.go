package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/wallet/application"
	"github.com/skill/exchange/internal/wallet/domain"
)

// feeRoutes serves the console's handling of the custodians' withdrawal
// fees (review ④): the list, and the decisions on one held for a person.
func (h *Handler) feeRoutes(r chi.Router) {
	r.Get("/internal/wallet/custody/fees", h.adminCustodyFees)
	r.Post("/internal/wallet/custody/fees/{withdrawal}/book", h.adminBookCustodyFee)
	r.Post("/internal/wallet/custody/fees/{withdrawal}/write-off", h.adminWriteOffCustodyFee)
}

// CustodyFeeJSON is a custodian's withdrawal fee as the console sees it.
type CustodyFeeJSON struct {
	WithdrawalID string `json:"withdrawal_id"`
	// Provider is the withdrawal's custodian (UDUN, UDUNMOCK).
	Provider string `json:"provider"`
	// TxHash keys the fee (the custodian and its trade); the list's cursor.
	TxHash  string `json:"tx_hash"`
	Asset   string `json:"asset"`
	Network string `json:"network"`
	Amount  string `json:"amount"`
	// Unit is how the custodian counts its fee on the withdrawal's network
	// as a person confirmed it (SELF, MAIN, OUTSIDE); null while nobody did.
	Unit         *string `json:"unit"`
	Status       string  `json:"status"`
	HoldReason   string  `json:"hold_reason"`
	JournalID    *string `json:"journal_id"`
	CreatedAt    string  `json:"created_at"`
	BookedAt     *string `json:"booked_at"`
	WrittenOffAt *string `json:"written_off_at"`
	ResolvedBy   string  `json:"resolved_by"`
	Resolution   string  `json:"resolution"`
}

// CustodyFeeJSONOf renders a custodian's fee; its custodian, when not
// given, from its key ("UDUNMOCK:<trade>").
func CustodyFeeJSONOf(f domain.CustodyFee) CustodyFeeJSON {
	if p, _, ok := strings.Cut(f.TxHash, ":"); f.Provider == "" && ok {
		f.Provider = p
	}
	j := CustodyFeeJSON{
		WithdrawalID: f.WithdrawalID, Provider: f.Provider, TxHash: f.TxHash, Asset: f.Asset, Network: f.Network, Amount: f.Amount.String(),
		Unit:   textOrNil(f.Unit),
		Status: f.Status, HoldReason: f.HoldReason, JournalID: textOrNil(f.JournalID), CreatedAt: httpx.FormatTime(f.CreatedAt),
		BookedAt: timeOrNil(f.BookedAt), ResolvedBy: f.ResolvedBy, Resolution: f.Resolution,
	}
	if f.Status == domain.FeeWrittenOff {
		j.WrittenOffAt = timeOrNil(f.ResolvedAt)
	}
	return j
}

// adminCustodyFees pages through the fees, newest first: provider (UDUN,
// UDUNMOCK; any when absent), status (HELD, BOOKABLE, WRITTEN_OFF; any when
// absent), cursor, limit (at most 200, default 50).
func (h *Handler) adminCustodyFees(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.CustodyFees(r.Context(), q.Get("provider"), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]CustodyFeeJSON, 0, len(list))
	for _, f := range list {
		out = append(out, CustodyFeeJSONOf(f))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": textOrNil(next)})
}

type bookFeeBody struct {
	Asset  string `json:"asset"`
	Amount string `json:"amount"`
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

// adminBookCustodyFee books a held fee from GAS_SUPPLY: as reported, or in
// the asset and amount the administrator found it charged.
func (h *Handler) adminBookCustodyFee(w http.ResponseWriter, r *http.Request) {
	var body bookFeeBody
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
	h.decideFee(w, r, application.FeeResolution{
		WithdrawalID: chi.URLParam(r, "withdrawal"), Book: true, Asset: body.Asset, Amount: amount, Actor: body.Actor, Reason: body.Reason,
	})
}

// adminWriteOffCustodyFee writes a held fee off: it was not taken from
// the coin balances, or was reported in another unit.
func (h *Handler) adminWriteOffCustodyFee(w http.ResponseWriter, r *http.Request) {
	var body decisionBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.decideFee(w, r, application.FeeResolution{WithdrawalID: chi.URLParam(r, "withdrawal"), Actor: body.Actor, Reason: body.Reason})
}

func (h *Handler) decideFee(w http.ResponseWriter, r *http.Request, d application.FeeResolution) {
	f, err := h.Svc.DecideCustodyFee(r.Context(), d)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, CustodyFeeJSONOf(domain.CustodyFee{ChainFee: f, WithdrawalID: d.WithdrawalID}))
}
