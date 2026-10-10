package backends_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/backends"
	"github.com/skill/exchange/internal/platform/apperr"
)

// user-service's accounts of some kinds (L1): asked once a minute by the
// kinds, whatever their order; then revalidated with their ETag; the last
// ones read go on while user-service does not answer, asked again each
// time; kinds never read are unavailable then.
func TestKindIDs(t *testing.T) {
	var (
		mu    sync.Mutex
		asked []string
		down  bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.URL.Query().Get("kind")+" "+r.Header.Get("If-None-Match"))
		switch {
		case down:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"COMMON_UNAVAILABLE","message":"down"}`))
		case r.Header.Get("If-None-Match") == `"v1"`:
			w.WriteHeader(http.StatusNotModified)
		default:
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"user_ids":["a","b"]}`))
		}
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	k := backends.NewKindIDs(backends.REST{Client: srv.Client()}, srv.URL)
	k.Now = func() time.Time { return now }
	ctx := context.Background()
	read := func(step string, kinds ...string) {
		t.Helper()
		if ids, err := k.IDs(ctx, kinds); err != nil || !slices.Equal(ids, []string{"a", "b"}) {
			t.Fatalf("%s: %v %v", step, ids, err)
		}
	}
	read("asked", "TEST", "BOT")
	read("kept a minute", "BOT", "TEST")
	now = now.Add(61 * time.Second)
	read("revalidated", "BOT", "TEST")
	now = now.Add(61 * time.Second)
	mu.Lock()
	down = true
	mu.Unlock()
	read("the last ones while user-service is down", "BOT", "TEST")
	read("asked again", "BOT", "TEST")
	if _, err := k.IDs(ctx, []string{"SYSTEM"}); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("kinds never read: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"BOT,TEST ", `BOT,TEST "v1"`, `BOT,TEST "v1"`, `BOT,TEST "v1"`, "SYSTEM "}; !slices.Equal(asked, want) {
		t.Fatalf("asked %q, want %q", asked, want)
	}
}
