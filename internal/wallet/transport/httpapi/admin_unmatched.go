package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/wallet/domain"
)

// unmatchedRoutes serves the console's "credit to a user" of a deposit of
// nobody (a custodian's deposit to an address no user has, B7a).
func (h *Handler) unmatchedRoutes(r chi.Router) {
	r.Post("/internal/wallet/deposits/{id}/assign", h.adminAssignDeposit)
}

type assignBody struct {
	UserID string `json:"user_id"`
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

// adminAssignDeposit names the user a deposit of nobody is credited to
// and releases it to them, audited (wallet.deposit.assigned, and the
// ledger's ledger.unclaimed_released).
func (h *Handler) adminAssignDeposit(w http.ResponseWriter, r *http.Request) {
	var body assignBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.Svc.AssignDeposit(r.Context(), chi.URLParam(r, "id"), body.UserID, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	j := AdminDepositJSONOf(d)
	httpx.WriteJSON(w, http.StatusOK, j)
}

// withOwner adds, to a deposit of nobody, who its address belongs to now
// or belonged to before it was retired: a hint for the person who decides.
func (h *Handler) withOwner(ctx context.Context, j *AdminDepositJSON, d domain.Deposit) error {
	if d.UserID != domain.NoOwner {
		return nil
	}
	owner, retired, err := h.Svc.AddressOwner(ctx, d.Network, d.Address)
	if err != nil {
		return err
	}
	j.AddressOwner, j.AddressOwnerRetired = textOrNil(owner), retired
	return nil
}
