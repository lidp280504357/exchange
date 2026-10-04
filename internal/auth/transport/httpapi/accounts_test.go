package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/auth/application"
	"github.com/skill/exchange/internal/platform/authtoken"
)

func router() http.Handler {
	r := chi.NewRouter()
	(&Accounts{
		Svc:            &application.AccountService{Config: application.AccountConfig{TermsVersion: "t1", RiskVersion: "r1"}},
		JWKS:           authtoken.JWKS{Keys: []authtoken.JWK{{Kid: "k1"}}},
		AllowedOrigins: []string{"https://astras.vip"},
		SecureCookies:  true,
	}).Routes(r)
	return r
}

func do(h http.Handler, method, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func code(rec *httptest.ResponseRecorder) string {
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Code
}

func TestRefreshNeedsAllowedOriginAndCookie(t *testing.T) {
	h := router()
	rec := do(h, http.MethodPost, "/v1/auth/token/refresh", map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodPost, "/v1/auth/token/refresh", map[string]string{"Origin": "https://astras.vip"})
	if rec.Code != http.StatusUnauthorized || code(rec) != "AUTH_SESSION_REVOKED" {
		t.Fatalf("no cookie: %d %s", rec.Code, rec.Body)
	}
}

func TestProtectedRoutesNeedGatewayIdentity(t *testing.T) {
	h := router()
	for _, p := range []string{"/v1/auth/sessions", "/v1/auth/login-history"} {
		if rec := do(h, http.MethodGet, p, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestTermsAndJWKS(t *testing.T) {
	h := router()
	rec := do(h, http.MethodGet, "/v1/auth/terms", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"terms_version":"t1"`) {
		t.Fatalf("terms: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/internal/jwks", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"kid":"k1"`) {
		t.Fatalf("jwks: %d %s", rec.Code, rec.Body)
	}
}

func TestWriteTokensPlacesRefreshToken(t *testing.T) {
	h := &Accounts{SecureCookies: true}
	tokens := application.Tokens{
		UserID: "u", SessionID: "s", Scope: "full", AccessToken: "a", AccessExpiresAt: time.Now().Add(15 * time.Minute),
		RefreshToken: "secret-refresh", RefreshExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	rec := httptest.NewRecorder()
	h.writeTokens(rec, application.Client{ClientType: "WEB"}, tokens, http.StatusOK)
	ck := rec.Result().Cookies()
	if len(ck) != 1 || ck[0].Value != "secret-refresh" || !ck[0].HttpOnly || !ck[0].Secure ||
		ck[0].SameSite != http.SameSiteStrictMode || ck[0].Path != refreshPath {
		t.Fatalf("web cookie: %+v", ck)
	}
	if strings.Contains(rec.Body.String(), "secret-refresh") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("web body must not carry the refresh token: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	h.writeTokens(rec, application.Client{ClientType: "APP"}, tokens, http.StatusOK)
	if len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), `"refresh_token":"secret-refresh"`) {
		t.Fatalf("app body: %s", rec.Body)
	}
}
