package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/internal/wallet/adapters/postgres"
	"github.com/skill/exchange/internal/wallet/application"
	"github.com/skill/exchange/internal/wallet/domain"
	"github.com/skill/exchange/internal/wallet/ports"
	"github.com/skill/exchange/migrations"
)

// The three lists' POST .../list variants (L2, review IY): the accounts in
// the body, the list's other parameters in the query string as for its
// GET. Accounts named wrongly - in the query string, both sets, no UUID,
// no JSON object - are refused before anything is read; a good body gets
// as far as the list's own checks.
func TestTheListsTakeTheirAccountsInABody(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{Svc: &application.Service{}}).Routes(r)
	send := func(path, body string) (int, httpx.ErrorBody) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body)))
		var e httpx.ErrorBody
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		return w.Code, e
	}
	const a = "0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e"
	for _, list := range []string{"/internal/wallet/withdrawals/list", "/internal/wallet/deposits/list", "/internal/wallet/custody/fees/list"} {
		for _, c := range []struct{ query, body string }{
			{"?user_ids=" + a, `{}`},
			{"", `{"user_ids":["` + a + `"],"exclude_user_ids":[]}`},
			{"", `{"exclude_user_ids":["nope"]}`},
			{"", `{"user_ids":"` + a + `"}`},
			{"", `[]`},
			{"", ``},
		} {
			if status, e := send(list+c.query, c.body); status != http.StatusBadRequest || e.Code != apperr.CodeInvalidArgument {
				t.Errorf("%s%s %s: %d %+v", list, c.query, c.body, status, e)
			}
		}
	}
	if status, e := send("/internal/wallet/withdrawals/list?held=maybe", `{"exclude_user_ids":["`+a+`"]}`); status != http.StatusBadRequest ||
		!strings.Contains(e.Message, "held") {
		t.Errorf("a good body, then the list's own check: %d %+v", status, e)
	}
	if status, _ := send("/internal/wallet/custody/fees/list?provider=FOO", `{"user_ids":["`+a+`"]}`); status != http.StatusNotFound {
		t.Errorf("a good body, then an unknown custodian: %d", status)
	}
}

// TestTheDepositListByBody pages the console's deposits narrowed to and
// past accounts named in the body, a page at a time by the cursor in the
// query string.
func TestTheDepositListByBody(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Wallet(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("wallet-test", "t"))
	r := chi.NewRouter()
	(&Handler{Svc: &application.Service{Store: store, Log: log, Now: time.Now}}).Routes(r)
	alice, bob := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	deposit := func(user string, at time.Time) string {
		t.Helper()
		d := domain.Deposit{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Asset: "USDT", Network: "TRON", Address: "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7",
			TxHash: "tx-" + uuid.NewString(), LogIndex: domain.NativeLog, BlockNumber: 7, Amount: decimal.NewFromInt(5), RawAmount: decimal.NewFromInt(5_000_000),
			Confirmations: 1, Required: 20, Status: domain.StatusConfirming, ProviderTxID: "UDUNMOCK:" + uuid.NewString(), DetectedAt: at,
		}
		if err := store.Tx(ctx, func(r ports.Repos) error { return r.Deposits().Insert(ctx, d) }); err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	hers, his := deposit(alice, now), deposit(bob, now.Add(time.Second))
	list := func(query, body string) ([]string, *string) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/wallet/deposits/list"+query, strings.NewReader(body)))
		var out struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Next *string `json:"next_cursor"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("%s %s: %d %s", query, body, w.Code, w.Body.String())
		}
		ids := make([]string, 0, len(out.Items))
		for _, d := range out.Items {
			ids = append(ids, d.ID)
		}
		return ids, out.Next
	}
	both := `{"user_ids":["` + alice + `","` + strings.ToUpper(bob) + `","` + alice + `"]}`
	if ids, next := list("", both); len(ids) != 2 || ids[0] != his || ids[1] != hers || next != nil {
		t.Fatalf("theirs, newest first: %v %v", ids, next)
	}
	first, next := list("?limit=1", both)
	if len(first) != 1 || first[0] != his || next == nil {
		t.Fatalf("a page of one: %v %v", first, next)
	}
	if rest, _ := list("?limit=1&cursor="+*next, both); len(rest) != 1 || rest[0] != hers {
		t.Fatalf("the next page: %v", rest)
	}
	if ids, _ := list("", `{"user_ids":[]}`); len(ids) != 0 {
		t.Fatalf("an empty list keeps nobody: %v", ids)
	}
	if ids, _ := list("?user_id="+alice, `{"exclude_user_ids":["`+alice+`"]}`); len(ids) != 0 {
		t.Fatalf("past her: %v", ids)
	}
	if ids, _ := list("?user_id="+alice, `{"exclude_user_ids":["`+bob+`"]}`); len(ids) != 1 || ids[0] != hers {
		t.Fatalf("past him: %v", ids)
	}
}
