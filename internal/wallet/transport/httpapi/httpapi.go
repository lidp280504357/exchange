// Package httpapi serves the deposit endpoints (api/openapi/wallet.yaml).
package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

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
	return DepositJSON{
		ID: d.ID, Asset: optional(d.Asset), Network: d.Network, Address: d.Address, Contract: optional(d.Contract),
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
