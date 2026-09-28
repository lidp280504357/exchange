package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Idempotency-Key handling (requirements §7.1): scoped to user and path,
// kept 24 hours; the same key with the same body replays the first
// response, with another body it fails with COMMON_IDEMPOTENCY_CONFLICT.
// Redis is the short-term cache; services that own money keep their own
// durable record of the key (the ledger's transfers), so losing Redis
// never duplicates a write.
const (
	HeaderIdempotencyKey = "Idempotency-Key"
	headerReplayed       = "Idempotent-Replayed"
	idempotencyTTL       = 24 * time.Hour
	maxStoredResponse    = 256 << 10
)

var idempotencyKeyRE = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,100}$`)

var (
	errIdempotencyKey      = apperr.Invalid("Idempotency-Key must be 8 to 100 letters, digits or _.:-")
	errIdempotencyConflict = apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict,
		"the idempotency key was used for a different request")
	errIdempotencyBusy = apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict,
		"a request with this idempotency key is still in progress")
)

// Idempotency replays responses of repeated writes.
type Idempotency struct {
	Redis redis.Cmdable
	Log   *slog.Logger
}

type storedResponse struct {
	State       string `json:"state"` // pending or done
	Hash        string `json:"hash"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

func writeMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

// Middleware applies to signed-in writes that carry the header; it runs
// after the authenticator.
func (i *Idempotency) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(HeaderIdempotencyKey)
		id, signedIn := IdentityFrom(r.Context())
		if key == "" || !signedIn || !writeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if !idempotencyKeyRE.MatchString(key) {
			httpx.WriteError(w, r, errIdempotencyKey)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpx.MaxBodyBytes))
		if err != nil {
			httpx.WriteError(w, r, apperr.Invalid("request body too large"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
		hash := hex.EncodeToString(sum[:])
		rkey := "gw:idem:" + id.UserID + ":" + r.Method + " " + r.URL.Path + ":" + key

		pending, _ := json.Marshal(storedResponse{State: "pending", Hash: hash})
		claimed, err := i.Redis.SetNX(r.Context(), rkey, pending, idempotencyTTL).Result()
		if err != nil {
			i.Log.WarnContext(r.Context(), "idempotency cache unavailable; the service's own key check applies", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		if !claimed {
			i.replay(w, r, rkey, hash)
			return
		}
		rec := &capture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		i.finish(r.Context(), rkey, hash, rec)
	})
}

// replay answers a repeated key from the cache.
func (i *Idempotency) replay(w http.ResponseWriter, r *http.Request, rkey, hash string) {
	raw, err := i.Redis.Get(r.Context(), rkey).Bytes()
	var stored storedResponse
	if err == nil {
		err = json.Unmarshal(raw, &stored)
	}
	switch {
	case errors.Is(err, redis.Nil):
		// Expired between SETNX and GET: treat as in progress, the client retries.
		httpx.WriteError(w, r, errIdempotencyBusy)
	case err != nil:
		httpx.WriteError(w, r, apperr.Unavailable(err))
	case stored.Hash != hash:
		httpx.WriteError(w, r, errIdempotencyConflict)
	case stored.State != "done":
		httpx.WriteError(w, r, errIdempotencyBusy)
	default:
		if stored.ContentType != "" {
			w.Header().Set("Content-Type", stored.ContentType)
		}
		w.Header().Set(headerReplayed, "true")
		w.WriteHeader(stored.Status)
		_, _ = w.Write(stored.Body)
	}
}

// finish keeps the response for replays; server errors release the key so
// the client may retry.
func (i *Idempotency) finish(ctx context.Context, rkey, hash string, rec *capture) {
	ctx = context.WithoutCancel(ctx)
	if rec.status >= 500 || rec.overflow {
		if err := i.Redis.Del(ctx, rkey).Err(); err != nil {
			i.Log.WarnContext(ctx, "releasing an idempotency key failed", "error", err)
		}
		return
	}
	done, _ := json.Marshal(storedResponse{
		State: "done", Hash: hash, Status: rec.status, ContentType: rec.Header().Get("Content-Type"), Body: rec.body.Bytes(),
	})
	if err := i.Redis.Set(ctx, rkey, done, idempotencyTTL).Err(); err != nil {
		i.Log.WarnContext(ctx, "storing an idempotent response failed", "error", err)
	}
}

// capture passes a response through while keeping a copy.
type capture struct {
	http.ResponseWriter
	status   int
	body     bytes.Buffer
	overflow bool
	wrote    bool
}

func (c *capture) WriteHeader(status int) {
	if !c.wrote {
		c.status, c.wrote = status, true
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *capture) Write(b []byte) (int, error) {
	c.wrote = true
	if c.body.Len()+len(b) <= maxStoredResponse {
		c.body.Write(b)
	} else {
		c.overflow = true
	}
	return c.ResponseWriter.Write(b)
}

func (c *capture) Unwrap() http.ResponseWriter { return c.ResponseWriter }
