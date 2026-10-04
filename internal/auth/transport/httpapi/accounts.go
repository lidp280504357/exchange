package httpapi

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/auth/application"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/authtoken"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Cookie and headers of the token flows (§6.6).
const (
	refreshCookie    = "rt"
	refreshPath      = "/v1/auth/token/refresh"
	headerClientType = "X-Client-Type"
	headerStepUp     = "X-Step-Up-Token"
)

// Accounts serves the registration, login and session endpoints.
type Accounts struct {
	Svc *application.AccountService
	OTP *application.OTPService
	// JWKS publishes the access-token keys to the gateway.
	JWKS authtoken.JWKS
	// AllowedOrigins may refresh with the cookie (CSRF defense with SameSite).
	AllowedOrigins []string
	// SecureCookies is off only for plain-HTTP local development.
	SecureCookies bool
}

// Routes mounts the endpoints on r.
func (h *Accounts) Routes(r chi.Router) {
	r.Post("/v1/auth/register/complete", h.register)
	r.Post("/v1/auth/login/password", h.loginPassword)
	r.Post("/v1/auth/login/complete", h.loginOTP)
	r.Post("/v1/auth/login/challenge", h.loginChallenge)
	r.Post("/v1/auth/password/reset/request", h.resetRequest)
	r.Post("/v1/auth/password/reset/complete", h.resetComplete)
	r.Post("/v1/auth/token/refresh", h.refresh)
	r.Get("/v1/auth/terms", h.terms)

	r.Group(func(r chi.Router) {
		r.Use(requireUser)
		r.Post("/v1/auth/logout", h.logout)
		r.Post("/v1/auth/logout/all", h.logoutAll)
		r.Get("/v1/auth/sessions", h.sessions)
		r.Delete("/v1/auth/sessions/{session_id}", h.revokeSession)
		r.Get("/v1/auth/login-history", h.history)
		r.Post("/v1/auth/step-up", h.stepUp)
		r.Post("/v1/auth/password/change", h.changePassword)
		r.Post("/v1/auth/identity/bind", h.bind)
		r.Post("/v1/auth/identity/rebind", h.rebind)
		r.Get("/v1/auth/totp", h.totpStatus)
		r.Post("/v1/auth/totp/setup", h.totpSetup)
		r.Post("/v1/auth/totp/confirm", h.totpConfirm)
		r.Delete("/v1/auth/totp", h.totpDisable)
	})
	r.Get("/internal/jwks", func(w http.ResponseWriter, _ *http.Request) { httpx.WriteJSON(w, http.StatusOK, h.JWKS) })
}

