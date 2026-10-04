package postgres_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/transport/httpapi"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/password"
	"github.com/skill/exchange/internal/platform/secretbox"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/internal/platform/totp"
	"github.com/skill/exchange/migrations"
)

// noCodes is admin.login_without_totp on: the same administrator signs in
// more than once within a code's 30 seconds here.
type noCodes struct{}

func (noCodes) Enabled(key string, _ flags.Subject) bool { return key == flags.KeyAdminNoTOTP }

// TestSetupLinksAndOwnCredentials: over HTTP on Postgres, a new account
// and a reset give a one-time link (no-store) whose holder sets the
// credentials without a session; an administrator whose password was
// generated for them is held to changing it; changing one's own ends the
// other sessions; two ADMINs demoting each other leave one (C5.5 ⑪).
func TestSetupLinksAndOwnCredentials(t *testing.T) {
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
	svc := &application.Service{Store: store, Hasher: hasher, Box: box, Features: noCodes{}, Log: log, Now: time.Now}
	for _, email := range []string{"boss@example.com", "deputy@example.com"} {
		if _, err := application.NewAdmin(ctx, store, hasher, box, email, "Test", domain.RoleAdmin, "a long password", totp.NewSecret(), "test",
			false, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	r := httpx.NewRouter(httpx.RouterOptions{Logger: log})
	(&httpapi.Handler{Svc: svc, Secure: true}).Routes(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	login := func(email, pw string) (*client, int, map[string]any) {
		c := &client{t: t, srv: srv}
		status, body := c.do(http.MethodPost, "/admin/v1/login", map[string]string{"email": email, "password": pw}, true)
		return c, status, body
	}
	boss, status, _ := login("boss@example.com", "a long password")
	if status != http.StatusOK {
		t.Fatalf("boss signs in: %d", status)
	}
	anon := &client{t: t, srv: srv}

	// A new OPERATOR: the link, shown once, is all the answer has.
	hire := map[string]string{"email": "ops@example.com", "name": "Ops", "role": "OPERATOR", "reason": "a new operator"}
	status, body := boss.do(http.MethodPost, "/admin/v1/admins", hire, true)
	setup, _ := body["setup"].(map[string]any)
	token, _ := setup["token"].(string)
	if status != http.StatusCreated || setup["kind"] != domain.SetupCreate || len(token) < 40 || body["password"] != nil ||
		boss.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("created %d %v %v", status, body, boss.header)
	}
	if status, body := anon.do(http.MethodPost, "/admin/v1/setup/inspect", map[string]string{"token": token}, false); status != http.StatusForbidden ||
		errCode(body) != "ADMIN_CSRF" {
		t.Fatalf("inspect without the CSRF header: %d %v", status, body)
	}
	status, body = anon.do(http.MethodPost, "/admin/v1/setup/inspect", map[string]string{"token": token}, true)
	secret, _ := body["totp_secret"].(string)
	if status != http.StatusOK || body["email"] != "ops@example.com" || body["sets_password"] != true || secret == "" ||
		anon.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("inspect %d %v", status, body)
	}
	raw, err := totp.Decode(secret)
	if err != nil {
		t.Fatal(err)
	}
	code := totp.Code(raw, totp.Step(time.Now()))
	if status, body := anon.do(http.MethodPost, "/admin/v1/setup", map[string]string{"token": token, "password": "short", "totp_code": code},
		true); status != http.StatusBadRequest {
		t.Fatalf("a short password: %d %v", status, body)
	}
	own := map[string]string{"token": token, "password": "the operator's own", "totp_code": code}
	if status, body := anon.do(http.MethodPost, "/admin/v1/setup", own, true); status != http.StatusNoContent {
		t.Fatalf("set up: %d %v", status, body)
	}
	if status, body := anon.do(http.MethodPost, "/admin/v1/setup/inspect", map[string]string{"token": token}, true); status != http.StatusNotFound ||
		errCode(body) != "ADMIN_SETUP_INVALID" {
		t.Fatalf("the link spent: %d %v", status, body)
	}
	ops, status, body := login("ops@example.com", "the operator's own")
	if status != http.StatusOK {
		t.Fatalf("ops signs in: %d %v", status, body)
	}
	var opsID string
	if admin, _ := body["admin"].(map[string]any); admin["must_change_password"] != false {
		t.Fatalf("not held: %v", body)
	} else {
		opsID, _ = admin["id"].(string)
	}

	// A password reset ends its sessions; the link sets a new one.
	status, body = boss.do(http.MethodPost, "/admin/v1/admins/"+opsID+"/password-reset", map[string]string{"reason": "forgot it"}, true)
	setup, _ = body["setup"].(map[string]any)
	token, _ = setup["token"].(string)
	if status != http.StatusOK || setup["kind"] != domain.SetupPassword || token == "" {
		t.Fatalf("reset %d %v", status, body)
	}
	if status, _ := ops.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("the reset ends the sessions: %d", status)
	}
	if status, body := anon.do(http.MethodPost, "/admin/v1/setup", map[string]string{"token": token, "password": "the operator's next"}, true); status != http.StatusNoContent {
		t.Fatalf("a new password: %d %v", status, body)
	}

	// One's own password: the current one proves it; the other sessions end.
	ops, _, _ = login("ops@example.com", "the operator's next")
	other, _, _ := login("ops@example.com", "the operator's next")
	if status, body := ops.do(http.MethodPost, "/admin/v1/me/password", map[string]string{"current_password": "wrong", "new_password": "the operator's third"},
		true); status != http.StatusUnprocessableEntity || errCode(body) != "ADMIN_PASSWORD_WRONG" {
		t.Fatalf("a wrong current password: %d %v", status, body)
	}
	if status, body := ops.do(http.MethodPost, "/admin/v1/me/password", map[string]string{
		"current_password": "the operator's next",
		"new_password":     "the operator's third",
	}, true); status != http.StatusNoContent {
		t.Fatalf("own password: %d %v", status, body)
	}
	if status, _ := other.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("the other session goes: %d", status)
	}
	if status, _ := ops.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusOK {
		t.Fatalf("this one stays: %d", status)
	}

	// A generated password (exchangectl admin create) is changed first.
	generated, chosen := "a generated password", "the auditor's own one"
	if _, err := application.NewAdmin(ctx, store, hasher, box, "cli@example.com", "CLI", domain.RoleAuditor, generated, totp.NewSecret(),
		"cli:test", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	cli, status, body := login("cli@example.com", generated)
	if admin, _ := body["admin"].(map[string]any); status != http.StatusOK || admin["must_change_password"] != true {
		t.Fatalf("held: %d %v", status, body)
	}
	if status, body := cli.do(http.MethodGet, "/admin/v1/roles", nil, false); status != http.StatusForbidden || errCode(body) != "ADMIN_PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("nothing else first: %d %v", status, body)
	}
	if status, _ := cli.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusOK {
		t.Fatalf("but who they are: %d", status)
	}
	if status, body := cli.do(http.MethodPost, "/admin/v1/me/password", map[string]string{
		"current_password": generated,
		"new_password":     chosen,
	}, true); status != http.StatusNoContent {
		t.Fatalf("changed: %d %v", status, body)
	}
	if status, body := cli.do(http.MethodGet, "/admin/v1/roles", nil, false); status != http.StatusOK {
		t.Fatalf("free once changed: %d %v", status, body)
	}

	// Two ADMINs demoting each other leave one: the second finds the roster
	// it waited for (or, signed in after the first, is no ADMIN any more).
	deputy, _, _ := login("deputy@example.com", "a long password")
	var deputyID, bossID string
	_, body = deputy.do(http.MethodGet, "/admin/v1/me", nil, false)
	deputyID, _ = body["id"].(string)
	_, body = boss.do(http.MethodGet, "/admin/v1/me", nil, false)
	bossID, _ = body["id"].(string)
	done := make(chan [2]any, 2)
	for _, p := range []struct {
		c      *client
		target string
	}{{boss, deputyID}, {deputy, bossID}} {
		go func() {
			status, body := p.c.do(http.MethodPost, "/admin/v1/admins/"+p.target+"/role", map[string]string{"role": "AUDITOR", "reason": "one too many"}, true)
			done <- [2]any{status, errCode(body)}
		}()
	}
	results := [][2]any{<-done, <-done}
	ok, refused := 0, 0
	for _, r := range results {
		switch {
		case r[0] == http.StatusOK:
			ok++
		case r[1] == "ADMIN_LAST_ADMIN" || r[1] == "ADMIN_FORBIDDEN":
			refused++
		}
	}
	var admins int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM admins WHERE role = 'ADMIN' AND status = 'ACTIVE'`).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if ok != 1 || refused != 1 || admins != 1 {
		t.Fatalf("one demotion and one refusal: %v, %d ADMINs left", results, admins)
	}
}
