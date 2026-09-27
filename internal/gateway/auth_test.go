package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/authtoken"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

type authFixture struct {
	signer  *authtoken.Signer
	revoked map[string]bool
	now     time.Time
	router  http.Handler
	seen    *Identity
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	signer, err := authtoken.NewSigner(seed, "k1")
	if err != nil {
		t.Fatal(err)
	}
	f := &authFixture{signer: signer, revoked: map[string]bool{}, now: time.Now()}
	authn := &Authenticator{
		Verifier: authtoken.NewVerifier(func(context.Context) (authtoken.JWKS, error) { return signer.JWKS(), nil }),
		Revoked: func(_ context.Context, sid string) (bool, error) {
			if sid == "redis-down" {
				return false, errors.New("redis down")
			}
			return f.revoked[sid], nil
		},
		Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return f.now },
	}
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := IdentityFrom(r.Context()); ok {
			f.seen = &id
		} else {
			f.seen = nil
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r := httpx.NewRouter(httpx.RouterOptions{Logger: slog.New(slog.DiscardHandler)})
	Mount(r, authn, Upstreams{Auth: upstream})
	r.With(authn.Required).Post("/v1/orders", upstream.ServeHTTP)
	f.router = r
	return f
}

func (f *authFixture) token(t *testing.T, sid, scope string) string {
	t.Helper()
	tok, _, err := f.signer.Issue("u-1", sid, scope, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// call returns the status and the error code of a request.
func (f *authFixture) call(method, path, authorization string) (int, string) {
	req := httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Code
}

func TestRequiredAuthentication(t *testing.T) {
	f := newAuthFixture(t)
	for _, tc := range []struct {
		name, auth string
		status     int
		code       string
	}{
		{"no token", "", 401, "COMMON_UNAUTHORIZED"},
		{"not bearer", "Basic dTpw", 401, "COMMON_UNAUTHORIZED"},
		{"garbage", "Bearer nope", 401, "COMMON_UNAUTHORIZED"},
		{"valid", "Bearer " + f.token(t, "s-1", authtoken.ScopeFull), 204, ""},
		{"revocation store down", "Bearer " + f.token(t, "redis-down", authtoken.ScopeFull), 204, ""},
	} {
		status, code := f.call(http.MethodGet, "/v1/auth/sessions", tc.auth)
		if status != tc.status || code != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, status, code, tc.status, tc.code)
		}
	}
	if f.seen == nil || f.seen.UserID != "u-1" || f.seen.SessionID != "redis-down" || f.seen.Scope != authtoken.ScopeFull {
		t.Fatalf("identity: %+v", f.seen)
	}

	tok := f.token(t, "s-2", authtoken.ScopeFull)
	f.revoked["s-2"] = true
	if status, code := f.call(http.MethodGet, "/v1/auth/sessions", "Bearer "+tok); status != 401 || code != "AUTH_SESSION_REVOKED" {
		t.Fatalf("revoked: %d %s", status, code)
	}
	tok = f.token(t, "s-3", authtoken.ScopeFull)
	f.now = f.now.Add(authtoken.AccessTTL + time.Second)
	if status, code := f.call(http.MethodGet, "/v1/auth/sessions", "Bearer "+tok); status != 401 || code != "AUTH_TOKEN_EXPIRED" {
		t.Fatalf("expired: %d %s", status, code)
	}
}

func TestPublicAndOptionalRoutes(t *testing.T) {
	f := newAuthFixture(t)
	// Token flows ignore a stale access token.
	expired := f.token(t, "s-1", authtoken.ScopeFull)
	f.now = f.now.Add(time.Hour)
	for _, p := range []string{"/v1/auth/token/refresh", "/v1/auth/login/password", "/v1/auth/otp/verify"} {
		if status, _ := f.call(http.MethodPost, p, "Bearer "+expired); status != 204 || f.seen != nil {
			t.Errorf("%s: %d, identity %+v", p, status, f.seen)
		}
	}
	// otp/request is anonymous without a token, and authenticated with one.
	if status, _ := f.call(http.MethodPost, "/v1/auth/otp/request", ""); status != 204 || f.seen != nil {
		t.Fatalf("anonymous otp/request: %d", status)
	}
	if status, code := f.call(http.MethodPost, "/v1/auth/otp/request", "Bearer "+expired); status != 401 || code != "AUTH_TOKEN_EXPIRED" {
		t.Fatalf("expired otp/request: %d %s", status, code)
	}
	if status, _ := f.call(http.MethodPost, "/v1/auth/otp/request", "Bearer "+f.token(t, "s-9", authtoken.ScopeFull)); status != 204 || f.seen == nil {
		t.Fatalf("signed-in otp/request: %d", status)
	}
	// Unknown auth paths are not public.
	if status, _ := f.call(http.MethodPost, "/v1/auth/something-new", ""); status != 401 {
		t.Fatalf("unlisted path: %d", status)
	}
}

func TestReadOnlyScope(t *testing.T) {
	f := newAuthFixture(t)
	read := "Bearer " + f.token(t, "s-1", authtoken.ScopeRead)
	if status, code := f.call(http.MethodPost, "/v1/orders", read); status != 403 || code != "USER_FROZEN" {
		t.Fatalf("frozen write: %d %s", status, code)
	}
	if status, _ := f.call(http.MethodPost, "/v1/auth/logout", read); status != 204 {
		t.Fatalf("frozen accounts may still secure themselves: %d", status)
	}
	if status, _ := f.call(http.MethodGet, "/v1/auth/sessions", read); status != 204 {
		t.Fatalf("frozen read: %d", status)
	}
}
