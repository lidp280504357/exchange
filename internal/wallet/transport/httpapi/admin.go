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
	"github.com/skill/exchange/internal/wallet/ports"
)

// adminRoutes serves the admin console's handling of withdrawals in
// review and of deposits that need a person (design 2026-10-02 §4.2,
// §4.3); the gateway does not route /internal.
func (h *Handler) adminRoutes(r chi.Router) {
	r.Get("/internal/wallet/withdrawals/{id}", h.adminWithdrawal)
	r.Post("/internal/wallet/withdrawals/{id}/hold", h.adminHold)
	r.Get("/internal/wallet/deposits", h.adminDeposits)
	r.Post("/internal/wallet/deposits/manual/check", h.adminCheckManual)
	r.Post("/internal/wallet/deposits/manual", h.adminBookManual)
	r.Get("/internal/wallet/deposits/{id}", h.adminDeposit)
	r.Post("/internal/wallet/deposits/{id}/credit", h.adminCreditDeposit)
	r.Post("/internal/wallet/deposits/{id}/dismiss", h.adminDismissDeposit)
	h.suspensionRoutes(r)
	h.unmatchedRoutes(r)
	h.feeRoutes(r)
}

// AddressBookJSON is a withdrawal address in its user's address book.
type AddressBookJSON struct {
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
	UsableAt  string `json:"usable_at"`
}

func (h *Handler) adminWithdrawal(w http.ResponseWriter, r *http.Request) {
	d, err := h.Svc.Detail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var book *AddressBookJSON
	if d.Address != nil {
		book = &AddressBookJSON{Label: d.Address.Label, CreatedAt: httpx.FormatTime(d.Address.CreatedAt), UsableAt: httpx.FormatTime(d.Address.UsableAt)}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"withdrawal": AdminWithdrawalJSONOf(d.Withdrawal), "address_book": book, "used_today_usdt": d.UsedToday.String(),
		"used_month_usdt": d.UsedMonth.String(),
	})
}

