package postgres_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/admin/transport/httpapi"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/password"
	"github.com/skill/exchange/internal/platform/secretbox"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/internal/platform/totp"
	"github.com/skill/exchange/migrations"
)

// TestConsoleAccess: the console's access switches on Postgres (N1) -
// none until stored, stored once by Init, changed by Put under a lock, the
// restriction's list kept as normalized text - an administrator's bound
// authenticator kept and cleared, and the restriction refusing the API to
// other addresses (X-Real-IP from a trusted proxy, here the loopback).
func TestConsoleAccess(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("admin-test", "t"))
	r := store.Read()
	if a, err := r.Access().Get(ctx); err != nil || a != nil {
		t.Fatalf("before any: %+v %v", a, err)
	}
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	first := domain.ConsoleAccess{RequireTOTP: false, UpdatedBy: "migration:admin.login_without_totp", UpdatedAt: at}
	if stored, err := r.Access().Init(ctx, first); err != nil || !stored {
		t.Fatalf("init %t %v", stored, err)
	}
	if stored, err := r.Access().Init(ctx, domain.ConsoleAccess{RequireTOTP: true, UpdatedBy: "another", UpdatedAt: at}); err != nil || stored {
		t.Fatalf("init again %t %v", stored, err)
	}
	if a, err := r.Access().Get(ctx); err != nil || a == nil || a.RequireTOTP || a.UpdatedBy != first.UpdatedBy || !a.UpdatedAt.Equal(at) {
		t.Fatalf("stored once %+v %v", a, err)
	}
	err := store.Tx(ctx, func(tx ports.Repos) error {
		a, err := tx.Access().GetForUpdate(ctx)
		if err != nil || a == nil {
			t.Fatalf("locked %+v %v", a, err)
		}
		return tx.Access().Put(ctx, domain.ConsoleAccess{RequireTOTP: true, UpdatedBy: "boss@example.com", UpdatedAt: at.Add(time.Minute)})
	})
	if err != nil {
		t.Fatal(err)
	}
	if a, err := r.Access().Get(ctx); err != nil || !a.RequireTOTP || a.UpdatedBy != "boss@example.com" || !a.UpdatedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("changed %+v %v", a, err)
	}
	list, err := domain.ParseAllowlist([]string{"203.0.113.0/24", "2001:db8:1:2::/64", "198.51.100.7"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Access().Put(ctx, domain.ConsoleAccess{RequireTOTP: false, Restricted: true, Allowlist: list, UpdatedBy: "boss@example.com", UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if a, err := r.Access().Get(ctx); err != nil || a.RequireTOTP || !a.Restricted || !slices.Equal(a.Allowlist, list) {
		t.Fatalf("restricted %+v %v", a, err)
	}
	if err := r.Access().Put(ctx, domain.ConsoleAccess{Restricted: true, UpdatedBy: "x", UpdatedAt: at}); err == nil {
		t.Fatal("on with an empty list was stored")
	}

	// The API refuses an address outside the list, sign-in included; the list's pass.
	svc := &application.Service{Store: store, Log: log, Now: time.Now}
	if err := svc.LoadAccess(ctx); err != nil {
		t.Fatal(err)
	}
	rt := httpx.NewRouter(httpx.RouterOptions{Logger: log})
	(&httpapi.Handler{Svc: svc, Secure: true}).Routes(rt)
	srv := httptest.NewServer(rt)
	defer srv.Close()
	from := func(ip, method, path string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, strings.NewReader(`{}`))
		req.Header.Set("X-Real-IP", ip)
		req.Header.Set(httpapi.CSRFHeader, "1")
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		return res.StatusCode, body.Code
	}
	for _, c := range []struct {
		ip, method, path string
		status           int
		code             string
	}{
		{"192.0.2.1", http.MethodGet, "/admin/v1/login-options", http.StatusForbidden, "ADMIN_ACCESS_DENIED"},
		{"192.0.2.1", http.MethodPost, "/admin/v1/login", http.StatusForbidden, "ADMIN_ACCESS_DENIED"},
		{"2001:db8:1:3::1", http.MethodGet, "/admin/v1/me", http.StatusForbidden, "ADMIN_ACCESS_DENIED"},
		{"203.0.113.9", http.MethodGet, "/admin/v1/login-options", http.StatusOK, ""},
		{"2001:db8:1:2:aaaa::1", http.MethodGet, "/admin/v1/login-options", http.StatusOK, ""},
		{"198.51.100.7", http.MethodGet, "/admin/v1/me", http.StatusUnauthorized, "ADMIN_UNAUTHORIZED"},
	} {
		if status, code := from(c.ip, c.method, c.path); status != c.status || code != c.code {
			t.Fatalf("%s %s from %s: %d %s, want %d %s", c.method, c.path, c.ip, status, code, c.status, c.code)
		}
	}

	// An administrator's authenticator, bound and then not.
	box, err := secretbox.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	hasher := password.NewHasher(1, password.Cost{MemoryKiB: 64, Iterations: 1})
	a, err := application.NewAdmin(ctx, store, hasher, box, "boss@example.com", "Boss", domain.RoleAdmin, "a long password", totp.NewSecret(),
		"test", false, at)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Admins().Get(ctx, a.ID); err != nil || got.TOTPBound() {
		t.Fatalf("a new one is not bound %+v %v", got, err)
	}
	a.TOTPConfirmedAt = at.Add(2 * time.Minute)
	if err := r.Admins().Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Admins().Get(ctx, a.ID); err != nil || !got.TOTPConfirmedAt.Equal(a.TOTPConfirmedAt) {
		t.Fatalf("bound %+v %v", got, err)
	}
	a.TOTPConfirmedAt = time.Time{}
	if err := r.Admins().Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Admins().Get(ctx, a.ID); err != nil || got.TOTPBound() {
		t.Fatalf("cleared %+v %v", got, err)
	}
}
