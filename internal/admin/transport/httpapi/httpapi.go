// Package httpapi serves the admin console API under /admin/v1
// (api/admin/admin.yaml). The session travels in an HttpOnly, SameSite
// Strict cookie scoped to /admin/; every write must also carry the header
// X-Admin-CSRF, which a cross-site form cannot set.
package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/idempotency"
	"github.com/skill/exchange/internal/platform/ratelimit"
)

// Cookie and header names.
const (
	CookieName = "admin_session"
	CSRFHeader = "X-Admin-CSRF"
	// KeyHeader carries the Idempotency-Key of a request that moves money.
	KeyHeader = "Idempotency-Key"
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
	// EventsEvery is how often the event stream checks the counts (10
	// seconds when zero).
	EventsEvery time.Duration
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
		r.Get("/login-options", h.loginOptions)
		r.Post("/login", h.login)
		r.Post("/setup/inspect", h.inspectSetup)
		r.Post("/setup", h.completeSetup)
		r.Group(func(r chi.Router) {
			r.Use(h.authenticate, mustChange)
			r.Post("/logout", h.logout)
			r.Get("/me", h.me)
			r.Post("/me/password", h.changeOwnPassword)
			r.Post("/me/totp/start", h.startOwnTOTP)
			r.Post("/me/totp", h.confirmOwnTOTP)
			r.Get("/settings", h.settings)
			r.Put("/settings", h.updateSettings)
			r.Get("/todo", h.todo)
			r.Get("/events", h.events)
			r.Get("/users", h.users)
			r.Get("/users/lookup", h.lookup)
			r.Get("/users/{id}", h.userDetail)
			r.Get("/users/{id}/notes", h.notes)
			r.Post("/users/{id}/notes", h.addNote)
			r.Put("/users/{id}/tags", h.setTags)
			r.Get("/users/{id}/security", h.userSecurity)
			r.Post("/users/{id}/contacts/reveal", h.revealContacts)
			r.Get("/users/{id}/login-history", h.loginHistory)
			r.Post("/users/{id}/sessions/revoke", h.revokeSessions)
			r.Post("/users/{id}/totp-reset", h.resetTOTP)
			r.Post("/users/{id}/password-reset", h.passwordReset)
			r.Get("/users/{id}/history", h.userHistory)
			r.Get("/users/{id}/risk", h.userRisk)
			r.Get("/identity-requests", h.identityRequests)
			r.Post("/identity-requests/{id}/decide", h.decideIdentityRequest)
			r.Get("/users/{id}/balances", h.balances)
			r.Get("/users/{id}/holds", h.holds)
			r.With(needKey).Post("/users/{id}/holds", h.placeHold)
			r.With(needKey).Delete("/users/{id}/holds/{hold}", h.releaseHold)
			r.Post("/users/{id}/orders/{order}/cancel", h.cancelOrder)
			r.Get("/users/{id}/contract-orders", h.contractOrders)
			r.Post("/users/{id}/contract-orders/{order}/cancel", h.cancelContractOrder)
			r.Get("/users/{id}/positions", h.userPositions)
			r.Get("/users/{id}/futures-margin", h.futuresMargin)
			r.With(needKey).Post("/users/{id}/positions/close", h.closePosition)
			r.Get("/roles", h.roles)
			r.Get("/admins", h.admins)
			r.Post("/admins", h.createAdmin)
			r.Post("/admins/{id}/status", h.adminStatus)
			r.Post("/admins/{id}/role", h.adminRole)
			r.Post("/admins/{id}/password-reset", h.adminPasswordReset)
			r.Post("/admins/{id}/totp-reset", h.adminTOTPReset)
			r.Get("/admins/{id}/sessions", h.adminSessions)
			r.Post("/admins/{id}/sessions/revoke", h.revokeAdminSessions)
			r.Get("/withdrawals/suspensions", h.withdrawalSuspensions)
			r.Post("/withdrawals/suspensions/{asset}/resume", h.resumeWithdrawals)
			r.Get("/withdrawals/{id}", h.withdrawalDetail)
			r.Post("/withdrawals/{id}/hold", h.holdWithdrawal)
			r.Get("/deposits/review", h.depositReviews)
			r.Post("/deposits/manual/check", h.checkBackfill)
			r.With(needKey).Post("/deposits/manual", h.backfill)
			r.Get("/deposits/{id}", h.depositDetail)
			r.With(needKey).Post("/deposits/{id}/credit", h.creditDeposit)
			r.With(needKey).Post("/deposits/{id}/assign", h.assignDeposit)
			r.Post("/deposits/{id}/reject", h.rejectDeposit)
			r.With(needKey).Post("/users/{id}/adjustments", h.userAdjustment)
			r.Get("/orders", h.orders)
			r.Get("/trades", h.trades)
			r.Get("/deposits", h.deposits)
			r.Get("/dashboard", h.dashboard)
			r.Post("/users/{id}/status", h.userStatus)
			r.Post("/users/{id}/cancel-orders", h.cancelOrders)
			r.Get("/withdrawals", h.withdrawals)
			r.With(needKey).Post("/withdrawals/{id}/review", h.review)
			r.With(needKey).Post("/withdrawals/review-batch", h.reviewBatch)
			r.Get("/custody", h.custody)
			r.Get("/custody/callbacks", h.custodyCallbacks)
			r.Get("/custody/callbacks/{id}", h.custodyCallback)
			r.Post("/custody/callbacks/{id}/replay", h.replayCallback)
			r.Get("/custody/fees", h.custodyFees)
			r.Post("/custody/fees/{withdrawal}/book", h.bookCustodyFee)
			r.Post("/custody/fees/{withdrawal}/write-off", h.writeOffCustodyFee)
			r.Get("/instruments", h.instruments)
			r.Get("/instruments/config", h.instrumentConfig)
			r.Post("/instruments/preview", h.previewConfig)
			r.Post("/instruments/apply", h.applyConfig)
			r.Post("/instruments/pairs/{symbol}/status/preview", h.previewStatus(domain.ChangePairStatus))
			r.Post("/instruments/pairs/{symbol}/status", h.setStatus(domain.ChangePairStatus))
			r.Get("/articles", h.articles)
			r.Post("/articles", h.createArticle)
			r.Get("/articles/{id}", h.article)
			r.Put("/articles/{id}", h.updateArticle)
			r.Post("/articles/{id}/publish", h.publishArticle)
			r.Post("/articles/{id}/archive", h.archiveArticle)
			r.Get("/broadcasts", h.broadcasts)
			r.With(needKey).Post("/broadcasts", h.sendBroadcast)
			r.Get("/broadcasts/{id}", h.broadcast)
			r.Post("/broadcasts/{id}/resume", h.resumeBroadcast)
			r.Get("/sim", h.simStatus)
			r.Get("/sim/history", h.simHistory)
			r.Get("/sim/events", h.simEvents)
			r.Post("/sim/events", h.createSimEvent)
			r.Post("/sim/events/{id}/end", h.endSimEvent)
			r.Get("/sim/events/{id}/plan", h.simPlan)
			r.Get("/sim/target-preview", h.simTargetPreview)
			r.Put("/sim/params", h.updateSimParams)
			r.Post("/sim/impact", h.simImpact)
			r.Get("/sim/token", h.simToken)
			r.With(needKey).Post("/sim/mint", h.simMint)
			r.Get("/approvals/{id}/sim-preview", h.simPreview)
			r.Get("/platform/profile", h.platformProfile)
			r.Put("/platform/profile", h.updatePlatformProfile)
			r.Put("/platform/images/{kind}", h.putPlatformImage)
			r.Delete("/platform/images/{kind}", h.deletePlatformImage)
			r.Get("/platform/welcome-credits", h.welcomeCredits)
			r.Put("/platform/welcome-credits", h.setWelcomeCredits)
			r.Get("/launch-checklist", h.launchChecklist)
			r.Get("/assets/{code}/profile", h.assetProfile)
			r.Put("/assets/{code}/profile", h.updateAssetProfile)
			r.Get("/instruments/changes", h.instrumentChanges)
			r.Post("/instruments/changes/{id}/decide", h.decideInstrumentChange)
			r.Post("/instruments/changes/{id}/cancel", h.cancelInstrumentChange)
			r.Get("/flags", h.flags)
			r.Put("/flags/{key}", h.switchFlag)
			r.With(needKey).Post("/ledger/adjustments", h.requestAdjustment)
			r.Get("/approvals", h.approvals)
			r.Post("/approvals/{id}/decide", h.decide)
			r.Get("/audit-logs", h.auditLogs)
			r.Get("/audit-logs/export", h.exportAuditLogs)
			r.Get("/reports/trading", h.tradingReport)
			r.Get("/reports/wallet", h.walletReport)
			r.Get("/reports/candles", h.candleReport)
			r.Get("/reports/derivatives", h.derivativesReport)
			r.Get("/reports/open-interest", h.openInterest)
			r.Get("/reports/users", h.usersReport)
			r.Get("/reports/house-pnl", h.housePnLReport)
			r.Get("/derivatives/contracts", h.derivativesContracts)
			r.Post("/derivatives/contracts/{symbol}/status/preview", h.previewStatus(domain.ChangeContractStatus))
			r.Post("/derivatives/contracts/{symbol}/status", h.setStatus(domain.ChangeContractStatus))
			r.Post("/derivatives/contracts/{symbol}/lift-reduce-only", h.liftReduceOnly)
			r.Get("/derivatives/risk", h.derivativesRisk)
			r.Get("/derivatives/liquidations", h.liquidations)
			r.Get("/positions", h.positions)
			r.Get("/derivatives/insurance-fund", h.insuranceFund)
			r.Get("/derivatives/insurance-funds", h.insuranceFunds)
			r.Get("/house", h.house)
			r.Get("/health", h.health)
			r.Get("/ledger/reconciliation", h.reconciliation)
			r.Get("/ledger/system-balances", h.systemBalances)
			r.With(needKey).Post("/derivatives/insurance-fund/contributions", h.requestInsuranceFunding)
			h.marginRoutes(r)
		})
	})
}