func (h *Handler) adminHold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hold     bool   `json:"hold"`
		Reviewer string `json:"reviewer"`
		Note     string `json:"note"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	wd, err := h.Svc.HoldWithdrawal(r.Context(), chi.URLParam(r, "id"), body.Hold, body.Reviewer, body.Note)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, AdminWithdrawalJSONOf(wd))
}

// AdminDepositJSON is a deposit as the admin console sees it.
type AdminDepositJSON struct {
	ID                    string  `json:"id"`
	UserID                string  `json:"user_id"`
	Kind                  string  `json:"kind"`
	Asset                 *string `json:"asset"`
	Network               string  `json:"network"`
	Address               string  `json:"address"`
	TxHash                string  `json:"tx_hash"`
	Amount                string  `json:"amount"`
	Status                string  `json:"status"`
	Unclaimed             bool    `json:"unclaimed"`
	Reason                *string `json:"reason"`
	TradeID               *string `json:"trade_id"`
	Confirmations         uint32  `json:"confirmations"`
	RequiredConfirmations uint32  `json:"required_confirmations"`
	JournalID             *string `json:"journal_id"`
	DetectedAt            string  `json:"detected_at"`
	ConfirmedAt           *string `json:"confirmed_at"`
	CreditedAt            *string `json:"credited_at"`
	Source                string  `json:"source"`
	EnteredBy             string  `json:"entered_by"`
	CallbackAt            *string `json:"callback_at"`
	Discrepancy           string  `json:"discrepancy"`
	Attention             bool    `json:"attention"`
	Resolution            string  `json:"resolution"`
	ResolvedBy            string  `json:"resolved_by"`
	ResolvedAt            *string `json:"resolved_at"`
	ResolutionNote        string  `json:"resolution_note"`
	ReleaseJournalID      *string `json:"release_journal_id"`
	// For a deposit of nobody (user_id the nil UUID, reason
	// UNKNOWN_ADDRESS): the user its address belongs to now, or belonged to
	// before it was retired (address_owner_retired), a hint for crediting
	// it (POST /internal/wallet/deposits/{id}/assign).
	AddressOwner        *string `json:"address_owner"`
	AddressOwnerRetired bool    `json:"address_owner_retired"`
}

func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// AdminDepositJSONOf renders a deposit for the admin console.
func AdminDepositJSONOf(d domain.Deposit) AdminDepositJSON {
	kind := d.Kind
	if kind == "" {
		kind = domain.KindChain
	}
	source := d.Source
	if source == "" {
		source = "AUTO"
	}
	return AdminDepositJSON{
		ID: d.ID, UserID: d.UserID, Kind: kind, Asset: textOrNil(d.Asset), Network: d.Network, Address: d.Address, TxHash: d.TxHash,
		Amount: d.Amount.String(), Status: d.Status, Unclaimed: d.Unclaimed, Reason: textOrNil(d.Reason), TradeID: textOrNil(d.ProviderTxID),
		Confirmations: d.Confirmations, RequiredConfirmations: d.Required, JournalID: textOrNil(d.JournalID),
		DetectedAt: httpx.FormatTime(d.DetectedAt), ConfirmedAt: timeOrNil(d.ConfirmedAt), CreditedAt: timeOrNil(d.CreditedAt),
		Source: source, EnteredBy: d.EnteredBy, CallbackAt: timeOrNil(d.CallbackAt), Discrepancy: d.Discrepancy, Attention: d.Attention(),
		Resolution: d.Resolution, ResolvedBy: d.ResolvedBy, ResolvedAt: timeOrNil(d.ResolvedAt), ResolutionNote: d.ResolutionNote,
		ReleaseJournalID: textOrNil(d.ReleaseJournalID),
	}
}

// adminDeposits pages through deposits, newest first: user_id, status,
// network, attention=true (waiting for a decision), manual_pending=true
// (backfilled, no callback yet), cursor, limit (at most 200, default 50).
func (h *Handler) adminDeposits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	only, exclude, err := httpx.UserIDsFrom(q)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.AdminDeposits(r.Context(), ports.DepositFilter{
		UserID: q.Get("user_id"), Status: strings.ToUpper(q.Get("status")), Network: strings.ToUpper(q.Get("network")),
		Attention: q.Get("attention") == "true", ManualPending: q.Get("manual_pending") == "true", After: q.Get("cursor"), Limit: limit,
		Users: ports.UserIDs{Only: only, Exclude: exclude},
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]AdminDepositJSON, 0, len(list))
	for _, d := range list {
		j := AdminDepositJSONOf(d)
		if err := h.withOwner(r.Context(), &j, d); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		out = append(out, j)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": textOrNil(next)})
}

func (h *Handler) adminDeposit(w http.ResponseWriter, r *http.Request) {
	d, err := h.Svc.AdminDeposit(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	j := AdminDepositJSONOf(d)
	if err := h.withOwner(r.Context(), &j, d); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, j)
}

type decisionBody struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

func (h *Handler) adminCreditDeposit(w http.ResponseWriter, r *http.Request) {
	var body decisionBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.Svc.CreditDeposit(r.Context(), chi.URLParam(r, "id"), body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, AdminDepositJSONOf(d))
}

func (h *Handler) adminDismissDeposit(w http.ResponseWriter, r *http.Request) {
	var body decisionBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.Svc.DismissDeposit(r.Context(), chi.URLParam(r, "id"), body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, AdminDepositJSONOf(d))
}

type manualBody struct {
	Network string `json:"network"`
	TradeID string `json:"trade_id"`
	Address string `json:"address"`
	TxHash  string `json:"tx_hash"`
	Amount  string `json:"amount"`
	Actor   string `json:"actor"`
	Reason  string `json:"reason"`
}

func (b manualBody) input() (application.ManualDeposit, error) {
	amount, err := decimal.NewFromString(b.Amount)
	if err != nil {
		return application.ManualDeposit{}, apperr.Invalid("amount must be a decimal string")
	}
	return application.ManualDeposit{
		Network: b.Network, TradeID: b.TradeID, Address: b.Address, TxHash: b.TxHash, Amount: amount, Actor: b.Actor,
	}, nil
}

// adminCheckManual checks a backfill and answers the deposit it would
// book, without booking it.
func (h *Handler) adminCheckManual(w http.ResponseWriter, r *http.Request) {
	var body manualBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in, err := body.input()
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.Svc.CheckManualDeposit(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, AdminDepositJSONOf(d))
}

func (h *Handler) adminBookManual(w http.ResponseWriter, r *http.Request) {
	var body manualBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in, err := body.input()
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.Svc.BookManualDeposit(r.Context(), in, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, AdminDepositJSONOf(d))
}