// requireUser rejects requests the gateway did not authenticate.
func requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httpx.UserID(r) == "" || httpx.SessionID(r) == "" {
			httpx.WriteError(w, r, apperr.Unauthorized("sign in first"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func client(r *http.Request, deviceID string) application.Client {
	ct := domain.ClientWeb
	if strings.EqualFold(r.Header.Get(headerClientType), "app") {
		ct = domain.ClientApp
	}
	return application.Client{
		DeviceID: deviceID, UserAgent: r.UserAgent(), IP: httpx.ClientIPFrom(r.Context()), ClientType: ct,
	}
}

type tokenResponse struct {
	UserID           string `json:"user_id"`
	SessionID        string `json:"session_id"`
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	ExpiresAt        string `json:"expires_at"`
	Scope            string `json:"scope"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	RefreshExpiresAt string `json:"refresh_expires_at"`
}

// writeTokens returns the tokens: WEB clients get the refresh token in an
// HttpOnly cookie scoped to the refresh path, APP clients in the body.
func (h *Accounts) writeTokens(w http.ResponseWriter, c application.Client, t application.Tokens, status int) {
	resp := tokenResponse{
		UserID: t.UserID, SessionID: t.SessionID, TokenType: "Bearer", AccessToken: t.AccessToken,
		ExpiresIn: int(time.Until(t.AccessExpiresAt).Seconds()), ExpiresAt: httpx.FormatTime(t.AccessExpiresAt),
		Scope: t.Scope, RefreshExpiresAt: httpx.FormatTime(t.RefreshExpiresAt),
	}
	if c.ClientType == domain.ClientApp {
		resp.RefreshToken = t.RefreshToken
	} else {
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is off only for plain-HTTP local development
			Name: refreshCookie, Value: t.RefreshToken, Path: refreshPath, Expires: t.RefreshExpiresAt,
			MaxAge: int(time.Until(t.RefreshExpiresAt).Seconds()), HttpOnly: true, Secure: h.SecureCookies, SameSite: http.SameSiteStrictMode,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, status, resp)
}

func (h *Accounts) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is off only for plain-HTTP local development
		Name: refreshCookie, Value: "", Path: refreshPath, MaxAge: -1, HttpOnly: true, Secure: h.SecureCookies, SameSite: http.SameSiteStrictMode,
	})
}

func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var body T
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return body, false
	}
	return body, true
}

// terms returns the document versions registration must accept.
func (h *Accounts) terms(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"terms_version": h.Svc.Config.TermsVersion, "risk_disclosure_version": h.Svc.Config.RiskVersion,
	})
}

type registerBody struct {
	OTPTicket             string `json:"otp_ticket"`
	Password              string `json:"password"`
	Country               string `json:"country"`
	Language              string `json:"language"`
	Timezone              string `json:"timezone"`
	TermsVersion          string `json:"terms_version"`
	RiskDisclosureVersion string `json:"risk_disclosure_version"`
	DeviceID              string `json:"device_id"`
}

func (h *Accounts) register(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[registerBody](w, r)
	if !ok {
		return
	}
	c := client(r, body.DeviceID)
	t, err := h.Svc.Register(r.Context(), application.RegisterInput{
		Ticket: body.OTPTicket, Password: body.Password, Country: body.Country, Language: httpx.Language(r, body.Language),
		Timezone: body.Timezone, TermsVersion: body.TermsVersion, RiskVersion: body.RiskDisclosureVersion, Client: c,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.writeTokens(w, c, t, http.StatusCreated)
}

type passwordLoginBody struct {
	Identifier   string `json:"identifier"`
	Password     string `json:"password"`
	CaptchaToken string `json:"captcha_token"`
	DeviceID     string `json:"device_id"`
}

func (h *Accounts) loginPassword(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[passwordLoginBody](w, r)
	if !ok {
		return
	}
	c := client(r, body.DeviceID)
	res, err := h.Svc.LoginPassword(r.Context(), application.PasswordLogin{
		Identifier: body.Identifier, Password: body.Password, CaptchaToken: body.CaptchaToken, Client: c,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.writeTokens(w, c, res.Tokens, http.StatusOK)
}

type ticketBody struct {
	OTPTicket        string `json:"otp_ticket"`
	LoginChallengeID string `json:"login_challenge_id"`
	DeviceID         string `json:"device_id"`
}

func (h *Accounts) loginOTP(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[ticketBody](w, r)
	if !ok {
		return
	}
	c := client(r, body.DeviceID)
	t, err := h.Svc.LoginOTP(r.Context(), body.OTPTicket, c)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.writeTokens(w, c, t, http.StatusOK)
}

func (h *Accounts) loginChallenge(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[ticketBody](w, r)
	if !ok {
		return
	}
	c := client(r, body.DeviceID)
	t, err := h.Svc.CompleteLoginChallenge(r.Context(), body.LoginChallengeID, body.OTPTicket, c)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.writeTokens(w, c, t, http.StatusOK)
}

// resetRequest is otp/request with the PASSWORD_RESET scene.
func (h *Accounts) resetRequest(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[otpRequestBody](w, r)
	if !ok {
		return
	}
	view, err := h.OTP.Request(r.Context(), application.RequestOTP{
		Scene: string(domain.ScenePasswordReset), Channel: body.Channel, Identifier: body.Identifier,
		CaptchaToken: body.CaptchaToken, DeviceID: body.DeviceID, Language: httpx.Language(r, body.Language),
		IP: httpx.ClientIPFrom(r.Context()),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, challengeResponse{ChallengeID: view.ChallengeID, ExpiresAt: httpx.FormatTime(view.ExpiresAt), Delivery: view.Delivery})
}

type resetBody struct {
	OTPTicket   string `json:"otp_ticket"`
	NewPassword string `json:"new_password"`
	DeviceID    string `json:"device_id"`
}

func (h *Accounts) resetComplete(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[resetBody](w, r)
	if !ok {
		return
	}
	if err := h.Svc.ResetPassword(r.Context(), body.OTPTicket, body.NewPassword, client(r, body.DeviceID)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

type refreshBody struct {
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
}

// refresh accepts the cookie from browsers, which must also send an
// allowed Origin, or the body token from apps.
func (h *Accounts) refresh(w http.ResponseWriter, r *http.Request) {
	var body refreshBody
	if r.ContentLength != 0 {
		var ok bool
		if body, ok = decode[refreshBody](w, r); !ok {
			return
		}
	}
	c := client(r, body.DeviceID)
	token := body.RefreshToken
	if c.ClientType == domain.ClientWeb {
		if !slices.Contains(h.AllowedOrigins, r.Header.Get("Origin")) {
			httpx.WriteError(w, r, apperr.Forbidden("origin not allowed"))
			return
		}
		ck, err := r.Cookie(refreshCookie)
		if err != nil {
			httpx.WriteError(w, r, domain.ErrSessionRevoked)
			return
		}
		token = ck.Value
	}
	t, err := h.Svc.Refresh(r.Context(), token, c)
	if err != nil {
		if apperr.Is(err, "AUTH_SESSION_REVOKED") && c.ClientType == domain.ClientWeb {
			h.clearCookie(w)
		}
		httpx.WriteError(w, r, err)
		return
	}
	h.writeTokens(w, c, t, http.StatusOK)
}

func (h *Accounts) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Logout(r.Context(), httpx.UserID(r), httpx.SessionID(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Accounts) logoutAll(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.LogoutOthers(r.Context(), httpx.UserID(r), httpx.SessionID(r), r.Header.Get(headerStepUp)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sessionItem struct {
	SessionID  string `json:"session_id"`
	DeviceID   string `json:"device_id"`
	ClientType string `json:"client_type"`
	UserAgent  string `json:"user_agent"`
	IP         string `json:"ip"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	Current    bool   `json:"current"`
}

func (h *Accounts) sessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Sessions(r.Context(), httpx.UserID(r), httpx.SessionID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]sessionItem, 0, len(list))
	for _, s := range list {
		items = append(items, sessionItem{
			SessionID: s.ID, DeviceID: s.DeviceID, ClientType: s.ClientType, UserAgent: s.UserAgent, IP: s.IP,
			CreatedAt: httpx.FormatTime(s.CreatedAt), LastSeenAt: httpx.FormatTime(s.LastSeenAt), Current: s.Current,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

func (h *Accounts) revokeSession(w http.ResponseWriter, r *http.Request) {
	target := chi.URLParam(r, "session_id")
	if err := h.Svc.RevokeSession(r.Context(), httpx.UserID(r), httpx.SessionID(r), target, r.Header.Get(headerStepUp)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if target == httpx.SessionID(r) {
		h.clearCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

type historyItem struct {
	Method    string `json:"method"`
	Result    string `json:"result"`
	Identity  string `json:"identity"`
	DeviceID  string `json:"device_id"`
	UserAgent string `json:"user_agent"`
	IP        string `json:"ip"`
	NewDevice bool   `json:"new_device"`
	CreatedAt string `json:"created_at"`
}

func (h *Accounts) history(w http.ResponseWriter, r *http.Request) {
	var before int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n <= 0 {
			httpx.WriteError(w, r, apperr.Invalid("invalid cursor"))
			return
		}
		before = n
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, next, err := h.Svc.History(r.Context(), httpx.UserID(r), before, limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]historyItem, 0, len(events))
	for _, e := range events {
		items = append(items, historyItem{
			Method: e.Method, Result: e.Result, Identity: e.IdentityMask, DeviceID: e.DeviceID, UserAgent: e.UserAgent,
			IP: e.IP, NewDevice: e.NewDevice, CreatedAt: httpx.FormatTime(e.CreatedAt),
		})
	}
	resp := map[string]any{"items": items, "next_cursor": nil}
	if next > 0 {
		resp["next_cursor"] = strconv.FormatInt(next, 10)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type stepUpBody struct {
	ticketBody
	// TOTPCode proves the step-up with an authenticator app instead of an
	// OTP ticket; once one is bound it is the only way (§6.5).
	TOTPCode string `json:"totp_code"`
}

func (h *Accounts) stepUp(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[stepUpBody](w, r)
	if !ok {
		return
	}
	var token string
	var exp time.Time
	var err error
	if body.TOTPCode != "" {
		token, exp, err = h.Svc.StepUpTOTP(r.Context(), httpx.UserID(r), httpx.SessionID(r), body.TOTPCode)
	} else {
		token, exp, err = h.Svc.StepUp(r.Context(), httpx.UserID(r), httpx.SessionID(r), body.OTPTicket, client(r, body.DeviceID))
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"step_up_token": token, "expires_at": httpx.FormatTime(exp)})
}

type changePasswordBody struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Accounts) changePassword(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[changePasswordBody](w, r)
	if !ok {
		return
	}
	err := h.Svc.ChangePassword(r.Context(), httpx.UserID(r), httpx.SessionID(r), body.CurrentPassword, body.NewPassword, r.Header.Get(headerStepUp))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Accounts) bind(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[ticketBody](w, r)
	if !ok {
		return
	}
	if err := h.Svc.BindIdentity(r.Context(), httpx.UserID(r), body.OTPTicket, r.Header.Get(headerStepUp), client(r, body.DeviceID)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Accounts) rebind(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[ticketBody](w, r)
	if !ok {
		return
	}
	res, err := h.Svc.RebindIdentity(r.Context(), httpx.UserID(r), body.OTPTicket, r.Header.Get(headerStepUp), client(r, body.DeviceID))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if res == application.RebindPendingReview {
		status = http.StatusAccepted
	}
	httpx.WriteJSON(w, status, map[string]string{"status": string(res)})
}

func (h *Accounts) totpStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.Svc.TOTPStatus(r.Context(), httpx.UserID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"enabled": st.Enabled, "pending": st.Pending})
}

func (h *Accounts) totpSetup(w http.ResponseWriter, r *http.Request) {
	secret, uri, err := h.Svc.SetupTOTP(r.Context(), httpx.UserID(r), r.Header.Get(headerStepUp))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_uri": uri})
}

type totpCodeBody struct {
	Code string `json:"code"`
}

func (h *Accounts) totpConfirm(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[totpCodeBody](w, r)
	if !ok {
		return
	}
	if err := h.Svc.ConfirmTOTP(r.Context(), httpx.UserID(r), body.Code); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Accounts) totpDisable(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.DisableTOTP(r.Context(), httpx.UserID(r), r.Header.Get(headerStepUp)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
