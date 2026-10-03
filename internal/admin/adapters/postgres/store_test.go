package postgres_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/admin/application"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/admin/transport/httpapi"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/password"
	"github.com/lidp280504357/exchange/internal/platform/secretbox"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/internal/platform/totp"
	"github.com/lidp280504357/exchange/migrations"
)

type ledger struct {
	keys []string
	// err answers the adjustments while set (the ledger down).
	err error
}

func (l *ledger) Adjust(_ context.Context, key, _, _, _ string, _ decimal.Decimal, _, _ string) (string, error) {
	l.keys = append(l.keys, key)
	if l.err != nil {
		return "", l.err
	}
	return "j1", nil
}

func (l *ledger) PlaceHold(context.Context, string, string, string, decimal.Decimal, string, string) (ports.Hold, error) {
	return ports.Hold{}, nil
}

func (l *ledger) ReleaseHold(context.Context, string, string, string) (ports.Hold, error) {
	return ports.Hold{}, nil
}

func (l *ledger) Holds(context.Context, string, bool) ([]ports.Hold, error) { return nil, nil }

func (l *ledger) FundInsurance(_ context.Context, key, _ string, _ decimal.Decimal, _, _ string) (string, error) {
	l.keys = append(l.keys, key)
	return "j2", nil
}

func (l *ledger) SystemBalances(context.Context, string) ([]ports.Balance, error) { return nil, nil }

// wallet has an empty review queue.
type wallet struct{}

func (wallet) Detail(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (wallet) Hold(context.Context, string, bool, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (wallet) List(context.Context, ports.WithdrawalQuery) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[],"next_cursor":null}`), nil
}

func (wallet) Review(context.Context, string, bool, string, string, decimal.Decimal, int) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (wallet) Custody(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

func (wallet) Callbacks(context.Context, ports.CallbackQuery) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[]}`), nil
}

