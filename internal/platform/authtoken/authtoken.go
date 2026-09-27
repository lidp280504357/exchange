// Package authtoken issues and verifies access tokens (requirements §5.2,
// ADR-0009): JWTs signed with Ed25519, valid 15 minutes, carrying the user
// (sub), the session (sid) and a scope. auth-service signs; the gateway
// verifies with the public keys auth-service publishes as a JWKS, looked up
// by kid so keys can rotate.
package authtoken

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Token parameters.
const (
	Issuer    = "exchange-auth"
	Audience  = "exchange-api"
	AccessTTL = 15 * time.Minute
)

// Scopes.
const (
	// ScopeFull may read and act.
	ScopeFull = "full"
	// ScopeRead is granted to FROZEN accounts, which may only read (§5.4).
	ScopeRead = "read"
)

// RevokedKey is the Redis key marking a session as ended: auth-service sets
// it for AccessTTL when a session is revoked, and the gateway rejects the
// session's tokens while it exists. Losing it only means a revoked token
// keeps working until it expires.
func RevokedKey(sessionID string) string { return "auth:revoked:" + sessionID }

// Claims of an access token.
type Claims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
	Scope     string `json:"scope"`
}

// Verification errors.
var (
	ErrExpired = errors.New("access token expired")
	ErrInvalid = errors.New("access token invalid")
	// ErrKeysUnavailable means the token names a key the verifier does not
	// have and the key set could not be fetched: a server-side problem,
	// not a bad token.
	ErrKeysUnavailable = errors.New("access token keys unavailable")
)

// Signer issues tokens with one key.
type Signer struct {
	key ed25519.PrivateKey
	kid string
}

// NewSigner derives the key from a 32-byte seed.
func NewSigner(seed []byte, kid string) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("signing key seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	if kid == "" {
		return nil, errors.New("key id is required")
	}
	return &Signer{key: ed25519.NewKeyFromSeed(seed), kid: kid}, nil
}

// Issue signs an access token and returns it with its expiry.
func (s *Signer) Issue(userID, sessionID, scope string, now time.Time) (string, time.Time, error) {
	exp := now.Add(AccessTTL)
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		SessionID: sessionID,
		Scope:     scope,
	})
	t.Header["kid"] = s.kid
	signed, err := t.SignedString(s.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, exp, nil
}

// JWK is an Ed25519 public key in JSON Web Key form (RFC 8037).
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

// JWKS is a key set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWKS publishes the signer's public key.
func (s *Signer) JWKS() JWKS {
	pub, _ := s.key.Public().(ed25519.PublicKey)
	return JWKS{Keys: []JWK{{
		Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub), Kid: s.kid, Alg: "EdDSA", Use: "sig",
	}}}
}

// Verifier checks tokens against a key set fetched on demand: at first use,
// when a token names an unknown kid (at most once a minute), and when the
// set is older than refreshAfter. After a failed fetch it waits retryGap
// before trying again, so an unreachable auth-service is not hammered.
type Verifier struct {
	fetch func(context.Context) (JWKS, error)

	mu        sync.Mutex
	keys      map[string]ed25519.PublicKey
	fetchedAt time.Time
	triedAt   time.Time
	fetchErr  error
}

const (
	refreshAfter = 10 * time.Minute
	refetchGap   = time.Minute
	retryGap     = 5 * time.Second
)

// NewVerifier returns a verifier that loads keys with fetch.
func NewVerifier(fetch func(context.Context) (JWKS, error)) *Verifier {
	return &Verifier{fetch: fetch, keys: map[string]ed25519.PublicKey{}}
}

// Verify parses and checks a token.
func (v *Verifier) Verify(ctx context.Context, token string, now time.Time) (Claims, error) {
	var claims Claims
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	_, err := parser.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return v.key(ctx, kid, now)
	})
	switch {
	case err == nil && claims.Subject != "" && claims.SessionID != "":
		return claims, nil
	case errors.Is(err, ErrKeysUnavailable):
		return Claims{}, err
	case errors.Is(err, jwt.ErrTokenExpired):
		return Claims{}, ErrExpired
	default:
		return Claims{}, ErrInvalid
	}
}

func (v *Verifier) key(ctx context.Context, kid string, now time.Time) (ed25519.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	k, ok := v.keys[kid]
	due := now.Sub(v.fetchedAt) > refreshAfter || (!ok && now.Sub(v.fetchedAt) > refetchGap)
	if due && (v.fetchErr == nil || now.Sub(v.triedAt) > retryGap) {
		v.triedAt = now
		set, err := v.fetch(ctx)
		v.fetchErr = err
		if err == nil {
			v.keys = map[string]ed25519.PublicKey{}
			for _, j := range set.Keys {
				if pub, err := base64.RawURLEncoding.DecodeString(j.X); err == nil && j.Crv == "Ed25519" && len(pub) == ed25519.PublicKeySize {
					v.keys[j.Kid] = pub
				}
			}
			v.fetchedAt = now
			k, ok = v.keys[kid]
		}
	}
	switch {
	case ok:
		return k, nil
	case v.fetchErr != nil:
		return nil, fmt.Errorf("%w: %w", ErrKeysUnavailable, v.fetchErr)
	default:
		return nil, fmt.Errorf("unknown key %q", kid)
	}
}

// HTTPKeys fetches a key set from url, e.g. auth-service's /internal/jwks.
func HTTPKeys(client *http.Client, url string) func(context.Context) (JWKS, error) {
	return func(ctx context.Context) (JWKS, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return JWKS{}, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return JWKS{}, fmt.Errorf("fetch keys: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return JWKS{}, fmt.Errorf("fetch keys: status %d", resp.StatusCode)
		}
		var set JWKS
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&set); err != nil {
			return JWKS{}, fmt.Errorf("decode keys: %w", err)
		}
		return set, nil
	}
}
