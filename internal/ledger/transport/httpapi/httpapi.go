// Package httpapi serves the account endpoints (api/openapi/account.yaml).
package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/ledger/application"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// HeaderIdempotencyKey carries the client's key for writes (§7.1).
const HeaderIdempotencyKey = "Idempotency-Key"

// Handler serves balances, transfers and the fund flow; every route needs
// the identity the gateway attaches.
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
		r.Get("/v1/account/balances", h.balances)
		r.Post("/v1/account/transfers", h.transfer)
		r.Get("/v1/account/transfers", h.transfers)
		r.Get("/v1/account/ledger", h.ledger)
	})
}

type balanceJSON struct {
	AccountType string `json:"account_type"`
	Asset       string `json:"asset"`
	Available   string `json:"available"`
	Frozen      string `json:"frozen"`
	Total       string `json:"total"`
}

func (h *Handler) balances(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Balances(r.Context(), httpx.UserID(r), r.URL.Query().Get("account_type"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]balanceJSON, 0, len(list))
	for _, a := range list {
		out = append(out, balanceJSON{
			AccountType: a.Key.Type, Asset: a.Key.Asset, Available: a.Available.String(), Frozen: a.Frozen.String(),
			Total: a.Available.Add(a.Frozen).String(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"balances": out})
}

type transferBody struct {
	Asset           string `json:"asset"`
	Amount          string `json:"amount"`
	FromAccountType string `json:"from_account_type"`
	ToAccountType   string `json:"to_account_type"`
}

type transferJSON struct {
	TransferID      string `json:"transfer_id"`
	Asset           string `json:"asset"`
	Amount          string `json:"amount"`
	FromAccountType string `json:"from_account_type"`
	ToAccountType   string `json:"to_account_type"`
	Status          string `json:"status"`
	FailureReason   string `json:"failure_reason,omitempty"`
	CreatedAt       string `json:"created_at"`
}

func toTransferJSON(t domain.Transfer) transferJSON {
	return transferJSON{
		TransferID: t.ID, Asset: t.Asset, Amount: t.Amount.String(), FromAccountType: t.From, ToAccountType: t.To,
		Status: t.Status, FailureReason: t.FailureReason, CreatedAt: httpx.FormatTime(t.CreatedAt),
	}
}

func (h *Handler) transfer(w http.ResponseWriter, r *http.Request) {
	var body transferBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	amount, err := decimal.NewFromString(body.Amount)
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("amount must be a decimal string"))
		return
	}
	t, err := h.Svc.Transfer(r.Context(), application.TransferInput{
		UserID: httpx.UserID(r), IdemKey: r.Header.Get(HeaderIdempotencyKey), Asset: body.Asset, Amount: amount,
		From: body.FromAccountType, To: body.ToAccountType,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toTransferJSON(t))
}

func (h *Handler) transfers(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, next, err := h.Svc.Transfers(r.Context(), httpx.UserID(r), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]transferJSON, 0, len(list))
	for _, t := range list {
		out = append(out, toTransferJSON(t))
	}
	resp := map[string]any{"items": out, "next_cursor": nil}
	if next != "" {
		resp["next_cursor"] = next
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type entryJSON struct {
	ID          string `json:"id"`
	JournalID   string `json:"journal_id"`
	EntryType   string `json:"entry_type"`
	AccountType string `json:"account_type"`
	Asset       string `json:"asset"`
	Amount      string `json:"amount"`
	BalanceKind string `json:"balance_kind"`
	Available   string `json:"available_after"`
	Frozen      string `json:"frozen_after"`
	PostedAt    string `json:"posted_at"`
}

func (h *Handler) ledger(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var before int64
	if c := q.Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n <= 0 {
			httpx.WriteError(w, r, apperr.Invalid("invalid cursor"))
			return
		}
		before = n
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, next, err := h.Svc.Entries(r.Context(), httpx.UserID(r), q.Get("asset"), q.Get("type"), before, limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]entryJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryJSON{
			ID: strconv.FormatInt(e.ID, 10), JournalID: e.JournalID, EntryType: e.EntryType, AccountType: e.AccountType,
			Asset: e.Asset, Amount: e.Amount.String(), BalanceKind: e.Kind, Available: e.Available.String(),
			Frozen: e.Frozen.String(), PostedAt: httpx.FormatTime(e.PostedAt),
		})
	}
	resp := map[string]any{"items": out, "next_cursor": nil}
	if next > 0 {
		resp["next_cursor"] = strconv.FormatInt(next, 10)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}
