package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/user/application"
	"github.com/skill/exchange/internal/user/domain"
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

// kindTx serves SetKind from one account: its kind, the changes and the
// audit events it was given.
type kindTx struct {
	ports.Store
	user    domain.User
	changes []domain.KindChange
	events  []proto.Message
}

func (s *kindTx) Tx(_ context.Context, fn func(ports.Repos) error) error {
	return fn(kindTxRepos{s: s})
}

type kindTxRepos struct {
	ports.Repos
	s *kindTx
}

func (r kindTxRepos) Users() ports.UserRepo { return kindTxUsers{s: r.s} }

func (r kindTxRepos) Emit(_ context.Context, _ string, msg proto.Message, _, _ string) error {
	r.s.events = append(r.s.events, msg)
	return nil
}

type kindTxUsers struct {
	ports.UserRepo
	s *kindTx
}

func (u kindTxUsers) GetForUpdate(_ context.Context, id string) (domain.User, error) {
	if id != u.s.user.ID {
		return domain.User{}, domain.ErrUserNotFound
	}
	return u.s.user, nil
}

func (u kindTxUsers) SetKind(_ context.Context, _ string, kind string) error {
	u.s.user.Kind = kind
	return nil
}

func (u kindTxUsers) AddKindChange(_ context.Context, c domain.KindChange) error {
	u.s.changes = append(u.s.changes, c)
	return nil
}

// Setting an account's kind (L0, B177): the change with its actor
// ("internal" when none is given) and reason, audited; the kind it has
// already changes nothing; an unknown kind, a reason that is missing or
// too long and a body that is no JSON object are 400, an unknown account
// is 404, and so is a request that came through the gateway.
func TestInternalSetKind(t *testing.T) {
	const id = "0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e"
	store := &kindTx{user: domain.User{ID: id, Kind: domain.KindHuman}}
	h := &Handler{Svc: &application.Service{Store: store, Now: func() time.Time { return time.Unix(1_791_600_000, 0) }}}
	r := chi.NewRouter()
	h.InternalRoutes(r)
	put := func(user, body string, header ...string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/internal/users/"+user+"/kind", strings.NewReader(body))
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := put(id, `{"kind":"bot","reason":"the simulated market's bots","actor":"ops@example.com"}`)
	if code != http.StatusOK || out["from"] != "HUMAN" || out["to"] != "BOT" || out["changed"] != true || out["user_id"] != id {
		t.Fatalf("set: %d %v", code, out)
	}
	if store.user.Kind != domain.KindBot || len(store.changes) != 1 || store.changes[0].Actor != "ops@example.com" || len(store.events) != 1 {
		t.Fatalf("kept %+v %+v %d events", store.user, store.changes, len(store.events))
	}
	if code, out := put(id, `{"kind":"BOT","reason":"again"}`); code != http.StatusOK || out["changed"] != false || len(store.changes) != 1 {
		t.Fatalf("the kind it has: %d %v, %d changes", code, out, len(store.changes))
	}
	if code, _ := put(id, `{"kind":"TEST","reason":"end-to-end"}`); code != http.StatusOK || store.changes[1].Actor != "internal" {
		t.Fatalf("no actor: %d %+v", code, store.changes)
	}
	for _, bad := range []string{
		`{"kind":"ROBOT","reason":"x"}`,
		`{"kind":"BOT"}`,
		`{"kind":"BOT","reason":"` + strings.Repeat("x", 201) + `"}`,
		`{"kind":"BOT","reason":"x","note":"y"}`,
		`kind=BOT`,
	} {
		if code, out := put(id, bad); code != http.StatusBadRequest {
			t.Errorf("%.40s: %d %v", bad, code, out)
		}
	}
	if code, out := put("0192f0c4-8a3e-7b2d-9c1f-000000000000", `{"kind":"BOT","reason":"x"}`); code != http.StatusNotFound || out["code"] != "COMMON_NOT_FOUND" {
		t.Fatalf("an unknown account: %d %v", code, out)
	}
	if code, _ := put(id, `{"kind":"BOT","reason":"x"}`, "X-User-Id", id); code != http.StatusNotFound {
		t.Fatalf("through the gateway: %d", code)
	}
	if len(store.changes) != 2 {
		t.Fatalf("the refused ones changed nothing: %+v", store.changes)
	}
}