// needKey requires an Idempotency-Key on a request that moves money
// (C5.5 ⑥): the console makes one per operation and sends it again with
// each retry, so a retry never does it twice.
func needKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if k := idemKey(r); k == "" || len(k) > idempotency.MaxKeyLength {
			httpx.WriteError(w, r, idempotency.ErrInvalidKey)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// idemKey is a request's Idempotency-Key.
func idemKey(r *http.Request) string { return strings.TrimSpace(r.Header.Get(KeyHeader)) }

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
	// MustChangePassword: the password was generated for them; nothing
	// else is open until they change it (C5.5 ⑪).
	MustChangePassword bool `json:"must_change_password"`
}

func adminJSON(a domain.Admin) AdminJSON {
	return AdminJSON{
		ID: a.ID, Email: a.Email, Name: a.Name, Role: a.Role, Permissions: domain.Permissions(a.Role), MustChangePassword: a.MustChangePassword,
	}
}

// loginOptions tells the sign-in page whether to ask for the
// authenticator code (flag admin.login_without_totp).
func (h *Handler) loginOptions(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"totp_required": h.Svc.TOTPRequired()})
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
	q := r.URL.Query()
	raw, err := h.Svc.Withdrawals(r.Context(), principal(r), ports.WithdrawalQuery{
		Status: q.Get("status"), UserID: q.Get("user_id"), Asset: q.Get("asset"), Network: q.Get("network"), Cursor: q.Get("cursor"),
		Limit: intParam(q, "limit"), Order: q.Get("order"), Held: q.Get("held"), MinValue: q.Get("min_value_usdt"),
		MaxValue: q.Get("max_value_usdt"), MinRisk: intParam(q, "min_risk"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func intParam(q url.Values, name string) int {
	n, _ := strconv.Atoi(q.Get(name))
	return n
}

// timeParams reads the RFC 3339 times from and to; empty ones are zero.
func timeParams(q url.Values) (time.Time, time.Time, error) {
	var out [2]time.Time
	for i, name := range []string{"from", "to"} {
		v := q.Get(name)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, apperr.Invalid(name + " must be an RFC 3339 time")
		}
		out[i] = t
	}
	return out[0], out[1], nil
}

// writePage answers a page of items with the cursor of the next.
func writePage(w http.ResponseWriter, items any, next string) {
	var cursor *string
	if next != "" {
		cursor = &next
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": cursor})
}

func userJSON(u ports.User) map[string]any {
	tags := u.Tags
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"id": u.ID, "status": u.Status, "region": u.Region, "language": u.Language, "timezone": u.Timezone, "kyc_level": u.KYCLevel,
		"created_at": httpx.FormatTime(u.CreatedAt), "tags": tags,
	}
}

func (h *Handler) userDetail(w http.ResponseWriter, r *http.Request) {
	u, err := h.Svc.UserDetail(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, userJSON(u))
}

// NoteJSON is an administrator's note on an account.
type NoteJSON struct {
	ID         string `json:"id"`
	Body       string `json:"body"`
	AdminID    string `json:"admin_id"`
	AdminEmail string `json:"admin_email"`
	CreatedAt  string `json:"created_at"`
}

func noteJSON(n domain.Note) NoteJSON {
	return NoteJSON{ID: n.ID, Body: n.Body, AdminID: n.AdminID, AdminEmail: n.AdminEmail, CreatedAt: httpx.FormatTime(n.CreatedAt)}
}

func (h *Handler) notes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, next, err := h.Svc.Notes(r.Context(), principal(r), chi.URLParam(r, "id"), q.Get("cursor"), intParam(q, "limit"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]NoteJSON, 0, len(list))
	for _, n := range list {
		out = append(out, noteJSON(n))
	}
	writePage(w, out, next)
}

func (h *Handler) addNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	n, err := h.Svc.AddNote(r.Context(), principal(r), chi.URLParam(r, "id"), body.Body)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, noteJSON(n))
}

