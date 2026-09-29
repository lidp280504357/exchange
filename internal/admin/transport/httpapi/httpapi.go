// Package httpapi serves the admin console API under /admin/v1
// (api/admin/admin.yaml). The session travels in an HttpOnly, SameSite
// Strict cookie scoped to /admin/; every write must also carry the header
// X-Admin-CSRF, which a cross-site form cannot set.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/application"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
)

// Cookie and header names.
const (
	CookieName = "admin_session"
	CSRFHeader = "X-Admin-CSRF"
)

// RuleLogin throttles login attempts per client IP.
var RuleLogin = ratelimit.Rule{Name: "admin_login", Limit: 10, Window: time.Minute}

// Handler serves the console.
type Handler struct {
	Svc *application.Service
	// Limiter throttles logins; nil leaves them unthrottled (tests).
	Limiter *ratelimit.Limiter
	// Secure marks the cookie Secure (off only for plain-HTTP development).
	Secure bool
}

type ctxKey struct{}

func principal(r *http.Request) application.Principal {
	p, _ := r.Context().Value(ctxKey{}).(application.Principal)
	return p
}

// Routes mounts the API on r.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/admin/v1", func(r chi.Router) {
		r.Use(h.csrf)
		r.Post("/login", h.login)
		r.Group(func(r chi.Router) {
			r.Use(h.authenticate)
			r.Post("/logout", h.logout)
			r.Get("/me", h.me)
			r.Get("/users/lookup", h.lookup)
			r.Post("/users/{id}/status", h.userStatus)
			r.Post("/users/{id}/cancel-orders", h.cancelOrders)
			r.Get("/withdrawals", h.withdrawals)
			r.Post("/withdrawals/{id}/review", h.review)
			r.Get("/instruments", h.instruments)
			r.Post("/instruments/pairs/{symbol}/status", h.pairStatus)
			r.Get("/flags", h.flags)
			r.Put("/flags/{key}", h.switchFlag)
			r.Post("/ledger/adjustments", h.requestAdjustment)
			r.Get("/approvals", h.approvals)
			r.Post("/approvals/{id}/decide", h.decide)
			r.Get("/audit-logs", h.auditLogs)
			r.Get("/reports/trading", h.tradingReport)
			r.Get("/reports/wallet", h.walletReport)
			r.Get("/reports/candles", h.candleReport)
			r.Get("/reports/derivatives", h.derivativesReport)
			r.Get("/reports/open-interest", h.openInterest)
			r.Get("/derivatives/contracts", h.derivativesContracts)
			r.Post("/derivatives/contracts/{symbol}/status", h.contractStatus)
			r.Post("/derivatives/contracts/{symbol}/lift-reduce-only", h.liftReduceOnly)
			r.Get("/derivatives/risk", h.derivativesRisk)
			r.Get("/derivatives/liquidations", h.liquidations)
			r.Get("/derivatives/insurance-fund", h.insuranceFund)
			r.Post("/derivatives/insurance-fund/contributions", h.requestInsuranceFunding)
		})
	})
}

func (h *Handler) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(CSRFHeader) != "1" {
			httpx.WriteError(w, r, apperr.New(apperr.KindForbidden, "ADMIN_CSRF", "the "+CSRFHeader+" header is required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		token := ""
		if err == nil {
			token = c.Value
		}
		p, err := h.Svc.Authenticate(r.Context(), token)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func clientIP(r *http.Request) string {
	if ip := httpx.ClientIPFrom(r.Context()); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// AdminJSON is the signed-in administrator.
type AdminJSON struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

func adminJSON(a domain.Admin) AdminJSON {
	return AdminJSON{ID: a.ID, Email: a.Email, Name: a.Name, Role: a.Role, Permissions: domain.Permissions(a.Role)}
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	ip := clientIP(r)
	if h.Limiter != nil {
		res, err := h.Limiter.Allow(r.Context(), ratelimit.Check{Rule: RuleLogin, Key: ip})
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if !res.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter.Seconds())+1))
			httpx.WriteError(w, r, apperr.New(apperr.KindRateLimited, apperr.CodeRateLimited, "too many login attempts"))
			return
		}
	}
	token, session, admin, err := h.Svc.Login(r.Context(), body.Email, body.Password, body.TOTPCode, ip, r.UserAgent())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is off only for plain-HTTP local development
		Name: CookieName, Value: token, Path: "/admin/", HttpOnly: true, Secure: h.Secure, SameSite: http.SameSiteStrictMode,
		Expires: session.ExpiresAt,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"admin": adminJSON(admin), "expires_at": httpx.FormatTime(session.ExpiresAt)})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Logout(r.Context(), principal(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is off only for plain-HTTP local development
		Name: CookieName, Value: "", Path: "/admin/", HttpOnly: true, Secure: h.Secure, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, adminJSON(principal(r).Admin))
}

func (h *Handler) lookup(w http.ResponseWriter, r *http.Request) {
	v, err := h.Svc.FindUser(r.Context(), principal(r), r.URL.Query().Get("q"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	balances := make([]map[string]string, 0, len(v.Balances))
	for _, b := range v.Balances {
		balances = append(balances, map[string]string{"account_type": b.AccountType, "asset": b.Asset, "available": b.Available, "frozen": b.Frozen})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id": v.User.ID, "status": v.User.Status, "region": v.User.Region, "language": v.User.Language, "kyc_level": v.User.KYCLevel,
			"created_at": httpx.FormatTime(v.User.CreatedAt),
		},
		"balances": balances,
	})
}

