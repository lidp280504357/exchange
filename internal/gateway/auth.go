package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/authtoken"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/logging"
)

// Errors of the bearer check (appendix C).
var (
	errNoToken      = apperr.Unauthorized("sign in first")
	errBadToken     = apperr.Unauthorized("invalid access token")
	errTokenExpired = apperr.New(apperr.KindUnauthenticated, "AUTH_TOKEN_EXPIRED", "the access token has expired; refresh it")
	errRevoked      = apperr.New(apperr.KindUnauthenticated, "AUTH_SESSION_REVOKED", "the session has ended; sign in again")
	errReadOnly     = apperr.New(apperr.KindForbidden, "USER_FROZEN", "the account is frozen and may only read")
)

// SessionState reports whether auth-service ended a session, and the Unix
// millisecond up to which the user's tokens are stale (0: none).
type SessionState func(ctx context.Context, sessionID, userID string) (revoked bool, staleUpToMS int64, err error)

// Authenticator checks bearer access tokens (requirements §5.1, §5.2).
type Authenticator struct {
	Verifier *authtoken.Verifier
	// State is checked on every request. When it fails the token is
	// accepted: it expires within 15 minutes anyway.
	State SessionState
	Log   *slog.Logger
	Now   func() time.Time
}

// Required rejects requests without a valid access token.
func (a *Authenticator) Required(next http.Handler) http.Handler {
	return a.handler(next, true)
}

// Optional authenticates requests that carry a token and passes the rest
// through anonymously; a token that is present must be valid.
func (a *Authenticator) Optional(next http.Handler) http.Handler {
	return a.handler(next, false)
}

func (a *Authenticator) handler(next http.Handler, required bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, found := bearer(r)
		if !found {
			if required {
				httpx.WriteError(w, r, errNoToken)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		id, _, err := a.Authenticate(r.Context(), token)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		// A read-only (frozen) account may still secure itself through the
		// auth endpoints, but may not act anywhere else.
		if id.Scope != authtoken.ScopeFull && !safeMethod(r.Method) && !strings.HasPrefix(r.URL.Path, "/v1/auth/") {
			httpx.WriteError(w, r, errReadOnly)
			return
		}
		ctx := context.WithValue(r.Context(), identityKey{}, id)
		ctx = logging.WithAttrs(ctx, slog.String("user_id", id.UserID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Authenticate checks an access token and returns its identity and
// expiry.
func (a *Authenticator) Authenticate(ctx context.Context, token string) (Identity, time.Time, error) {
	id, claims, err := a.authenticate(ctx, token)
	if err != nil {
		return Identity{}, time.Time{}, err
	}
	var exp time.Time
	if claims.ExpiresAt != nil {
		exp = claims.ExpiresAt.Time
	}
	return id, exp, nil
}

func (a *Authenticator) authenticate(ctx context.Context, token string) (Identity, authtoken.Claims, error) {
	claims, err := a.Verifier.Verify(ctx, token, a.Now())
	switch {
	case errors.Is(err, authtoken.ErrExpired):
		return Identity{}, claims, errTokenExpired
	case errors.Is(err, authtoken.ErrKeysUnavailable):
		return Identity{}, claims, apperr.Unavailable(err)
	case err != nil:
		return Identity{}, claims, errBadToken
	}
	revoked, staleUpTo, err := a.State(ctx, claims.SessionID, claims.Subject)
	if err != nil {
		a.Log.WarnContext(ctx, "session state check failed; accepting the token", "error", err)
	}
	switch {
	case revoked:
		return Identity{}, claims, errRevoked
	case staleUpTo > 0 && claims.IssuedAtMillis() <= staleUpTo:
		// The account status changed after the token was issued: a
		// refresh brings the current scope.
		return Identity{}, claims, errTokenExpired
	}
	return Identity{UserID: claims.Subject, SessionID: claims.SessionID, Scope: claims.Scope}, claims, nil
}

// bearer extracts the token of an "Authorization: Bearer" header.
func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", true // present but malformed: fails verification
	}
	return strings.TrimSpace(token), true
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// IdentityFrom returns the authenticated caller of a request.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}