func (wallet) Callback(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (wallet) Replay(context.Context, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (wallet) Suspensions(context.Context) ([]ports.Suspension, error) {
	return []ports.Suspension{}, nil
}

func (wallet) Resume(context.Context, string, string, string) (ports.Suspension, error) {
	return ports.Suspension{}, nil
}

// deposits has no deposit waiting for a decision.
type deposits struct{}

func (deposits) List(context.Context, ports.DepositReviewQuery) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[],"next_cursor":null}`), nil
}

func (deposits) Get(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (deposits) Credit(context.Context, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (deposits) Dismiss(context.Context, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (deposits) CheckManual(context.Context, ports.ManualDeposit) (ports.ManualCheck, error) {
	return ports.ManualCheck{}, nil
}

func (deposits) BookManual(context.Context, ports.ManualDeposit, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// users knows every account.
type users struct{}

func (users) Find(context.Context, string) (string, error) { return "", nil }

func (users) Get(_ context.Context, id string) (ports.User, error) {
	return ports.User{ID: id, Status: "ACTIVE", Region: "SG", Language: "en", CreatedAt: time.Now()}, nil
}

func (users) Balances(context.Context, string) ([]ports.Balance, error) { return nil, nil }

func (users) ChangeStatus(context.Context, string, string, string, string, string) (string, error) {
	return "ACTIVE", nil
}

func (users) List(context.Context, ports.UserQuery) ([]ports.User, string, error) {
	return nil, "", nil
}

func (users) Stats(context.Context, time.Time, int) (ports.UserStats, error) {
	return ports.UserStats{}, nil
}

// stream opens the console's event stream and passes on its lines until
// the test ends.
func (c *client) stream(ctx context.Context) <-chan string {
	c.t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.srv.URL+"/admin/v1/events", nil)
	req.AddCookie(c.cookie)
	res, err := c.srv.Client().Do(req) //nolint:bodyclose // the reader below closes it
	if err != nil {
		c.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		_ = res.Body.Close()
		c.t.Fatalf("events: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	lines := make(chan string, 64)
	go func() {
		defer res.Body.Close()
		defer close(lines)
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	return lines
}

// await reads the stream until a line, failing after 10 seconds.
func await(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("the stream ended before %q", want)
			}
			if strings.HasPrefix(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no %q on the stream", want)
		}
	}
}

type client struct {
	t      *testing.T
	srv    *httptest.Server
	cookie *http.Cookie
	// header is the last answer's.
	header http.Header
	// key is the next write's Idempotency-Key (a new one when empty).
	key string
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
	if method != http.MethodGet {
		key := c.key
		if key == "" {
			key = uuid.NewString()
		}
		c.key = ""
		req.Header.Set(httpapi.KeyHeader, key)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	res, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	c.header = res.Header
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
	// No flags: two-person approval is off (single-person mode).
	svc := &application.Service{
		Store: store, Hasher: hasher, Box: box, Ledger: led, Wallet: wallet{}, Deposits: deposits{}, Users: users{}, Log: log, Now: time.Now,
	}
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
	(&httpapi.Handler{Svc: svc, Secure: true, EventsEvery: 50 * time.Millisecond}).Routes(r)
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
	// An insurance fund contribution is a request of its own kind.
	status, body = fin.do(http.MethodPost, "/admin/v1/derivatives/insurance-fund/contributions",
		map[string]string{"amount": "1000", "reason": "after the drill"}, true)
	if status != http.StatusCreated || body["kind"] != domain.KindInsuranceFund || body["status"] != domain.ApprovalPending {
		t.Fatalf("insurance contribution: %d %v", status, body)
	}
	fund, _ := body["id"].(string)
	status, body = boss.do(http.MethodPost, "/admin/v1/approvals/"+fund+"/decide", map[string]any{"approve": true, "reason": "checked"}, true)
	if status != http.StatusOK || body["status"] != domain.ApprovalExecuted || len(led.keys) != 2 || led.keys[1] != "approval:"+fund {
		t.Fatalf("insurance approval: %d %v (ledger %v)", status, body, led.keys)
	}

	// Single-person mode: an adjustment from the user's page is booked at once.
	user := "/admin/v1/users/01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b/adjustments"
	status, body = boss.do(http.MethodPost, user, map[string]string{"asset": "usdt", "amount": "12.5", "reason": "goodwill", "reference": "T-7"}, true)
	if status != http.StatusCreated || body["status"] != domain.ApprovalExecuted || body["mode"] != domain.ModeSingle || body["journal_id"] != "j1" ||
		body["decided_by_email"] != "boss@example.com" || body["value_usdt"] != "12.5" || len(led.keys) != 3 {
		t.Fatalf("single-person adjustment: %d %v", status, body)
	}
	status, body = boss.do(http.MethodGet, "/admin/v1/settings", nil, false)
	if status != http.StatusOK || body["two_person_approval"] != false || body["single_max_usdt"] != "100000" || body["daily_used_usdt"] != "12.5" {
		t.Fatalf("settings: %d %v", status, body)
	}
	if status, body := fin.do(http.MethodPut, "/admin/v1/settings", map[string]string{"single_max_usdt": "10", "reason": "cautious"}, true); status != http.StatusForbidden {
		t.Fatalf("finance changes the settings: %d %v", status, body)
	}
	if status, body := boss.do(http.MethodPut, "/admin/v1/settings", map[string]string{"single_max_usdt": "10", "reason": "cautious"}, true); status != http.StatusOK || body["single_max_usdt"] != "10" || body["updated_by"] != "boss@example.com" {
		t.Fatalf("settings changed: %d %v", status, body)
	}
	// Above the limit it waits for a second administrator; its requester may withdraw it.
	status, body = boss.do(http.MethodPost, user, map[string]string{"asset": "USDT", "amount": "-20", "reason": "fee correction"}, true)
	if status != http.StatusCreated || body["status"] != domain.ApprovalPending || body["escalation"] != domain.EscalationSingleMax {
		t.Fatalf("over the limit: %d %v", status, body)
	}
	over, _ := body["id"].(string)
	status, body = boss.do(http.MethodPost, "/admin/v1/approvals/"+over+"/decide", map[string]any{"approve": false, "reason": "withdrawn"}, true)
	if status != http.StatusOK || body["status"] != domain.ApprovalRejected || body["decided_by_email"] != "boss@example.com" {
		t.Fatalf("withdrawn: %d %v", status, body)
	}
	// A money route wants an Idempotency-Key; the same request with it is the same operation (C5.5 ⑥).
	boss.key = " "
	if status, body := boss.do(http.MethodPost, user, map[string]string{"asset": "USDT", "amount": "1", "reason": "goodwill"}, true); status != http.StatusBadRequest {
		t.Fatalf("without a key: %d %v", status, body)
	}
	once := map[string]string{"asset": "USDT", "amount": "2", "reason": "goodwill"}
	boss.key = "adjust-once"
	status, body = boss.do(http.MethodPost, user, once, true)
	first, _ := body["id"].(string)
	boss.key = "adjust-once"
	if again, body := boss.do(http.MethodPost, user, once, true); status != http.StatusCreated || again != http.StatusCreated || body["id"] != first ||
		body["status"] != domain.ApprovalExecuted || len(led.keys) != 4 {
		t.Fatalf("repeated: %d %d %v (ledger %v)", status, again, body, led.keys)
	}
	boss.key = "adjust-once"
	if status, body := boss.do(http.MethodPost, user, map[string]string{"asset": "USDT", "amount": "3", "reason": "goodwill"}, true); status != http.StatusConflict ||
		errCode(body) != "COMMON_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("the key with another amount: %d %v", status, body)
	}
	// The ledger does not answer: the operation stays pending, attempted, to be finished, never withdrawn.
	led.err = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "down")
	status, body = boss.do(http.MethodPost, user, map[string]string{"asset": "USDT", "amount": "4", "reason": "goodwill"}, true)
	details, _ := body["details"].(map[string]any)
	stuck, _ := details["approval_id"].(string)
	if status != http.StatusServiceUnavailable || stuck == "" {
		t.Fatalf("unknown outcome: %d %v", status, body)
	}
	_, body = boss.do(http.MethodGet, "/admin/v1/approvals?status=PENDING", nil, false)
	items, _ := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != stuck || items[0].(map[string]any)["attempted_at"] == nil ||
		items[0].(map[string]any)["result"] != "COMMON_UNAVAILABLE: down" {
		t.Fatalf("attempted: %v", body)
	}
	if status, body := boss.do(http.MethodPost, "/admin/v1/approvals/"+stuck+"/decide", map[string]any{"approve": false, "reason": "never mind"}, true); status != http.StatusConflict ||
		errCode(body) != "ADMIN_APPROVAL_ATTEMPTED" {
		t.Fatalf("withdrawn after an attempt: %d %v", status, body)
	}
	led.err = nil
	status, body = boss.do(http.MethodPost, "/admin/v1/approvals/"+stuck+"/decide", map[string]any{"approve": true, "reason": "the ledger is back"}, true)
	if status != http.StatusOK || body["status"] != domain.ApprovalExecuted || body["attempted_at"] == nil || len(led.keys) != 6 || led.keys[5] != "approval:"+stuck {
		t.Fatalf("finished: %d %v (ledger %v)", status, body, led.keys)
	}
	// Notes and tags on an account.
	account := "/admin/v1/users/01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b"
	if status, body := boss.do(http.MethodPost, account+"/notes", map[string]string{"body": "called about a deposit"}, true); status != http.StatusCreated || body["admin_email"] != "boss@example.com" {
		t.Fatalf("a note: %d %v", status, body)
	}
	if status, body := fin.do(http.MethodPost, account+"/notes", map[string]string{"body": "refund promised"}, true); status != http.StatusCreated {
		t.Fatalf("a second note: %d %v", status, body)
	}
	status, body = boss.do(http.MethodGet, account+"/notes?limit=1", nil, false)
	if items, _ := body["items"].([]any); status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["body"] != "refund promised" || body["next_cursor"] == nil {
		t.Fatalf("notes, newest first: %d %v", status, body)
	}
	if status, body := boss.do(http.MethodPut, account+"/tags", map[string]any{"tags": []string{"vip", "test"}}, true); status != http.StatusOK ||
		fmt.Sprint(body["tags"]) != "[TEST VIP]" {
		t.Fatalf("tags: %d %v", status, body)
	}
	if status, body := boss.do(http.MethodGet, account, nil, false); status != http.StatusOK || fmt.Sprint(body["tags"]) != "[TEST VIP]" {
		t.Fatalf("the account with its tags: %d %v", status, body)
	}
	if status, body := boss.do(http.MethodPut, account+"/tags", map[string]any{"tags": []string{}}, true); status != http.StatusOK ||
		fmt.Sprint(body["tags"]) != "[]" {
		t.Fatalf("no tags: %d %v", status, body)
	}

	// The ADMIN creates an OPERATOR, who signs in with what came back once;
	// its live session shows, and ends with the ADMIN's word.
	status, body = boss.do(http.MethodPost, "/admin/v1/admins", map[string]string{"email": "ops@example.com", "name": "Ops", "role": "operator", "reason": "new hire"}, true)
	pw, _ := body["password"].(string)
	sec, _ := body["totp_secret"].(string)
	created, _ := body["admin"].(map[string]any)
	if status != http.StatusCreated || boss.header.Get("Cache-Control") != "no-store" || len(pw) < 20 || sec == "" || created["role"] != domain.RoleOperator {
		t.Fatalf("create: %d %v", status, created)
	}
	opsID, _ := created["id"].(string)
	opsSecret, err := totp.Decode(sec)
	if err != nil {
		t.Fatal(err)
	}
	ops := &client{t: t, srv: srv}
	if status, body := ops.do(http.MethodPost, "/admin/v1/login", map[string]string{"email": "ops@example.com", "password": pw, "totp_code": totp.Code(opsSecret, totp.Step(time.Now()))}, true); status != http.StatusOK {
		t.Fatalf("the new administrator signs in: %d %v", status, body)
	}
	status, body = boss.do(http.MethodGet, "/admin/v1/admins/"+opsID+"/sessions", nil, false)
	if live, _ := body["sessions"].([]any); status != http.StatusOK || len(live) != 1 {
		t.Fatalf("its sessions: %d %v", status, body)
	}
	status, body = boss.do(http.MethodGet, "/admin/v1/admins", nil, false)
	if list, _ := body["admins"].([]any); status != http.StatusOK || len(list) != 3 || !strings.Contains(fmt.Sprint(list), "sessions:1") {
		t.Fatalf("the administrators: %d %v", status, body)
	}
	if status, body := fin.do(http.MethodGet, "/admin/v1/admins", nil, false); status != http.StatusForbidden {
		t.Fatalf("finance lists the administrators: %d %v", status, body)
	}
	if status, _ := boss.do(http.MethodPost, "/admin/v1/admins/"+opsID+"/sessions/revoke", map[string]string{"reason": "laptop lost"}, true); status != http.StatusNoContent {
		t.Fatalf("revoke: %d", status)
	}
	if status, _ := ops.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("me after the sessions ended: %d", status)
	}

	// The event stream counts what waits and ends with the session.
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	lines := fin.stream(streamCtx)
	await(t, lines, `data: {"withdrawals":0,"approvals":0`)
	if err := application.DisableAdmin(ctx, store, "fin@example.com", "test", "left the team", time.Now()); err != nil {
		t.Fatal(err)
	}
	await(t, lines, "event: signed_out")

	if status, _ := boss.do(http.MethodPost, "/admin/v1/logout", nil, true); status != http.StatusNoContent {
		t.Fatalf("logout: %d", status)
	}
	if status, _ := boss.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d", status)
	}
	if status, _ := fin.do(http.MethodGet, "/admin/v1/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("me after disable: %d", status)
	}
	// Every change went to the outbox as an audit event.
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE topic = 'audit.events'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// created ×2, login ×2, login_failed, requested ×2, approved ×2, single-person requested and executed,
	// settings changed, requested and withdrawn, the keyed one requested and executed, the unknown one
	// requested, unfinished and approved, notes ×2, tags ×2, the OPERATOR created, signed in and its
	// sessions ended, disabled, logout
	if n != 28 {
		t.Fatalf("%d audit events in the outbox, want 28", n)
	}
	var admins string
	if err := db.QueryRow(ctx, `SELECT string_agg(email || ':' || status || ':' || failed_attempts, ',' ORDER BY email) FROM admins`).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(admins, "boss@example.com:ACTIVE:0") || !strings.Contains(admins, "fin@example.com:DISABLED:1") {
		t.Fatalf("admins %s", admins)
	}
}
