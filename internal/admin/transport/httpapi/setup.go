package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/lidp280504357/exchange/internal/admin/application"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
)

// Administrators set their own credentials (C5.5 ⑪): the one-time setup
// link's endpoints need no session (the token is the proof); the
// administrator's own password and authenticator need theirs. What either
// answers is shown once: no cache keeps it, no log or audit has it.

// SetupJSON is a setup link, shown once to whoever created or reset the
// account to hand over.
type SetupJSON struct {
	Token     string `json:"token"`
	Kind      string `json:"kind"`
	ExpiresAt string `json:"expires_at"`
}

// writeSetup answers a create or a reset with its setup link.
func writeSetup(w http.ResponseWriter, status int, admin *ManagedAdminJSON, s application.Setup) {
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{"setup": SetupJSON{Token: s.Token, Kind: s.Kind, ExpiresAt: httpx.FormatTime(s.ExpiresAt)}}
	if admin != nil {
		out["admin"] = admin
	}
	httpx.WriteJSON(w, status, out)
}

// RuleOwn throttles an administrator's own password and authenticator
// changes, each of which checks the current password.
var RuleOwn = ratelimit.Rule{Name: "admin_own_credentials", Limit: 10, Window: 15 * time.Minute}

// throttle counts a request against rule for key (a setup link's against
// the sign-in limit of its address, one's own credentials' against the
// administrator); false when it was answered (limited).
func (h *Handler) throttle(w http.ResponseWriter, r *http.Request, rule ratelimit.Rule, key string) bool {
	if h.Limiter == nil {
		return true
	}
	res, err := h.Limiter.Allow(r.Context(), ratelimit.Check{Rule: rule, Key: key})
	if err != nil {
		httpx.WriteError(w, r, err)
		return false
	}
	if !res.Allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter.Seconds())+1))
		httpx.WriteError(w, r, apperr.New(apperr.KindRateLimited, apperr.CodeRateLimited, "too many attempts"))
		return false
	}
	return true
}

func (h *Handler) inspectSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.throttle(w, r, RuleLogin, clientIP(r)) {
		return
	}
	v, err := h.Svc.InspectSetup(r.Context(), body.Token)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"email": v.Email, "name": v.Name, "kind": v.Kind, "expires_at": httpx.FormatTime(v.ExpiresAt),
		"sets_password": domain.SetsPassword(v.Kind), "totp_secret": optText(v.TOTPSecret), "totp_uri": optText(v.TOTPURI),
	})
}

func (h *Handler) completeSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.throttle(w, r, RuleLogin, clientIP(r)) {
		return
	}
	if err := h.Svc.CompleteSetup(r.Context(), body.Token, body.Password, body.TOTPCode, clientIP(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.throttle(w, r, RuleOwn, principal(r).Admin.ID) {
		return
	}
	if err := h.Svc.ChangeOwnPassword(r.Context(), principal(r), body.Current, body.New); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) startOwnTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		Code    string `json:"totp_code"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.throttle(w, r, RuleOwn, principal(r).Admin.ID) {
		return
	}
	secret, uri, err := h.Svc.StartOwnTOTP(r.Context(), principal(r), body.Current, body.Code)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"totp_secret": secret, "totp_uri": uri})
}

func (h *Handler) confirmOwnTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"totp_code"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.throttle(w, r, RuleOwn, principal(r).Admin.ID) {
		return
	}
	if err := h.Svc.ConfirmOwnTOTP(r.Context(), principal(r), body.Code); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// whileChanging are the routes open to an administrator who must change
// their password first.
var whileChanging = map[string]bool{
	"GET /admin/v1/me": true, "POST /admin/v1/logout": true, "POST /admin/v1/me/password": true,
}

// mustChange holds an administrator whose password was generated for them
// on their own password until they change it (C5.5 ⑪).
func mustChange(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Admin.MustChangePassword && !whileChanging[r.Method+" "+r.URL.Path] {
			httpx.WriteError(w, r, domain.ErrPasswordChangeRequired)
			return
		}
		next.ServeHTTP(w, r)
	})
}
