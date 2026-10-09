package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/user/application"
	"github.com/skill/exchange/internal/user/ports"
)

// kindStore answers IDsOfKinds from ids and records the kinds it was asked
// for; nothing else of the store is used here.
type kindStore struct {
	ports.Store
	ids   []string
	asked *[]string
}

func (s kindStore) Read() ports.Repos { return kindRepos{ids: s.ids, asked: s.asked} }

type kindRepos struct {
	ports.Repos
	ids   []string
	asked *[]string
}

func (r kindRepos) Users() ports.UserRepo { return kindUsers{ids: r.ids, asked: r.asked} }

type kindUsers struct {
	ports.UserRepo
	ids   []string
	asked *[]string
}

func (u kindUsers) IDsOfKinds(_ context.Context, kinds []string) ([]string, error) {
	*u.asked = kinds
	return u.ids, nil
}

// The accounts of some kinds (L0): ?kind= comma-separated or repeated,
// whatever the case; an ETag and a minute's caching, 304 on a match; no
// kind is 400; a request that came through the gateway (X-User-Id) is not
// served.
func TestInternalAccountsOfKinds(t *testing.T) {
	var asked []string
	h := &Handler{Svc: &application.Service{Store: kindStore{ids: []string{"u1", "u2"}, asked: &asked}}}
	r := chi.NewRouter()
	h.InternalRoutes(r)
	get := func(url string, header ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := get("/internal/users/ids?kind=bot,TEST&kind=system")
	var body struct {
		UserIDs []string `json:"user_ids"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || !slices.Equal(body.UserIDs, []string{"u1", "u2"}) {
		t.Fatalf("ids: %d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(asked, []string{"BOT", "TEST", "SYSTEM"}) {
		t.Fatalf("asked for %v", asked)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || rec.Header().Get("Cache-Control") != "max-age=60" {
		t.Fatalf("headers %v", rec.Header())
	}
	if again := get("/internal/users/ids?kind=BOT,TEST,SYSTEM", "If-None-Match", etag); again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("not modified: %d %s", again.Code, again.Body)
	}
	if none := get("/internal/users/ids"); none.Code != http.StatusBadRequest {
		t.Fatalf("no kind: %d", none.Code)
	}
	if bad := get("/internal/users/ids?kind=ROBOT"); bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "ROBOT") {
		t.Fatalf("unknown kind: %d %s", bad.Code, bad.Body)
	}
	if outside := get("/internal/users/ids?kind=BOT", "X-User-Id", "u9"); outside.Code != http.StatusNotFound {
		t.Fatalf("through the gateway: %d", outside.Code)
	}
}
