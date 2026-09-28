// Package httpapi serves the deposit and withdrawal endpoints
// (api/openapi/wallet.yaml).
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/wallet/application"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

// Handler serves deposits; every route needs the identity the gateway
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
		r.Get("/v1/wallet/deposit-address", h.address)
		r.Get("/v1/wallet/deposits", h.deposits)
		r.Get("/v1/wallet/withdraw-addresses", h.listAddresses)
		r.Post("/v1/wallet/withdraw-addresses", h.addAddress)
		r.Delete("/v1/wallet/withdraw-addresses/{id}", h.deleteAddress)
		r.Post("/v1/wallet/withdrawals", h.requestWithdrawal)
		r.Get("/v1/wallet/withdrawals", h.listWithdrawals)
		r.Get("/v1/wallet/withdrawals/{id}", h.getWithdrawal)
		r.Delete("/v1/wallet/withdrawals/{id}", h.cancelWithdrawal)
	})
}

type addressJSON struct {
	Asset         string  `json:"asset"`
	Network       string  `json:"network"`
	Address       string  `json:"address"`
	Contract      *string `json:"contract"`
	MinDeposit    string  `json:"min_deposit"`
	Confirmations uint32  `json:"confirmations"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (h *Handler) address(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	asset, network := strings.ToUpper(q.Get("asset")), strings.ToUpper(q.Get("network"))
	if asset == "" || network == "" {
		httpx.WriteError(w, r, apperr.Invalid("asset and network are required"))
		return
	}
	a, net, err := h.Svc.DepositAddress(r.Context(), httpx.UserID(r), asset, network)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, addressJSON{
		Asset: asset, Network: network, Address: a.Address, Contract: optional(net.Contract),
		MinDeposit: net.MinDeposit.String(), Confirmations: max(net.Confirmations, 1),
	})
}

// DepositJSON is a deposit as clients see it.
type DepositJSON struct {
	ID                    string  `json:"id"`
	Kind                  string  `json:"kind"`
	Asset                 *string `json:"asset"`
	Network               string  `json:"network"`
	Address               string  `json:"address"`
	Contract              *string `json:"contract"`
	TxHash                string  `json:"tx_hash"`
	LogIndex              int64   `json:"log_index"`
	BlockNumber           uint64  `json:"block_number"`
	Amount                string  `json:"amount"`
	RawAmount             string  `json:"raw_amount"`
	Confirmations         uint32  `json:"confirmations"`
	RequiredConfirmations uint32  `json:"required_confirmations"`
	Status                string  `json:"status"`
	Unclaimed             bool    `json:"unclaimed"`
	Reason                *string `json:"reason"`
	DetectedAt            string  `json:"detected_at"`
	ConfirmedAt           *string `json:"confirmed_at"`
	CreditedAt            *string `json:"credited_at"`
}

// ToJSON renders a deposit.
func ToJSON(d domain.Deposit) DepositJSON {
	stamp := func(t interface{ IsZero() bool }, s string) *string {
		if t.IsZero() {
			return nil
		}
		return &s
	}
	kind := d.Kind
	if kind == "" {
		kind = domain.KindChain
	}
	return DepositJSON{
		ID: d.ID, Kind: kind, Asset: optional(d.Asset), Network: d.Network, Address: d.Address, Contract: optional(d.Contract),
		TxHash: d.TxHash, LogIndex: d.LogIndex, BlockNumber: d.BlockNumber, Amount: d.Amount.String(),
		RawAmount: d.RawAmount.String(), Confirmations: d.Confirmations, RequiredConfirmations: d.Required, Status: d.Status,
		Unclaimed: d.Unclaimed, Reason: optional(d.Reason), DetectedAt: httpx.FormatTime(d.DetectedAt),
		ConfirmedAt: stamp(d.ConfirmedAt, httpx.FormatTime(d.ConfirmedAt)),
		CreditedAt:  stamp(d.CreditedAt, httpx.FormatTime(d.CreditedAt)),
	}
}

func (h *Handler) deposits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.Deposits(r.Context(), httpx.UserID(r), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]DepositJSON, 0, len(list))
	for _, d := range list {
		out = append(out, ToJSON(d))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": optional(next)})
}

// stepUpHeader carries the step-up token of sensitive requests.
const stepUpHeader = "X-Step-Up-Token"

// AddressJSON is an entry of the withdrawal address book.
type AddressJSON struct {
	ID        string `json:"id"`
	Network   string `json:"network"`
	Address   string `json:"address"`
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
	UsableAt  string `json:"usable_at"`
}

func addressJSONOf(a domain.WithdrawAddress) AddressJSON {
	return AddressJSON{
		ID: a.ID, Network: a.Network, Address: a.Address, Label: a.Label, CreatedAt: httpx.FormatTime(a.CreatedAt),
		UsableAt: httpx.FormatTime(a.UsableAt),
	}
}

func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Addresses(r.Context(), httpx.UserID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]AddressJSON, 0, len(list))
	for _, a := range list {
		out = append(out, addressJSONOf(a))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) addAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Network string `json:"network"`
		Address string `json:"address"`
		Label   string `json:"label"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.AddAddress(r.Context(), httpx.UserID(r), application.AddressInput{
		Network: strings.ToUpper(body.Network), Address: body.Address, Label: body.Label, StepUp: r.Header.Get(stepUpHeader),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, addressJSONOf(a))
}

func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.DeleteAddress(r.Context(), httpx.UserID(r), chi.URLParam(r, "id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// WithdrawalJSON is a withdrawal as clients see it.
type WithdrawalJSON struct {
	ID                    string   `json:"id"`
	Asset                 string   `json:"asset"`
	Network               string   `json:"network"`
	Address               string   `json:"address"`
	Amount                string   `json:"amount"`
	Fee                   string   `json:"fee"`
	Internal              bool     `json:"internal"`
	Status                string   `json:"status"`
	RiskReasons           []string `json:"risk_reasons"`
	ApprovalsRequired     int      `json:"approvals_required"`
	RejectReason          *string  `json:"reject_reason"`
	TxHash                *string  `json:"tx_hash"`
	Confirmations         uint32   `json:"confirmations"`
	RequiredConfirmations uint32   `json:"required_confirmations"`
	CreatedAt             string   `json:"created_at"`
	ApprovedAt            *string  `json:"approved_at"`
	BroadcastAt           *string  `json:"broadcast_at"`
	ConfirmedAt           *string  `json:"confirmed_at"`
}

func timeOrNil(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := httpx.FormatTime(t)
	return &s
}

// WithdrawalJSONOf renders a withdrawal. Internal transfers never show a
// transaction hash.
func WithdrawalJSONOf(wd domain.Withdrawal) WithdrawalJSON {
	reasons := wd.RiskReasons
	if reasons == nil {
		reasons = []string{}
	}
	return WithdrawalJSON{
		ID: wd.ID, Asset: wd.Asset, Network: wd.Network, Address: wd.Address, Amount: wd.Amount.String(), Fee: wd.Fee.String(),
		Internal: wd.InternalUserID != "", Status: wd.Status, RiskReasons: reasons, ApprovalsRequired: wd.ApprovalsRequired,
		RejectReason: optional(wd.RejectReason), TxHash: optional(wd.TxHash), Confirmations: wd.Confirmations,
		RequiredConfirmations: wd.Required, CreatedAt: httpx.FormatTime(wd.CreatedAt), ApprovedAt: timeOrNil(wd.ApprovedAt),
		BroadcastAt: timeOrNil(wd.BroadcastAt), ConfirmedAt: timeOrNil(wd.ConfirmedAt),
	}
}

func (h *Handler) requestWithdrawal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Asset   string `json:"asset"`
		Network string `json:"network"`
		Address string `json:"address"`
		Amount  string `json:"amount"`
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
	wd, err := h.Svc.RequestWithdrawal(r.Context(), httpx.UserID(r), application.WithdrawalInput{
		Asset: strings.ToUpper(body.Asset), Network: strings.ToUpper(body.Network), Address: body.Address, Amount: amount,
		StepUp: r.Header.Get(stepUpHeader),
	})
	if err != nil {
		if wd.ID != "" {
			err = apperr.From(err).WithDetail("withdrawal_id", wd.ID)
		}
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, WithdrawalJSONOf(wd))
}

func (h *Handler) listWithdrawals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.Withdrawals(r.Context(), httpx.UserID(r), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]WithdrawalJSON, 0, len(list))
	for _, wd := range list {
		out = append(out, WithdrawalJSONOf(wd))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": optional(next)})
}

func (h *Handler) getWithdrawal(w http.ResponseWriter, r *http.Request) {
	wd, err := h.Svc.Withdrawal(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, WithdrawalJSONOf(wd))
}

func (h *Handler) cancelWithdrawal(w http.ResponseWriter, r *http.Request) {
	wd, err := h.Svc.CancelWithdrawal(r.Context(), httpx.UserID(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, WithdrawalJSONOf(wd))
}