type reasonBody struct {
	Reason string `json:"reason"`
}

func (h *Handler) userStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To     string `json:"to"`
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	from, err := h.Svc.ChangeUserStatus(r.Context(), principal(r), chi.URLParam(r, "id"), body.To, body.Reason, body.Note)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"from": from, "to": strings.ToUpper(body.To)})
}

func (h *Handler) cancelOrders(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.Svc.CancelOrders(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// writeRaw passes a backend's JSON through (re-encoded, so it is checked).
func writeRaw(w http.ResponseWriter, raw []byte) {
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(raw))
}

func (h *Handler) withdrawals(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.Withdrawals(r.Context(), principal(r), r.URL.Query().Get("status"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) review(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.ReviewWithdrawal(r.Context(), principal(r), chi.URLParam(r, "id"), body.Approve, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) instruments(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.Instruments(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) pairStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To     string `json:"to"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	from, err := h.Svc.SetPairStatus(r.Context(), principal(r), chi.URLParam(r, "symbol"), strings.ToUpper(body.To), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"from": from, "to": strings.ToUpper(body.To)})
}

func (h *Handler) flags(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.FlagList(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) switchFlag(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool  `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.Enabled == nil {
		httpx.WriteError(w, r, apperr.Invalid("enabled is required"))
		return
	}
	f, err := h.Svc.SwitchFlag(r.Context(), principal(r), chi.URLParam(r, "key"), *body.Enabled, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}

// ApprovalJSON is a two-person request.
type ApprovalJSON struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Payload     map[string]string `json:"payload"`
	Reason      string            `json:"reason"`
	Status      string            `json:"status"`
	RequestedBy string            `json:"requested_by"`
	DecidedBy   *string           `json:"decided_by"`
	Result      string            `json:"result"`
	CreatedAt   string            `json:"created_at"`
	DecidedAt   *string           `json:"decided_at"`
}

func approvalJSON(a domain.Approval) ApprovalJSON {
	out := ApprovalJSON{
		ID: a.ID, Kind: a.Kind, Payload: a.Payload, Reason: a.Reason, Status: a.Status, RequestedBy: a.RequestedBy, Result: a.Result,
		CreatedAt: httpx.FormatTime(a.CreatedAt),
	}
	if a.DecidedBy != "" {
		out.DecidedBy = &a.DecidedBy
	}
	if !a.DecidedAt.IsZero() {
		s := httpx.FormatTime(a.DecidedAt)
		out.DecidedAt = &s
	}
	return out
}

func (h *Handler) requestAdjustment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
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
	a, err := h.Svc.RequestAdjustment(r.Context(), principal(r), application.Adjustment{
		UserID: body.UserID, Asset: body.Asset, Amount: amount, Reason: body.Reason,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, approvalJSON(a))
}

func (h *Handler) approvals(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Approvals(r.Context(), principal(r), r.URL.Query().Get("status"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]ApprovalJSON, 0, len(list))
	for _, a := range list {
		out = append(out, approvalJSON(a))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := h.Svc.DecideApproval(r.Context(), principal(r), chi.URLParam(r, "id"), body.Approve, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, approvalJSON(a))
}

func (h *Handler) auditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := h.Svc.AuditLogs(r.Context(), principal(r), q.Get("actor"), q.Get("target"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) tradingReport(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	list, err := h.Svc.TradingReport(r.Context(), principal(r), days)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) walletReport(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	list, err := h.Svc.WalletReport(r.Context(), principal(r), days)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) candleReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	interval := q.Get("interval")
	if interval == "" {
		interval = "1h"
	}
	list, err := h.Svc.CandleReport(r.Context(), principal(r), q.Get("symbol"), interval, limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) derivativesReport(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	list, err := h.Svc.DerivativesReport(r.Context(), principal(r), days)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) openInterest(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.OpenInterest(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) derivativesContracts(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.DerivativesContracts(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) contractStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To     string `json:"to"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	from, err := h.Svc.SetContractStatus(r.Context(), principal(r), chi.URLParam(r, "symbol"), strings.ToUpper(body.To), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"from": from, "to": strings.ToUpper(body.To)})
}

func (h *Handler) liftReduceOnly(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.LiftReduceOnly(r.Context(), principal(r), chi.URLParam(r, "symbol"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) derivativesRisk(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.DerivativesRisk(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) liquidations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := h.Svc.Liquidations(r.Context(), principal(r), days, q.Get("kind"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) insuranceFund(w http.ResponseWriter, r *http.Request) {
	fund, err := h.Svc.InsuranceFund(r.Context(), principal(r), r.URL.Query().Get("asset"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fund)
}

func (h *Handler) requestInsuranceFunding(w http.ResponseWriter, r *http.Request) {
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
	a, err := h.Svc.RequestInsuranceFunding(r.Context(), principal(r), body.Asset, amount, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, approvalJSON(a))
}
