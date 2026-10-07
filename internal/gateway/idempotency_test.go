package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/testenv"
)

func TestIdempotencyReplaysAndConflicts(t *testing.T) {
	rdb, _ := testenv.Redis(t)
	user := "u-" + testenv.Name("idem")
	t.Cleanup(func() {
		keys, _ := rdb.Keys(context.Background(), "gw:idem:"+user+":*").Result()
		if len(keys) > 0 {
			rdb.Del(context.Background(), keys...)
		}
	})
	var calls atomic.Int32
	status := http.StatusCreated
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"echo":` + string(body) + `}`))
	})
	h := (&Idempotency{Redis: rdb, Log: slog.New(slog.DiscardHandler)}).Middleware(upstream)
	send := func(key, body string) *httptest.ResponseRecorder {
		ctx := context.WithValue(context.Background(), identityKey{}, Identity{UserID: user})
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/account/transfers", strings.NewReader(body))
		if key != "" {
			req.Header.Set(HeaderIdempotencyKey, key)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	first := send("key-00001", `{"a":1}`)
	again := send("key-00001", `{"a":1}`)
	if first.Code != 201 || again.Code != 201 || again.Body.String() != first.Body.String() ||
		again.Header().Get(headerReplayed) != "true" || calls.Load() != 1 {
		t.Fatalf("replay: %d %d %q %q calls=%d", first.Code, again.Code, first.Body, again.Body, calls.Load())
	}
	if rec := send("key-00001", `{"a":2}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "COMMON_IDEMPOTENCY_CONFLICT") {
		t.Fatalf("other body: %d %s", rec.Code, rec.Body)
	}
	if rec := send("bad key!", `{}`); rec.Code != 400 {
		t.Fatalf("malformed key: %d", rec.Code)
	}
	if send("", `{}`); calls.Load() != 2 {
		t.Fatal("requests without a key pass through")
	}

	// Server errors release the key so the client can retry.
	status = http.StatusBadGateway
	if rec := send("key-00002", `{}`); rec.Code != 502 {
		t.Fatalf("upstream failure: %d", rec.Code)
	}
	status = http.StatusCreated
	if rec := send("key-00002", `{}`); rec.Code != 201 || rec.Header().Get(headerReplayed) != "" {
		t.Fatalf("retry after a 5xx: %d %v", rec.Code, rec.Header())
	}
	// So does a request the client gave up on (499, review B155): a retry
	// reaches the service instead of replaying an empty answer.
	status = httpx.StatusClientClosedRequest
	if rec := send("key-00004", `{}`); rec.Code != httpx.StatusClientClosedRequest {
		t.Fatalf("client gone: %d", rec.Code)
	}
	status = http.StatusCreated
	before := calls.Load()
	if rec := send("key-00004", `{}`); rec.Code != 201 || rec.Header().Get(headerReplayed) != "" || calls.Load() != before+1 {
		t.Fatalf("retry after a 499: %d %v, calls %d", rec.Code, rec.Header(), calls.Load()-before)
	}

	// A key still in flight is refused.
	rdb.Set(context.Background(), "gw:idem:"+user+":POST /v1/account/transfers:key-00003", `{"state":"pending","hash":"x"}`, 0)
	if rec := send("key-00003", `{}`); rec.Code != 409 {
		t.Fatalf("in progress: %d", rec.Code)
	}
}