func (h *Handler) setTags(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	tags, err := h.Svc.SetTags(r.Context(), principal(r), chi.URLParam(r, "id"), body.Tags)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (h *Handler) users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := timeParams(q)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	list, next, err := h.Svc.ListUsers(r.Context(), principal(r), ports.UserQuery{
		Status: q.Get("status"), Region: q.Get("region"), CreatedFrom: from, CreatedBefore: to, Cursor: q.Get("cursor"),
		Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(list))
	for _, u := range list {
		items = append(items, userJSON(u))
	}
	writePage(w, items, next)
}

func (h *Handler) orders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := timeParams(q)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	list, next, err := h.Svc.OrderList(r.Context(), principal(r), ports.OrderQuery{
		UserID: q.Get("user_id"), OrderID: q.Get("order_id"), Symbol: q.Get("symbol"), Status: q.Get("status"), Side: q.Get("side"),
		From: from, To: to, Accounts: q.Get("accounts"), Cursor: q.Get("cursor"), Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writePage(w, list, next)
}

func (h *Handler) trades(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := timeParams(q)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	list, next, err := h.Svc.TradeList(r.Context(), principal(r), ports.TradeQuery{
		Symbol: q.Get("symbol"), UserID: q.Get("user_id"), From: from, To: to, Accounts: q.Get("accounts"), Cursor: q.Get("cursor"),
		Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writePage(w, list, next)
}

func (h *Handler) deposits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, next, err := h.Svc.DepositList(r.Context(), principal(r), ports.DepositQuery{
		UserID: q.Get("user_id"), Asset: q.Get("asset"), Network: q.Get("network"), Status: q.Get("status"), TxHash: q.Get("tx_hash"),
		Cursor: q.Get("cursor"), Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writePage(w, list, next)
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := h.Svc.Dashboard(r.Context(), principal(r), intParam(r.URL.Query(), "days"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	series := make([]map[string]any, 0, len(d.Days))
	for _, day := range d.Days {
		turnover := d.Activity.TurnoverUSDTByDay[day]
		if turnover == "" {
			turnover = "0"
		}
		series = append(series, map[string]any{
			"day": day, "new_users": d.Users.Days[day], "trades": d.Activity.TradesByDay[day], "turnover_usdt": turnover,
		})
	}
	turnover := d.Activity.Turnover24h
	if turnover == nil {
		turnover = []ports.Turnover{}
	}
	partial := d.Partial
	if partial == nil {
		partial = []string{}
	}
	var feed any
	if d.Feed != nil {
		halted := d.Feed.Halted
		if len(halted) == 0 {
			halted = json.RawMessage("[]")
		}
		feed = map[string]any{"state": d.Feed.State, "received_at": d.Feed.ReceivedAt, "followed": d.Feed.Followed, "halted": halted}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"users": map[string]any{"total": d.Users.Total, "new_24h": d.Users.CreatedSince},
		"trading": map[string]any{
			"trades_24h": d.Activity.Trades24h, "active_traders_24h": d.Activity.ActiveTraders24h, "turnover_24h": turnover,
		},
		"wallet":  map[string]any{"pending_deposits": d.Activity.PendingDeposits, "pending_withdrawals": d.Activity.PendingWithdraws},
		"risk":    map[string]any{"events_24h": d.Activity.RiskEvents24h},
		"feed":    feed,
		"series":  series,
		"partial": partial,
	})
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
	raw, err := h.Svc.ReviewWithdrawal(r.Context(), principal(r), idemKey(r), chi.URLParam(r, "id"), body.Approve, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) reviewBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs     []string `json:"ids"`
		Approve bool     `json:"approve"`
		Reason  string   `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	results, err := h.Svc.ReviewBatch(r.Context(), principal(r), idemKey(r), body.IDs, body.Approve, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *Handler) custody(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.Custody(r.Context(), principal(r), r.URL.Query().Get("provider"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) custodyCallbacks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw, err := h.Svc.CustodyCallbacks(r.Context(), principal(r), ports.CallbackQuery{
		Provider: q.Get("provider"), Result: q.Get("result"), Kind: q.Get("kind"), Query: q.Get("q"), Cursor: q.Get("cursor"),
		Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) custodyCallback(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.CustodyCallback(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

func (h *Handler) replayCallback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := h.Svc.ReplayCallback(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
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

// instrumentConfig returns the reference data as a config document.
func (h *Handler) instrumentConfig(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Svc.InstrumentConfig(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeRaw(w, raw)
}

type configBody struct {
	Config json.RawMessage `json:"config"`
	Reason string          `json:"reason"`
	// Confirmation is the preview's, for a document moving trading
	// parameters.
	Confirmation string `json:"confirmation"`
}

// previewConfig works out what a config document would change.
func (h *Handler) previewConfig(w http.ResponseWriter, r *http.Request) {
	var body configBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.Svc.PreviewConfig(r.Context(), principal(r), body.Config)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
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

// ApprovalJSON is a fund operation: a two-person request or a
// single-person operation.
type ApprovalJSON struct {
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	Payload          map[string]string `json:"payload"`
	Reason           string            `json:"reason"`
	Status           string            `json:"status"`
	RequestedBy      string            `json:"requested_by"`
	RequestedByEmail string            `json:"requested_by_email"`
	DecidedBy        *string           `json:"decided_by"`
	DecidedByEmail   *string           `json:"decided_by_email"`
	Result           string            `json:"result"`
	CreatedAt        string            `json:"created_at"`
	DecidedAt        *string           `json:"decided_at"`
	Mode             string            `json:"mode"`
	ValueUSDT        *string           `json:"value_usdt"`
	Escalation       string            `json:"escalation"`
	JournalID        *string           `json:"journal_id"`
	// AttemptedAt is when an attempt to carry it out began: pending with
	// it, it may have booked (finish it, never reject it).
	AttemptedAt *string `json:"attempted_at"`
	// Expired: a simulated market's pending request past its expiry by
	// the server's clock, in a list (review ⑭).
	Expired bool `json:"expired"`
}

func approvalJSON(a domain.Approval) ApprovalJSON {
	out := ApprovalJSON{
		ID: a.ID, Kind: a.Kind, Payload: a.Payload, Reason: a.Reason, Status: a.Status, RequestedBy: a.RequestedBy,
		RequestedByEmail: a.RequestedByEmail, Result: a.Result, CreatedAt: httpx.FormatTime(a.CreatedAt), Mode: a.Mode,
		Escalation: a.Escalation, Expired: a.Lapsed,
	}
	if out.Mode == "" {
		out.Mode = domain.ModeTwoPerson
	}
	if a.DecidedBy != "" {
		out.DecidedBy = &a.DecidedBy
	}
	if a.DecidedByEmail != "" {
		out.DecidedByEmail = &a.DecidedByEmail
	}
	if !a.DecidedAt.IsZero() {
		s := httpx.FormatTime(a.DecidedAt)
		out.DecidedAt = &s
	}
	if a.ValueUSDT != nil {
		v := a.ValueUSDT.String()
		out.ValueUSDT = &v
	}
	if a.JournalID != "" {
		out.JournalID = &a.JournalID
	}
	if !a.AttemptedAt.IsZero() {
		s := httpx.FormatTime(a.AttemptedAt)
		out.AttemptedAt = &s
	}
	return out
}

// fundBody is the body of a fund operation.
type fundBody struct {
	UserID      string `json:"user_id"`
	AccountType string `json:"account_type"`
	Asset       string `json:"asset"`
	Amount      string `json:"amount"`
	Reason      string `json:"reason"`
	Reference   string `json:"reference"`
	Direct      bool   `json:"direct"`
}

// submitFunds answers a fund operation: 201 with it, whatever came of it.
func (h *Handler) submitFunds(w http.ResponseWriter, r *http.Request, kind string, body fundBody) {
	amount, err := decimal.NewFromString(body.Amount)
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("amount must be a decimal string"))
		return
	}
	a, err := h.Svc.SubmitFunds(r.Context(), principal(r), application.FundRequest{
		Kind: kind, UserID: body.UserID, AccountType: body.AccountType, Asset: body.Asset, Amount: amount, Reason: body.Reason,
		Reference: body.Reference, Direct: body.Direct, Key: idemKey(r),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, approvalJSON(a))
}

func (h *Handler) requestAdjustment(w http.ResponseWriter, r *http.Request) {
	var body fundBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.submitFunds(w, r, domain.KindLedgerAdjustment, body)
}

// userAdjustment adjusts a user's balance, at once in single-person mode
// within the limits.
func (h *Handler) userAdjustment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Asset       string `json:"asset"`
		Amount      string `json:"amount"`
		Reason      string `json:"reason"`
		Reference   string `json:"reference"`
		AccountType string `json:"account_type"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.submitFunds(w, r, domain.KindLedgerAdjustment, fundBody{
		UserID: chi.URLParam(r, "id"), AccountType: body.AccountType, Asset: body.Asset, Amount: body.Amount, Reason: body.Reason,
		Reference: body.Reference, Direct: true,
	})
}

// SettingsJSON is the console's settings with the caller's single-person
// total of the last 24 hours.
type SettingsJSON struct {
	TwoPerson     bool    `json:"two_person_approval"`
	SingleMax     string  `json:"single_max_usdt"`
	DailyMax      string  `json:"daily_max_usdt"`
	WithdrawalMax string  `json:"withdrawal_max_usdt"`
	ChangeDelay   int64   `json:"change_delay_seconds"`
	DelayFloor    int64   `json:"change_delay_floor_seconds"`
	DailyUsed     string  `json:"daily_used_usdt"`
	UpdatedBy     string  `json:"updated_by"`
	UpdatedAt     *string `json:"updated_at"`
}

func settingsJSON(v application.SettingsView) SettingsJSON {
	out := SettingsJSON{
		TwoPerson: v.TwoPerson, SingleMax: v.SingleMax.String(), DailyMax: v.DailyMax.String(), WithdrawalMax: v.WithdrawalMax.String(),
		ChangeDelay: int64(v.ChangeDelay / time.Second), DelayFloor: int64(v.DelayFloor / time.Second), DailyUsed: v.Used.String(),
		UpdatedBy: v.UpdatedBy,
	}
	if !v.UpdatedAt.IsZero() {
		s := httpx.FormatTime(v.UpdatedAt)
		out.UpdatedAt = &s
	}
	return out
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	v, err := h.Svc.Settings(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settingsJSON(v))
}

func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TwoPerson     *bool   `json:"two_person_approval"`
		SingleMax     *string `json:"single_max_usdt"`
		DailyMax      *string `json:"daily_max_usdt"`
		WithdrawalMax *string `json:"withdrawal_max_usdt"`
		ChangeDelay   *int64  `json:"change_delay_seconds"`
		Reason        string  `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	patch := application.SettingsPatch{TwoPerson: body.TwoPerson, Reason: body.Reason}
	if body.ChangeDelay != nil {
		d := time.Duration(*body.ChangeDelay) * time.Second
		patch.ChangeDelay = &d
	}
	for _, f := range []struct {
		in   *string
		out  **decimal.Decimal
		name string
	}{
		{body.SingleMax, &patch.SingleMax, "single_max_usdt"},
		{body.DailyMax, &patch.DailyMax, "daily_max_usdt"},
		{body.WithdrawalMax, &patch.WithdrawalMax, "withdrawal_max_usdt"},
	} {
		if f.in == nil {
			continue
		}
		d, err := decimal.NewFromString(*f.in)
		if err != nil {
			httpx.WriteError(w, r, apperr.Invalid(f.name+" must be a decimal string"))
			return
		}
		*f.out = &d
	}
	v, err := h.Svc.UpdateSettings(r.Context(), principal(r), patch)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settingsJSON(v))
}

func (h *Handler) todo(w http.ResponseWriter, r *http.Request) {
	t, err := h.Svc.Todo(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) approvals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, next, err := h.Svc.Approvals(r.Context(), principal(r), q.Get("status"), q.Get("cursor"), intParam(q, "limit"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]ApprovalJSON, 0, len(list))
	for _, a := range list {
		out = append(out, approvalJSON(a))
	}
	writePage(w, out, next)
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
	from, to, err := timeParams(q)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	list, next, err := h.Svc.AuditLogs(r.Context(), principal(r), ports.AuditQuery{
		Actor: q.Get("actor"), Target: q.Get("target"), EventType: q.Get("event_type"), From: from, To: to, Cursor: q.Get("cursor"),
		Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writePage(w, list, next)
}

// exportAuditLogs writes the audit entries matching the filters as CSV
// (UTF-8 with a byte order mark, which spreadsheets need for Chinese), at
// most application.MaxAuditExport rows; X-Truncated says more were left
// out.
func (h *Handler) exportAuditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := timeParams(q)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	// The rows go out a page at a time (C5.5 ⑪); once they started, a
	// failure can only cut the file short, which the log says.
	out := csv.NewWriter(w)
	started, written := false, 0
	err = h.Svc.ExportAuditLogs(r.Context(), principal(r), ports.AuditQuery{
		Actor: q.Get("actor"), Target: q.Get("target"), EventType: q.Get("event_type"), From: from, To: to,
	}, func(cut bool) error {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit-`+time.Now().UTC().Format("20060102-150405")+`.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Truncated", strconv.FormatBool(cut))
		started = true
		_, _ = w.Write([]byte("\ufeff"))
		return out.Write([]string{"occurred_at", "event_type", "actor", "target", "action", "reason", "details", "event_id"})
	}, func(e application.AuditRow) error {
		written++
		if written%500 == 0 {
			out.Flush()
		}
		return out.Write([]string{
			httpx.FormatTime(e.OccurredAt), e.EventType, csvText(e.Actor), csvText(e.Target), csvText(e.Action), csvText(e.Reason),
			csvText(e.Details), e.EventID,
		})
	})
	out.Flush()
	if err != nil {
		if !started {
			httpx.WriteError(w, r, err)
			return
		}
		h.Svc.Log.WarnContext(r.Context(), "audit export cut short", "rows", written, "error", err)
	}
}

// csvText keeps a spreadsheet from reading typed text as a formula: a cell
// starting with = + - @ or a control character gets a leading quote.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// reportQuery reads a report's period: days, or from and to, and the bucket.
func reportQuery(r *http.Request) application.ReportQuery {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	return application.ReportQuery{Days: days, From: q.Get("from"), To: q.Get("to"), Bucket: q.Get("bucket")}
}

func (h *Handler) tradingReport(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.TradingReport(r.Context(), principal(r), reportQuery(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) walletReport(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.WalletReport(r.Context(), principal(r), reportQuery(r))
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
	list, err := h.Svc.DerivativesReport(r.Context(), principal(r), reportQuery(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) usersReport(w http.ResponseWriter, r *http.Request) {
	out, err := h.Svc.UsersReport(r.Context(), principal(r), reportQuery(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) housePnLReport(w http.ResponseWriter, r *http.Request) {
	out, err := h.Svc.HousePnLReport(r.Context(), principal(r), reportQuery(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
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
	list, next, err := h.Svc.Liquidations(r.Context(), principal(r), ports.LiquidationQuery{
		Days: intParam(q, "days"), Kind: q.Get("kind"), Symbol: q.Get("symbol"), UserID: q.Get("user_id"), Cursor: q.Get("cursor"),
		Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writePage(w, list, next)
}

// positions lists every user's open positions, riskiest first: symbol,
// user_id, watch=true for those under watch, limit (at most 500).
func (h *Handler) positions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Svc.OpenPositions(r.Context(), principal(r), ports.PositionQuery{
		Symbol: q.Get("symbol"), UserID: q.Get("user_id"), Watch: q.Get("watch") == "true", Limit: intParam(q, "limit"),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
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
		Asset     string `json:"asset"`
		Amount    string `json:"amount"`
		Reason    string `json:"reason"`
		Reference string `json:"reference"`
		Direct    bool   `json:"direct"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.submitFunds(w, r, domain.KindInsuranceFund, fundBody{
		Asset: body.Asset, Amount: body.Amount, Reason: body.Reason, Reference: body.Reference, Direct: body.Direct,
	})
}

func (h *Handler) house(w http.ResponseWriter, r *http.Request) {
	book, err := h.Svc.House(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, book)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	rep, err := h.Svc.Health(r.Context(), principal(r), r.URL.Query().Get("details") == "true")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handler) reconciliation(w http.ResponseWriter, r *http.Request) {
	rec, err := h.Svc.Reconciliation(r.Context(), principal(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

func (h *Handler) systemBalances(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.SystemBalances(r.Context(), principal(r), r.URL.Query().Get("asset"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]map[string]string, 0, len(list))
	for _, b := range list {
		out = append(out, map[string]string{"account_type": b.AccountType, "asset": b.Asset, "available": b.Available, "frozen": b.Frozen})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"balances": out})
}
