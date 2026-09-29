package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/admin/application"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/transport/httpapi"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/password"
	"github.com/lidp280504357/exchange/internal/platform/secretbox"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/internal/platform/totp"
	"github.com/lidp280504357/exchange/migrations"
)

type ledger struct{ keys []string }

func (l *ledger) Adjust(_ context.Context, key, _, _ string, _ decimal.Decimal, _, _ string) (string, error) {
	l.keys = append(l.keys, key)
	return "j1", nil
}

type client struct {
	t      *testing.T
	srv    *httptest.Server
	cookie *http.Cookie
}

func (c *client) do(method, path string, body any, csrf bool) (int, map[string]any) {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, c.srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set(httpapi.CSRFHeader, "1")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	res, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	for _, ck := range res.Cookies() {
		if ck.Name == httpapi.CookieName {
			c.cookie = ck
		}
	}
	out := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func errCode(body map[string]any) string {
	s, _ := body["code"].(string)
	return s
}

func TestConsole(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), log); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("admin-test", "t"))
	hasher := password.NewHasher(1, password.Cost{MemoryKiB: 64, Iterations: 1})
	led := &ledger{}
	svc := &application.Service{Store: store, Hasher: hasher, Box: box, Ledger: led, Log: log, Now: time.Now}
	secrets := map[string][]byte{}
	for _, a := range []struct{ email, role string }{{"fin@example.com", domain.RoleFinance}, {"boss@example.com", domain.RoleAdmin}} {
		secrets[a.email] = totp.NewSecret()
		if _, err := application.NewAdmin(ctx, store, hasher, box, a.email, "Test", a.role, "a long password", secrets[a.email], "test",
			time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := application.NewAdmin(ctx, store, hasher, box, "FIN@example.com", "Dup", domain.RoleAuditor, "a long password",
		totp.NewSecret(), "test", time.Now()); err == nil {
		t.Fatal("a second account with the same address (in another case) was created")
	}
	r := httpx.NewRouter(httpx.RouterOptions{Logger: log})
	(&httpapi.Handler{Svc: svc, Secure: true}).Routes(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	codes := map[string]string{}
	login := func(email string) *client {
		c := &client{t: t, srv: srv}
		code := totp.Code(secrets[email], totp.Step(time.Now()))
		codes[email] = code
		if status, body := c.do(http.MethodPost, "/admin/v1/login", map[string]string{"email": email, "password": "a long password", "totp_code": code}, false); status != http.StatusForbidden || errCode(body) != "ADMIN_CSRF" {
			t.Fatalf("login without the CSRF header: %d %v", status, body)
		}
		status, body := c.do(http.MethodPost, "/admin/v1/login", map[string]string{"email": email, "password": "a long password", "totp_code": code}, true)
		if status != http.StatusOK || c.cookie == nil {
			t.Fatalf("login %s: %d %v", email, status, body)
		}
		if !c.cookie.HttpOnly || !c.cookie.Secure || c.cookie.SameSite != http.SameSiteStrictMode || c.cookie.Path != "/admin/" {
			t.Fatalf("cookie %+v", c.cookie)
		}
		return c
	}
	anon := &client{t: t, srv: srv}
	if status, body := anon.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusUnauthorized || errCode(body) != "ADMIN_UNAUTHORIZED" {
		t.Fatalf("me without a session: %d %v", status, body)
	}
	fin := login("fin@example.com")
	if status, body := fin.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusOK || body["role"] != domain.RoleFinance {
		t.Fatalf("me: %d %v", status, body)
	}
	// The same code signs in only once (the last step is stored).
	if status, _ := anon.do(http.MethodPost, "/admin/v1/login", map[string]string{"email": "fin@example.com", "password": "a long password", "totp_code": codes["fin@example.com"]}, true); status != http.StatusUnauthorized {
		t.Fatalf("replayed code: %d", status)
	}
	adj := map[string]string{"user_id": "01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b", "asset": "USDT", "amount": "7.25", "reason": "goodwill"}
	status, body := fin.do(http.MethodPost, "/admin/v1/ledger/adjustments", adj, true)
	if status != http.StatusCreated || body["status"] != domain.ApprovalPending {
		t.Fatalf("request: %d %v", status, body)
	}
	id, _ := body["id"].(string)
	if status, body := fin.do(http.MethodPost, "/admin/v1/approvals/"+id+"/decide", map[string]any{"approve": true, "reason": "mine"}, true); status != http.StatusForbidden || errCode(body) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("self approval: %d %v", status, body)
	}
	boss := login("boss@example.com")
	status, body = boss.do(http.MethodGet, "/admin/v1/approvals?status=PENDING", nil, false)
	if items, _ := body["items"].([]any); status != http.StatusOK || len(items) != 1 {
		t.Fatalf("pending: %d %v", status, body)
	}
	status, body = boss.do(http.MethodPost, "/admin/v1/approvals/"+id+"/decide", map[string]any{"approve": true, "reason": "checked the ticket"}, true)
	if status != http.StatusOK || body["status"] != domain.ApprovalExecuted || body["decided_by"] == nil || len(led.keys) != 1 || led.keys[0] != "approval:"+id {
		t.Fatalf("approve: %d %v (ledger %v)", status, body, led.keys)
	}
	if status, body := boss.do(http.MethodPost, "/admin/v1/approvals/"+id+"/decide", map[string]any{"approve": false, "reason": "again"}, true); status != http.StatusConflict || errCode(body) != "ADMIN_APPROVAL_DECIDED" {
		t.Fatalf("second decision: %d %v", status, body)
	}
	if status, _ := boss.do(http.MethodPost, "/admin/v1/logout", nil, true); status != http.StatusNoContent {
		t.Fatalf("logout: %d", status)
	}
	if status, _ := boss.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d", status)
	}
	if err := application.DisableAdmin(ctx, store, "fin@example.com", "test", "left the team", time.Now()); err != nil {
		t.Fatal(err)
	}
	if status, _ := fin.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("me after disable: %d", status)
	}
	// Every change went to the outbox as an audit event.
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE topic = 'audit.events'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// created ×2, login ×2, login_failed, requested, approved, logout, disabled
	if n != 9 {
		t.Fatalf("%d audit events in the outbox, want 9", n)
	}
	var admins string
	if err := db.QueryRow(ctx, `SELECT string_agg(email || ':' || status || ':' || failed_attempts, ',' ORDER BY email) FROM admins`).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(admins, "boss@example.com:ACTIVE:0") || !strings.Contains(admins, "fin@example.com:DISABLED:1") {
		t.Fatalf("admins %s", admins)
	}
}
