// Package svcsign signs and checks requests between services with a shared
// secret: HMAC-SHA256 over the time, the method, the path with its query
// and the body. The callee knows the caller holds the secret, and so
// trusts who the caller says acts — an operator signed in to the admin
// console, and a second one who approved (ASTRA design §6.2: the
// approver's identity comes from the caller's credentials, not from a
// name anyone on the network could send). A signature older than MaxAge,
// or one seen before, is refused.
//
// The header is "X-Service-Signature: t=<unix seconds>,v1=<hex>".
package svcsign

import (
	"bytes"
	"crypto/hmac"
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
	"the request must be signed by a service that holds the shared secret")

// CheckSecret refuses a secret too short to sign with.
func CheckSecret(secret string) error {
	if len(secret) < MinSecret {
		return fmt.Errorf("the shared secret must be at least %d characters", MinSecret)
	}
	return nil
}

// Sign returns the header value for a request at now.
func Sign(secret []byte, method, pathAndQuery string, body []byte, now time.Time) string {
	t := strconv.FormatInt(now.Unix(), 10)
	return "t=" + t + ",v1=" + mac(secret, t, method, pathAndQuery, body)
}

// SignRequest signs r, whose body is body (nil: none), at now.
func SignRequest(r *http.Request, secret []byte, body []byte, now time.Time) {
	r.Header.Set(Header, Sign(secret, r.Method, r.URL.RequestURI(), body, now))
}

func mac(secret []byte, t, method, pathAndQuery string, body []byte) string {
	h := hmac.New(sha256.New, secret)
	fmt.Fprintf(h, "%s\n%s\n%s\n", t, method, pathAndQuery)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Verifier checks signed requests and remembers the signatures it took
// for MaxAge, refusing them again.
type Verifier struct {
	Secret []byte
	Now    func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Verify checks the signature of a request with body.
func (v *Verifier) Verify(method, pathAndQuery, header string, body []byte) error {
	var t, sig string
	for part := range strings.SplitSeq(header, ",") {
		k, val, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			t = val
		case "v1":
			sig = val
		}
	}
	unix, err := strconv.ParseInt(t, 10, 64)
	if err != nil || sig == "" {
		return ErrUnsigned
	}
	now := v.now()
	if d := now.Sub(time.Unix(unix, 0)); d > MaxAge || d < -MaxAge {
		return ErrUnsigned
	}
	if !hmac.Equal([]byte(sig), []byte(mac(v.Secret, t, method, pathAndQuery, body))) {
		return ErrUnsigned
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for s, at := range v.seen {
		if now.Sub(at) > 2*MaxAge {
			delete(v.seen, s)
		}
	}
	if _, again := v.seen[sig]; again {
		return ErrUnsigned
	}
	if v.seen == nil {
		v.seen = map[string]time.Time{}
	}
	v.seen[sig] = now
	return nil
}

// Changes guards the requests that change something (all but GET, HEAD
// and OPTIONS): unsigned ones are refused with ErrUnsigned.
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
		if err := v.Verify(r.Method, r.URL.RequestURI(), r.Header.Get(Header), body); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// Client sends signed requests.
type Client struct {
	Secret []byte
	HTTP   *http.Client
}

// Do sends method url with body (nil: none) signed and returns the
// response; the caller closes its body.
func (c Client) Do(r *http.Request, body []byte) (*http.Response, error) {
	if len(c.Secret) == 0 {
		return nil, errors.New("svcsign: no secret")
	}
	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	SignRequest(r, c.Secret, body, time.Now())
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(r) //nolint:gosec // the caller's own request to a peer it configured
}
