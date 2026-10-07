// Package httpapi is market-maker's internal API: HOUSE's runtime caps
// (review C45), for the console (admin-service; the gateway does not pass
// /internal). Amounts are decimal strings in USDT; the leverage a number
// of times.
//
// A change (PUT) must be signed (internal/platform/svcsign, review FL,
// C47) with one of two keys: KeyOps (HOUSE_CAPS_API_SECRET, exchangectl
// in market-maker's container) or KeyAdmin (HOUSE_CAPS_ADMIN_API_SECRET,
// the admin console's service). The signer vouches for the actor it names,
// and only KeyAdmin may name an approver: the console signs both
// operators in. The reads are not signed.
//
//	GET /internal/house/caps          the caps in force and their version
//	PUT /internal/house/caps          a change {"level": "...", ..., "version", "actor", "approver", "approval_id", "reason"}
//	GET /internal/house/caps/changes  the latest changes, newest first (?limit=)
//	GET /internal/house/rooms/{symbol} what HOUSE may still buy and sell there (market-sim's price events)
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketmaker/application"
	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/svcsign"
)

// The keys the changes are signed with: exchangectl's in market-maker's
// container, and the admin console's service's, the only one that may
// name an approver.
const (
	KeyOps   = "ops"
	KeyAdmin = "admin"
)

// ErrApprovalNeedsAdmin refuses an approver named by any caller but the
// admin console's service.
var ErrApprovalNeedsAdmin = apperr.New(apperr.KindForbidden, "HOUSE_CAPS_APPROVAL_NEEDS_ADMIN",
	"only the admin console's service names an approver: it signed both operators in")

// Handler serves the caps; Signed checks the changes' signatures. Rooms
// gives HOUSE's rooms by symbol (nil: not served).
type Handler struct {
	Caps   *application.Caps
	Signed *svcsign.Verifier
	Rooms  func(symbol string) (application.Rooms, bool)
}

// Routes registers the handlers on r.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/internal/house/caps", h.get)
	r.With(h.Signed.Changes).Put("/internal/house/caps", h.put)
	r.Get("/internal/house/caps/changes", h.changes)
	if h.Rooms != nil {
		r.Get("/internal/house/rooms/{symbol}", h.rooms)
	}
}

// rooms answers what HOUSE may still buy and sell of a symbol as last
// published (J0 contract §4.2); 404 while it does not quote it.
func (h *Handler) rooms(w http.ResponseWriter, r *http.Request) {
	ro, ok := h.Rooms(strings.ToUpper(chi.URLParam(r, "symbol")))
	if !ok {
		httpx.WriteError(w, r, apperr.NotFound("HOUSE does not quote the symbol"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"symbol": ro.Symbol, "buy": ro.Buy.String(), "sell": ro.Sell.String(), "mid": ro.Mid.String(), "unit_value": ro.UnitValue.String(),
		"inverse": ro.Inverse, "updated_at": ro.At.UTC().Format(time.RFC3339Nano),
	})
}

// capsJSON are the caps as the API gives them.
type capsJSON struct {
	Level            string `json:"level"`
	Symbol           string `json:"symbol"`
	Total            string `json:"total"`
	Contract         string `json:"contract"`
	Safety           string `json:"safety"`
	ContractLeverage string `json:"contract_leverage"`
}

func toJSON(c domain.Caps) capsJSON {
	return capsJSON{
		Level: c.Level.String(), Symbol: c.Symbol.String(), Total: c.Total.String(), Contract: c.Contract.String(),
		Safety: c.Safety.String(), ContractLeverage: c.ContractLeverage.String(),
	}
}

type storedJSON struct {
	capsJSON
	Version   int64  `json:"version"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

func stored(s domain.StoredCaps) storedJSON {
	return storedJSON{capsJSON: toJSON(s.Caps), Version: s.Version, UpdatedBy: s.UpdatedBy, UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func (h *Handler) get(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, stored(h.Caps.Get()))
}

// putBody changes the caps of version: the caps given (decimal strings),
// the others as they are.
type putBody struct {
	Level            *string `json:"level"`
	Symbol           *string `json:"symbol"`
	Total            *string `json:"total"`
	Contract         *string `json:"contract"`
	Safety           *string `json:"safety"`
	ContractLeverage *string `json:"contract_leverage"`
	Version          int64   `json:"version"`
	Actor            string  `json:"actor"`
	Approver         string  `json:"approver"`
	ApprovalID       string  `json:"approval_id"`
	Reason           string  `json:"reason"`
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var b putBody
	if err := httpx.DecodeJSON(w, r, &b); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	signedBy := svcsign.KeyID(r.Context())
	if (b.Approver != "" || b.ApprovalID != "") && signedBy != KeyAdmin {
		httpx.WriteError(w, r, ErrApprovalNeedsAdmin)
		return
	}
	// Only the caps given: the store applies them to the caps it locks.
	var patch domain.CapsPatch
	for _, f := range []struct {
		name string
		in   *string
		out  **decimal.Decimal
	}{
		{"level", b.Level, &patch.Level},
		{"symbol", b.Symbol, &patch.Symbol},
		{"total", b.Total, &patch.Total},
		{"contract", b.Contract, &patch.Contract},
		{"safety", b.Safety, &patch.Safety},
		{"contract_leverage", b.ContractLeverage, &patch.ContractLeverage},
	} {
		if f.in == nil {
			continue
		}
		v, err := decimal.NewFromString(*f.in)
		if err != nil {
			httpx.WriteError(w, r, apperr.Invalid(f.name+" must be a decimal string"))
			return
		}
		*f.out = &v
	}
	s, err := h.Caps.Change(r.Context(), domain.CapsChange{
		Patch: patch, Version: b.Version, Actor: b.Actor, Approver: b.Approver, ApprovalID: b.ApprovalID, Reason: b.Reason,
		SignedBy: signedBy,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stored(s))
}

type changeJSON struct {
	Version    int64     `json:"version"`
	Caps       capsJSON  `json:"caps"`
	Previous   *capsJSON `json:"previous"`
	Actor      string    `json:"actor"`
	Approver   string    `json:"approver"`
	ApprovalID string    `json:"approval_id"`
	Reason     string    `json:"reason"`
	SignedBy   string    `json:"signed_by"`
	At         string    `json:"at"`
}

func (h *Handler) changes(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := h.Caps.Changes(r.Context(), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]changeJSON, 0, len(list))
	for _, c := range list {
		j := changeJSON{
			Version: c.Version, Caps: toJSON(c.Caps), Actor: c.Actor, Approver: c.Approver, ApprovalID: c.ApprovalID, Reason: c.Reason,
			SignedBy: c.SignedBy, At: c.At.UTC().Format(time.RFC3339Nano),
		}
		if c.Previous != nil {
			p := toJSON(*c.Previous)
			j.Previous = &p
		}
		out = append(out, j)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}
