package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Margin trading (margin design 2026-10-06 §8, E5): margin-service's terms
// and accounts, the read models' liquidations and interest.

func (h *Handler) marginRoutes(r chi.Router) {
	r.Get("/margin/assets", h.marginAssets)
	r.Put("/margin/assets/{asset}", h.setMarginAsset)
	r.Get("/margin/settings", h.marginSettings)
	r.Put("/margin/settings", h.setMarginSettings)
	r.Get("/margin/pairs", h.marginPairs)
	r.Put("/margin/pairs/{symbol}", h.setMarginPair)
	r.Get("/margin/accounts", h.marginAccounts)
	r.Get("/margin/accounts/{user_id}/{account}", h.marginAccount)
	r.Post("/margin/accounts/{user_id}/{account}/freeze", h.freezeMarginAccount)
	r.Post("/margin/accounts/{user_id}/{account}/unfreeze", h.unfreezeMarginAccount)
	r.With(needKey).Post("/margin/accounts/{user_id}/{account}/liquidate", h.liquidateMarginAccount)
	r.Get("/margin/liquidations", h.marginLiquidations)
	r.Get("/margin/interest", h.marginInterest)
}

func (h *Handler) marginAssets(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.MarginAssets(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) marginPairs(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.MarginPairs(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) marginSettings(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.MarginSettings(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// The fields a change of terms gives, every one of them: a field left out
// would read as false or zero (borrowing switched off at once).
var (
	marginAssetFields = []string{
		"borrowable", "collateral", "haircut", "pool_cap", "user_cap", "interest_model", "fixed_rate", "float_base", "float_kink",
		"float_kink_rate", "float_max_rate",
	}
	marginTermsFields = []string{"leverage", "warn_level", "liquidation_level", "liquidation_fee"}
	marginPairFields  = append([]string{"isolated"}, marginTermsFields...)
)

// decodeMarginChange reads a change of terms: every one of fields, given
// as into reads them, with expected_version and the reason.
func decodeMarginChange(w http.ResponseWriter, r *http.Request, fields []string, into any) (int64, string, error) {
	var body map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		return 0, "", err
	}
	var version *int64
	var reason string
	if err := json.Unmarshal(body["expected_version"], &version); err != nil || version == nil {
		return 0, "", apperr.Invalid("expected_version is the version read")
	}
	if raw, ok := body["reason"]; ok && json.Unmarshal(raw, &reason) != nil {
		return 0, "", apperr.Invalid("reason is a text")
	}
	delete(body, "expected_version")
	delete(body, "reason")
	for k := range body {
		if !slices.Contains(fields, k) {
			return 0, "", apperr.Invalid("unknown field " + k)
		}
	}
	for _, f := range fields {
		if _, ok := body[f]; !ok {
			return 0, "", apperr.Invalid("every field of the terms is given: "+strings.Join(fields, ", ")).WithDetail("field", f)
		}
	}
	raw, _ := json.Marshal(body)
	if err := json.Unmarshal(raw, into); err != nil {
		return 0, "", apperr.Invalid("the terms are not as described: " + err.Error())
	}
	return *version, reason, nil
}

// writeMarginResult answers 200 with margin-service's answer when the
// change applied, 202 with the request when a second ADMIN decides it.
func writeMarginResult(w http.ResponseWriter, res application.MarginResult) {
	if res.Approval != nil {
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"approval": approvalJSON(*res.Approval)})
		return
	}
	writeRaw(w, res.Value)
}

func (h *Handler) setMarginAsset(w http.ResponseWriter, r *http.Request) {
	var terms application.MarginAssetTerms
	version, reason, err := decodeMarginChange(w, r, marginAssetFields, &terms)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.Svc.SetMarginAsset(r.Context(), principal(r), chi.URLParam(r, "asset"), terms, version, reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeMarginResult(w, res)
}

func (h *Handler) setMarginPair(w http.ResponseWriter, r *http.Request) {
	var terms application.MarginPairTerms
	version, reason, err := decodeMarginChange(w, r, marginPairFields, &terms)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.Svc.SetMarginPair(r.Context(), principal(r), chi.URLParam(r, "symbol"), terms, version, reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeMarginResult(w, res)
}

// setMarginSettings takes the cross account's terms as {cross: {...}}:
// every change of them waits for a second ADMIN (202).
func (h *Handler) setMarginSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cross           json.RawMessage `json:"cross"`
		ExpectedVersion *int64          `json:"expected_version"`
		Reason          string          `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.ExpectedVersion == nil {
		httpx.WriteError(w, r, apperr.Invalid("expected_version is the version read"))
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body.Cross, &fields) != nil || fields == nil {
		httpx.WriteError(w, r, apperr.Invalid("cross is the cross account's terms"))
		return
	}
	for k := range fields {
		if !slices.Contains(marginTermsFields, k) {
			httpx.WriteError(w, r, apperr.Invalid("unknown field cross."+k))
			return
		}
	}
	for _, f := range marginTermsFields {
		if _, ok := fields[f]; !ok {
			httpx.WriteError(w, r, apperr.Invalid("every field of the terms is given: "+strings.Join(marginTermsFields, ", ")).WithDetail("field", f))
			return
		}
	}
	var terms application.MarginTerms
	if err := json.Unmarshal(body.Cross, &terms); err != nil {
		httpx.WriteError(w, r, apperr.Invalid("the terms are not as described: "+err.Error()))
		return
	}
	a, err := h.Svc.SetMarginSettings(r.Context(), principal(r), terms, *body.ExpectedVersion, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"approval": approvalJSON(a)})
}

func (h *Handler) marginAccounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw, err := h.Svc.MarginAccounts(r.Context(), principal(r), application.MarginAccountQuery{
		Status: q.Get("status"), Account: q.Get("account"), Symbol: q.Get("symbol"), UserID: q.Get("user_id"), Limit: intParam(q, "limit"),
		Kinds: kindsParam(q),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) marginAccount(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.MarginAccount(r.Context(), principal(r), chi.URLParam(r, "user_id"), chi.URLParam(r, "account"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) freezeMarginAccount(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.FreezeMarginAccount(r.Context(), principal(r), chi.URLParam(r, "user_id"), chi.URLParam(r, "account"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) unfreezeMarginAccount(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.UnfreezeMarginAccount(r.Context(), principal(r), chi.URLParam(r, "user_id"), chi.URLParam(r, "account"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

// liquidateMarginAccount answers 202 with the request (or, repeated with
// its key, the request as it stands).
func (h *Handler) liquidateMarginAccount(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.LiquidateMarginAccount(r.Context(), principal(r), chi.URLParam(r, "user_id"), chi.URLParam(r, "account"), body.Reason,
		idemKey(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"approval": approvalJSON(a)})
}

func (h *Handler) marginLiquidations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, next, err := h.Svc.MarginLiquidations(r.Context(), principal(r), application.MarginLiquidationQuery{
		Days: intParam(q, "days"), Account: q.Get("account"), Symbol: q.Get("symbol"), Trigger: q.Get("trigger"), UserID: q.Get("user_id"),
		Cursor: q.Get("cursor"), Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writePage(w, list, next)
}

func (h *Handler) marginInterest(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.MarginInterest(r.Context(), principal(r), reportQuery(r), r.URL.Query().Get("asset"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}
