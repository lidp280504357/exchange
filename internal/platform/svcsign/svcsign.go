// Package svcsign signs and checks requests between services with shared
// secrets: HMAC-SHA256 over the time, a nonce, the method, the path with
// its query and the body. Each caller has its own key, named by an ID the
// callee looks up (one per calling service), so the callee knows which
// service signed and trusts who that service says acts — for market-sim,
// only the admin console's service, which signed in the operators, may
// name an approver (ASTRA design §6.2: the approver's identity comes from
// the caller's credentials, not from a name anyone on the network could
// send). A signature older than MaxAge, or one seen before, is refused.
//
// The header is "X-Service-Signature: k=<key ID>,t=<unix seconds>,n=<nonce>,v1=<hex>".
package svcsign

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Header carries the signature.
const Header = "X-Service-Signature"

// MaxAge is how far a signature's time may be from the callee's clock.
const MaxAge = 5 * time.Minute

// MinSecret is the shortest secret accepted.
const MinSecret = 32

// maxBody is the largest body checked.
const maxBody = 1 << 20

// ErrUnsigned refuses a request without a valid signature.
var ErrUnsigned = apperr.New(apperr.KindUnauthenticated, "SERVICE_UNSIGNED",
	"the request must be signed by a service that holds one of the callee's keys")

// CheckSecret refuses a secret too short to sign with.
func CheckSecret(secret string) error {
	if len(secret) < MinSecret {
		return fmt.Errorf("the shared secret must be at least %d characters", MinSecret)
	}
	return nil
}

// Sign returns the header value for a request at now, signed with the key
// keyID names.
func Sign(keyID string, secret []byte, method, pathAndQuery string, body []byte, now time.Time) string {
	t := strconv.FormatInt(now.Unix(), 10)
	var b [12]byte
	_, _ = rand.Read(b[:])
	n := hex.EncodeToString(b[:])
	return "k=" + keyID + ",t=" + t + ",n=" + n + ",v1=" + mac(secret, t, n, method, pathAndQuery, body)
}

// SignRequest signs r, whose body is body (nil: none), at now.
func SignRequest(r *http.Request, keyID string, secret []byte, body []byte, now time.Time) {
	r.Header.Set(Header, Sign(keyID, secret, r.Method, r.URL.RequestURI(), body, now))
}

func mac(secret []byte, t, n, method, pathAndQuery string, body []byte) string {
	h := hmac.New(sha256.New, secret)
	fmt.Fprintf(h, "%s\n%s\n%s\n%s\n", t, n, method, pathAndQuery)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Verifier checks signed requests against its keys (key ID to secret) and
// remembers the nonces it took for twice MaxAge, refusing them again.
type Verifier struct {
	Keys map[string][]byte
	Now  func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Verify checks the signature of a request with body and returns the ID
// of the key that signed it.
func (v *Verifier) Verify(method, pathAndQuery, header string, body []byte) (string, error) {
	var k, t, n, sig string
	for part := range strings.SplitSeq(header, ",") {
		key, val, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch key {
		case "k":
			k = val
		case "t":
			t = val
		case "n":
			n = val
		case "v1":
			sig = val
		}
	}
	secret, ok := v.Keys[k]
	unix, err := strconv.ParseInt(t, 10, 64)
	if !ok || len(secret) == 0 || err != nil || n == "" || sig == "" {
		return "", ErrUnsigned
	}
	now := v.now()
	if d := now.Sub(time.Unix(unix, 0)); d > MaxAge || d < -MaxAge {
		return "", ErrUnsigned
	}
	if !hmac.Equal([]byte(sig), []byte(mac(secret, t, n, method, pathAndQuery, body))) {
		return "", ErrUnsigned
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for s, at := range v.seen {
		if now.Sub(at) > 2*MaxAge {
			delete(v.seen, s)
		}
	}
	nonce := k + "/" + n
	if _, again := v.seen[nonce]; again {
		return "", ErrUnsigned
	}
	if v.seen == nil {
		v.seen = map[string]time.Time{}
	}
	v.seen[nonce] = now
	return k, nil
}

type keyIDKey struct{}

// KeyID is the ID of the key that signed the request of ctx (Changes), ""
// for a request that was not checked (a read).
func KeyID(ctx context.Context) string {
	k, _ := ctx.Value(keyIDKey{}).(string)
	return k
}

// Changes guards the requests that change something (all but GET, HEAD
// and OPTIONS): unsigned ones are refused with ErrUnsigned; signed ones
// carry the signing key's ID in their context (KeyID).
func (v *Verifier) Changes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil || len(body) > maxBody {
			httpx.WriteError(w, r, apperr.Invalid("the body could not be read"))
			return
		}
		k, err := v.Verify(r.Method, r.URL.RequestURI(), r.Header.Get(Header), body)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyIDKey{}, k)))
	})
}

// Client sends requests signed with one key.
type Client struct {
	KeyID  string
	Secret []byte
	HTTP   *http.Client
}

// Do sends r with body (nil: none) signed and returns the response; the
// caller closes its body.
func (c Client) Do(r *http.Request, body []byte) (*http.Response, error) {
	if len(c.Secret) == 0 || c.KeyID == "" {
		return nil, errors.New("svcsign: no key")
	}
	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	SignRequest(r, c.KeyID, c.Secret, body, time.Now())
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(r) //nolint:gosec // the caller's own request to a peer it configured
}
