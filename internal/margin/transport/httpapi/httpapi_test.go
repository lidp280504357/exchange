package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/adapters/postgres"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/margin/transport/httpapi"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ledger books what it is given on in-memory balances: enough of the
// ledger for the handlers' answers (the application tests check the
// postings' rules).
type ledger struct {
	mu       sync.Mutex
	spot     map[string]map[string]decimal.Decimal
	holdings map[string]map[domain.Account]map[string]*domain.Holding
	keys     map[string][]string
}

func (l *ledger) fund(user, asset string, amount decimal.Decimal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.spot[user] == nil {
		l.spot[user] = map[string]decimal.Decimal{}
	}
	l.spot[user][asset] = l.spot[user][asset].Add(amount)
}

func (l *ledger) Post(_ context.Context, p ports.Posting) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if j, ok := l.keys[p.IdemKey]; ok {
		return j, nil
	}
	if l.holdings[p.UserID] == nil {
		l.holdings[p.UserID] = map[domain.Account]map[string]*domain.Holding{}
	}
	if l.holdings[p.UserID][p.Account] == nil {
		l.holdings[p.UserID][p.Account] = map[string]*domain.Holding{}
	}
	acct := l.holdings[p.UserID][p.Account]
	journals := make([]string, 0, len(p.Moves))
	for _, m := range p.Moves {
		h := acct[m.Asset]
		if h == nil {
			h = &domain.Holding{Asset: m.Asset}
			acct[m.Asset] = h
		}
		switch m.Type {
		case domain.MoveTransferIn:
			if l.spot[p.UserID][m.Asset].LessThan(m.Amount) {
				return nil, apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient spot")
			}
			l.spot[p.UserID][m.Asset] = l.spot[p.UserID][m.Asset].Sub(m.Amount)
			h.Free = h.Free.Add(m.Amount)
		case domain.MoveTransferOut:
			h.Free = h.Free.Sub(m.Amount)
			l.spot[p.UserID][m.Asset] = l.spot[p.UserID][m.Asset].Add(m.Amount)
		case domain.MoveBorrow:
			h.Free, h.Borrowed = h.Free.Add(m.Amount), h.Borrowed.Add(m.Amount)
		case domain.MoveInterest:
			h.Interest = h.Interest.Add(m.Amount)
		case domain.MoveRepay, domain.MoveLiquidationRepay:
			h.Free, h.Interest, h.Borrowed = h.Free.Sub(m.Amount), h.Interest.Sub(m.Interest), h.Borrowed.Sub(m.Amount.Sub(m.Interest))
		default:
			return nil, apperr.Invalid("not booked here: " + string(m.Type))
		}
		journals = append(journals, uuid.Must(uuid.NewV7()).String())
	}
	l.keys[p.IdemKey] = journals
	return journals, nil
}

func (l *ledger) Accrue(context.Context, string, string, string, []ports.Accrual) (string, error) {
	return "", apperr.Invalid("no interest runs here")
}

func (l *ledger) Holdings(_ context.Context, userID string) (map[domain.Account][]domain.Holding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[domain.Account][]domain.Holding{}
	for a, assets := range l.holdings[userID] {
		for _, h := range assets {
			out[a] = append(out[a], *h)
		}
	}
	return out, nil
}

func (l *ledger) Debts(context.Context) ([]ports.Debt, error) { return nil, nil }

type prices struct{}

func (prices) Prices() domain.Prices {
	return domain.Prices{"BTC": {Value: d("30000"), Fresh: true}, "ETH": {Value: d("2000"), Fresh: true}}
}

// instruments knows every asset (8 decimals, USDT 6) and every pair.
type instruments struct{}

func (instruments) Decimals(_ context.Context, asset string) (int32, error) {
	if asset == "USDT" {
		return 6, nil
	}
	return 8, nil
}

func (instruments) Pair(_ context.Context, symbol string) (ports.PairInfo, error) {
	base, quote, ok := strings.Cut(symbol, "-")
	if !ok {
		return ports.PairInfo{}, apperr.NotFound("no such pair")
	}
	return ports.PairInfo{Symbol: symbol, Base: base, Quote: quote, Status: "TRADING", TickDecimals: 2, Lot: d("0.0001")}, nil
}

type eligible struct{}

func (eligible) Check(context.Context, string, string, string) (bool, string, error) {
	return true, "", nil
}

// switches has every flag on but those set off.
type switches struct {
	mu  sync.Mutex
	off map[string]bool
}

func (f *switches) Enabled(key string, _ flags.Subject) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.off[key]
}

func (f *switches) Closed(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.off[key]
}

func (f *switches) set(key string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.off[key] = !on
}

type trading struct{}

func (trading) CancelAccount(context.Context, string, domain.Account) error { return nil }

func (trading) PlaceLiquidation(context.Context, string, domain.Account, ports.LiquidationOrder) (ports.OrderState, error) {
	return ports.OrderState{}, apperr.Unavailable(fmt.Errorf("no trading service here"))
}

// seed is the tests' own terms (not the repository's, which operators
// change): the cross account at 3x, a 2% fee, USDT at 0.001% an hour,
// BTC and ETH, BTC-USDT isolated at 10x.
const seed = `{
  "cross": {"leverage": 3, "warn_level": "1.30", "liquidation_level": "1.10"},
  "liquidation_fee_rate": "0.02",
  "floating": {"base_rate": "0.000005", "kink": "0.8", "kink_rate": "0.00003", "max_rate": "0.0001"},
  "assets": [
    {"asset": "USDT", "haircut": "1", "pool_cap": "2000000", "user_cap": "200000", "interest_model": "FIXED", "hourly_rate": "0.00001"},
    {"asset": "BTC", "haircut": "0.95", "pool_cap": "20", "user_cap": "2", "interest_model": "FIXED", "hourly_rate": "0.000005"},
    {"asset": "ETH", "haircut": "0.95", "pool_cap": "400", "user_cap": "40", "interest_model": "FLOATING", "hourly_rate": "0"}
  ],
  "pairs": [{"symbol": "BTC-USDT", "isolated_leverage": 10}]
}`

// api is margin-service's HTTP endpoints over the margin schema on the
// test database, its terms the tests' seed.
type api struct {
	t        *testing.T
	srv      *httptest.Server
	store    *postgres.Store
	ledger   *ledger
	features *switches
}

func newAPI(t *testing.T) *api {
	t.Helper()
	ctx := context.Background()
	db := testenv.Postgres(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, os.DirFS("../../../../migrations/margin"), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("margin-service", "test"))
	file, err := application.ReadTermsFile(strings.NewReader(seed))
	if err != nil {
		t.Fatal(err)
	}
	terms, err := file.Terms()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.ApplyTerms(ctx, store, terms, application.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	l := &ledger{
		spot: map[string]map[string]decimal.Decimal{}, holdings: map[string]map[domain.Account]map[string]*domain.Holding{},
		keys: map[string][]string{},
	}
	features := &switches{off: map[string]bool{}}
	svc := &application.Service{
		Store: store, Ledger: l, Prices: prices{}, Instruments: instruments{}, Eligibility: eligible{}, Features: features,
		Trading: trading{}, Log: log, Now: time.Now,
	}
	r := chi.NewRouter()
	h := &httpapi.Handler{Svc: svc}
	h.Routes(r)
	h.InternalRoutes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &api{t: t, srv: srv, store: store, ledger: l, features: features}
}

// do sends a request and returns the status and the decoded body; headers
// go in pairs.
func (a *api) do(method, path, body string, headers ...string) (int, map[string]any) {
	a.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, a.srv.URL+path, strings.NewReader(body))
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatal(err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			a.t.Fatalf("%s %s: %s", method, path, raw)
		}
	}
	return resp.StatusCode, out
}

// expect checks an answer's status and, for an error, its code.
func expect(t *testing.T, what string, status int, body map[string]any, want int, code string) {
	t.Helper()
	if status != want || (code != "" && body["code"] != code) {
		t.Fatalf("%s: %d %v, want %d %s", what, status, body, want, code)
	}
}

// versionOf reads an answer's version, failing the test without one.
func versionOf(t *testing.T, m map[string]any) int64 {
	t.Helper()
	v, ok := m["version"].(float64)
	if !ok {
		t.Fatalf("no version in %v", m)
	}
	return int64(v)
}

func item(body map[string]any, key, value string) map[string]any {
	items, _ := body["items"].([]any)
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m[key] == value {
			return m
		}
	}
	return nil
}

// TestUserEndpoints checks the user endpoints (api/openapi/margin.yaml):
// the terms public, the rest only with the caller's identity; requests
// read strictly; a transfer, a borrow and a repayment of ALL answered
// with the contract's fields, a write repeated with its key answered as
// the first time; the interest charged; with margin.enabled off no
// transfer in or borrow (MARGIN_DISABLED) but repaying and moving out; a
// liquidation waiting for the insurance fund shown to its user as under
// way.
func TestUserEndpoints(t *testing.T) {
	a := newAPI(t)
	user := uuid.Must(uuid.NewV7()).String()
	a.ledger.fund(user, "USDT", d("1000"))
	as := []string{"X-User-Id", user}
	with := func(key string) []string { return append(append([]string{}, as...), "Idempotency-Key", key) }

	st, body := a.do("GET", "/v1/margin/assets", "")
	expect(t, "the assets", st, body, 200, "")
	usdt := item(body, "asset", "USDT")
	if usdt == nil || usdt["haircut"] != "1" || usdt["interest_model"] != "FIXED" || usdt["borrowable"] != true {
		t.Fatalf("USDT %v", usdt)
	}
	if f, ok := usdt["floating"].(map[string]any); !ok || f["kink"] != "0.8" {
		t.Fatalf("USDT's floating curve %v", usdt["floating"])
	}
	st, body = a.do("GET", "/v1/margin/pairs", "")
	expect(t, "the pairs", st, body, 200, "")
	if p := item(body, "symbol", "BTC-USDT"); p == nil || p["isolated"] != true {
		t.Fatalf("BTC-USDT %v", p)
	}

	st, body = a.do("GET", "/v1/margin/accounts", "")
	expect(t, "without the caller", st, body, 401, apperr.CodeUnauthorized)
	for _, c := range []struct{ what, body string }{
		{"a direction", `{"direction":"SIDEWAYS","account":"MARGIN_CROSS","asset":"USDT","amount":"10"}`},
		{"an isolated account without its pair", `{"direction":"IN","account":"MARGIN_ISOLATED","asset":"USDT","amount":"10"}`},
		{"an amount", `{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"1x"}`},
		{"an unknown field", `{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"10","memo":"x"}`},
	} {
		st, body = a.do("POST", "/v1/margin/transfer", c.body, with("bad")...)
		expect(t, "a transfer with a bad "+c.what, st, body, 400, apperr.CodeInvalidArgument)
	}
	in := `{"direction":"IN","account":"MARGIN_CROSS","asset":"usdt","amount":"500"}`
	st, body = a.do("POST", "/v1/margin/transfer", in, as...)
	expect(t, "a transfer without a key", st, body, 400, "")
	st, body = a.do("POST", "/v1/margin/transfer", in, with("in-1")...)
	expect(t, "500 USDT in", st, body, 200, "")
	if body["direction"] != "IN" || body["account"] != "MARGIN_CROSS" || body["symbol"] != nil || body["asset"] != "USDT" ||
		body["amount"] != "500" || body["transfer_id"] == "" {
		t.Fatalf("the transfer %v", body)
	}
	first := body["transfer_id"]
	st, body = a.do("POST", "/v1/margin/transfer", in, with("in-1")...)
	expect(t, "the same transfer again", st, body, 200, "")
	if body["transfer_id"] != first {
		t.Fatalf("a repeat answered %v, first %v", body["transfer_id"], first)
	}

	st, body = a.do("POST", "/v1/margin/borrow", `{"account":"MARGIN_CROSS","asset":"USDT","amount":"100"}`, with("b-1")...)
	expect(t, "a borrow", st, body, 200, "")
	// The first hour's interest at 0.001% an hour is charged as it is lent.
	if body["principal"] != "100" || body["interest"] != "0.001" || body["interest_model"] != "FIXED" || body["symbol"] != nil {
		t.Fatalf("the loan %v", body)
	}
	st, body = a.do("GET", "/v1/margin/max-borrowable?account=MARGIN_CROSS&asset=USDT", "", as...)
	expect(t, "what may still be borrowed", st, body, 200, "")
	if body["asset"] != "USDT" || body["amount"] == "" || body["limited_by"] == "" {
		t.Fatalf("max-borrowable %v", body)
	}
	st, body = a.do("GET", "/v1/margin/interest?limit=0", "", as...)
	expect(t, "interest with a bad limit", st, body, 400, apperr.CodeInvalidArgument)
	st, body = a.do("GET", "/v1/margin/interest", "", as...)
	expect(t, "the interest", st, body, 200, "")
	if c := item(body, "asset", "USDT"); c == nil || c["interest"] != "0.001" || c["principal"] != "100" || c["interest_model"] != "FIXED" ||
		c["account"] != "MARGIN_CROSS" || c["symbol"] != nil || c["hour"] == "" || body["next_cursor"] != nil {
		t.Fatalf("the interest %v", body)
	}
	st, body = a.do("GET", "/v1/margin/loans?symbol=BTC-USDT", "", as...)
	expect(t, "loans of a pair without the account", st, body, 400, apperr.CodeInvalidArgument)
	st, body = a.do("GET", "/v1/margin/loans", "", as...)
	expect(t, "the loans", st, body, 200, "")
	if l := item(body, "asset", "USDT"); l == nil || l["principal"] != "100" {
		t.Fatalf("the loans %v", body)
	}
	st, body = a.do("POST", "/v1/margin/repay", `{"account":"MARGIN_CROSS","asset":"USDT","amount":"ALL"}`, with("r-1")...)
	expect(t, "repaying ALL", st, body, 200, "")
	loan, _ := body["loan"].(map[string]any)
	if body["interest_repaid"] != "0.001" || body["principal_repaid"] != "100" || loan["principal"] != "0" || loan["interest"] != "0" {
		t.Fatalf("the repayment %v", body)
	}
	// margin.enabled off (design decision of 02:02): no new money in, no
	// borrowing; repaying and moving out as before.
	st, body = a.do("POST", "/v1/margin/borrow", `{"account":"MARGIN_CROSS","asset":"USDT","amount":"50"}`, with("b-2")...)
	expect(t, "50 USDT borrowed again", st, body, 200, "")
	a.features.set(flags.KeyMarginEnabled, false)
	st, body = a.do("POST", "/v1/margin/transfer", `{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"10"}`, with("in-off")...)
	expect(t, "a transfer in with margin.enabled off", st, body, 403, "MARGIN_DISABLED")
	st, body = a.do("POST", "/v1/margin/borrow", `{"account":"MARGIN_CROSS","asset":"USDT","amount":"10"}`, with("b-off")...)
	expect(t, "a borrow with margin.enabled off", st, body, 403, "MARGIN_DISABLED")
	st, body = a.do("POST", "/v1/margin/repay", `{"account":"MARGIN_CROSS","asset":"USDT","amount":"ALL"}`, with("r-off")...)
	expect(t, "repaying with margin.enabled off", st, body, 200, "")
	st, body = a.do("POST", "/v1/margin/transfer", `{"direction":"OUT","account":"MARGIN_CROSS","asset":"USDT","amount":"10"}`, with("out-off")...)
	expect(t, "a transfer out with margin.enabled off", st, body, 200, "")
	a.features.set(flags.KeyMarginEnabled, true)

	st, body = a.do("GET", "/v1/margin/accounts", "", as...)
	expect(t, "the accounts", st, body, 200, "")
	cross, _ := body["cross"].(map[string]any)
	if cross["status"] != "NORMAL" || cross["total_liability"] != "0" || cross["margin_level"] != nil {
		t.Fatalf("the cross account %v", cross)
	}

	// A liquidation waiting for the insurance fund is under way for its
	// user; the console sees SHORTFALL.
	now := time.Now()
	l := ports.Liquidation{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Account: domain.Cross(), Trigger: ports.TriggerAuto,
		Status: ports.LiquidationShortfall, Step: application.StepCover, PriorStatus: domain.StatusNormal, TotalAsset: d("90"),
		TotalLiability: d("100"), FeeRate: d("0.02"), QuoteAsset: "USDT", Traded: decimal.Zero, Fee: decimal.Zero,
		FeeUSDT: decimal.Zero, InsuranceCovered: decimal.Zero, StartedAt: now, StepAt: now,
	}
	if err := a.store.Tx(context.Background(), func(r ports.Repos) error { return r.Liquidations().Insert(context.Background(), l) }); err != nil {
		t.Fatal(err)
	}
	st, body = a.do("GET", "/v1/margin/liquidations", "", as...)
	expect(t, "the liquidations", st, body, 200, "")
	if got := item(body, "liquidation_id", l.ID); got == nil || got["status"] != "STARTED" || got["completed_at"] != nil {
		t.Fatalf("the liquidation for its user %v", body)
	}
	st, body = a.do("GET", "/internal/margin/accounts/"+user+"/MARGIN_CROSS", "")
	expect(t, "the account in the console", st, body, 200, "")
	liqs, _ := body["liquidations"].([]any)
	var shortfall map[string]any
	if len(liqs) == 1 {
		shortfall, _ = liqs[0].(map[string]any)
	}
	if shortfall["status"] != "SHORTFALL" {
		t.Fatalf("the liquidation in the console %v", body["liquidations"])
	}
}

// TestInternalEndpoints checks the console's endpoints (admin-service on
// the compose network): not served to a request that came through the
// gateway; writes name their administrator; a change over a version that
// moved refused; an asset's, the cross account's and a pair's terms
// changed; the accounts listed and filtered; a freeze with its reason and
// back.
func TestInternalEndpoints(t *testing.T) {
	a := newAPI(t)
	admin := []string{"X-Admin-Id", "ops@example.com"}

	st, body := a.do("GET", "/internal/margin/settings", "", "X-User-Id", uuid.Must(uuid.NewV7()).String())
	expect(t, "through the gateway", st, body, 404, apperr.CodeNotFound)
	st, body = a.do("GET", "/internal/margin/settings", "")
	expect(t, "the settings", st, body, 200, "")
	cross, _ := body["cross"].(map[string]any)
	if body["updated_by"] != "" || cross["leverage"] != float64(3) || cross["liquidation_fee"] != "0.02" {
		t.Fatalf("the settings %v", body)
	}

	st, body = a.do("GET", "/internal/margin/assets", "")
	expect(t, "the assets", st, body, 200, "")
	btc := item(body, "asset", "BTC")
	if btc == nil || btc["updated_by"] != "" {
		t.Fatalf("BTC %v", btc)
	}
	version := versionOf(t, btc)
	put := func(v int64) string {
		return fmt.Sprintf(`{"borrowable":true,"collateral":true,"haircut":"0.9","pool_cap":"20","user_cap":"2","interest_model":"FIXED",
			"fixed_rate":"0.000005","float_base":"0.000005","float_kink":"0.8","float_kink_rate":"0.00003","float_max_rate":"0.0001",
			"expected_version":%d}`, v)
	}
	st, body = a.do("PUT", "/internal/margin/assets/BTC", put(version), "")
	expect(t, "a change without its administrator", st, body, 400, apperr.CodeInvalidArgument)
	st, body = a.do("PUT", "/internal/margin/assets/BTC", put(version+1), admin...)
	expect(t, "a change over a version that moved", st, body, 409, "MARGIN_PARAMS_CHANGED")
	st, body = a.do("PUT", "/internal/margin/assets/BTC", put(version), admin...)
	expect(t, "BTC's haircut to 0.9", st, body, 200, "")
	if body["haircut"] != "0.9" || body["updated_by"] != "ops@example.com" || versionOf(t, body) != version+1 {
		t.Fatalf("BTC after %v", body)
	}

	settings := func(leverage int, warn string, v int64) string {
		return fmt.Sprintf(`{"cross":{"leverage":%d,"warn_level":%q,"liquidation_level":"1.10","liquidation_fee":"0.02"},"expected_version":%d}`,
			leverage, warn, v)
	}
	st, body = a.do("GET", "/internal/margin/settings", "")
	expect(t, "the settings", st, body, 200, "")
	sv := versionOf(t, body)
	st, body = a.do("PUT", "/internal/margin/settings", settings(4, "1.20", sv), admin...)
	expect(t, "the cross account at 4x", st, body, 400, apperr.CodeInvalidArgument)
	st, body = a.do("PUT", "/internal/margin/settings", settings(5, "1.20", sv+1), admin...)
	expect(t, "settings over a version that moved", st, body, 409, "MARGIN_PARAMS_CHANGED")
	st, body = a.do("PUT", "/internal/margin/settings", settings(5, "1.20", sv), admin...)
	expect(t, "the cross account at 5x", st, body, 200, "")
	cross, _ = body["cross"].(map[string]any)
	if cross["leverage"] != float64(5) || cross["warn_level"] != "1.2" || body["updated_by"] != "ops@example.com" ||
		versionOf(t, body) != sv+1 {
		t.Fatalf("the settings after %v", body)
	}

	st, body = a.do("GET", "/internal/margin/pairs", "")
	expect(t, "the pairs", st, body, 200, "")
	pair := item(body, "symbol", "BTC-USDT")
	if pair == nil || pair["isolated"] != true || pair["leverage"] != float64(10) || pair["warn_level"] != "1.1" ||
		pair["liquidation_level"] != "1.05" || pair["liquidation_fee"] != "0.02" || pair["accounts"] != float64(0) {
		t.Fatalf("BTC-USDT %v", pair)
	}
	pv := versionOf(t, pair)
	st, body = a.do("PUT", "/internal/margin/pairs/BTC-USDT", fmt.Sprintf(
		`{"leverage":5,"warn_level":"1.20","liquidation_level":"1.10","liquidation_fee":"0.02","expected_version":%d}`, pv), admin...)
	expect(t, "a pair without isolated", st, body, 400, apperr.CodeInvalidArgument)
	st, body = a.do("PUT", "/internal/margin/pairs/btc-usdt", fmt.Sprintf(
		`{"leverage":5,"warn_level":"1.20","liquidation_level":"1.10","liquidation_fee":"0.02","isolated":true,"expected_version":%d}`, pv), admin...)
	expect(t, "BTC-USDT at 5x", st, body, 200, "")
	if body["symbol"] != "BTC-USDT" || body["leverage"] != float64(5) || body["updated_by"] != "ops@example.com" {
		t.Fatalf("BTC-USDT after %v", body)
	}

	user := uuid.Must(uuid.NewV7()).String()
	a.ledger.fund(user, "USDT", d("100"))
	st, body = a.do("POST", "/v1/margin/transfer", `{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"100"}`,
		"X-User-Id", user, "Idempotency-Key", "in")
	expect(t, "100 USDT in", st, body, 200, "")
	for _, q := range []string{
		"status=BAD", "user_id=someone", "account=SPOT", "limit=0", "user_ids=someone",
		"user_ids=" + user + "&exclude_user_ids=" + user,
	} {
		st, body = a.do("GET", "/internal/margin/accounts?"+q, "")
		expect(t, "accounts with "+q, st, body, 400, apperr.CodeInvalidArgument)
	}
	st, body = a.do("GET", "/internal/margin/accounts?user_id="+user, "")
	expect(t, "the user's accounts", st, body, 200, "")
	if acc := item(body, "user_id", user); acc == nil || acc["account"] != "MARGIN_CROSS" || acc["status"] != "NORMAL" ||
		acc["leverage"] != float64(5) || acc["frozen_by"] != nil || body["truncated"] != false {
		t.Fatalf("the user's accounts %v", body)
	}
	// Only some users' accounts, or all but some (review L3).
	st, body = a.do("GET", "/internal/margin/accounts?user_ids="+user+","+uuid.Must(uuid.NewV7()).String(), "")
	expect(t, "only the user's", st, body, 200, "")
	if item(body, "user_id", user) == nil {
		t.Fatalf("only the user's %v", body)
	}
	st, body = a.do("GET", "/internal/margin/accounts?exclude_user_ids="+user, "")
	expect(t, "all but the user's", st, body, 200, "")
	if item(body, "user_id", user) != nil {
		t.Fatalf("all but the user's %v", body)
	}
	path := "/internal/margin/accounts/" + user + "/MARGIN_CROSS"
	st, body = a.do("POST", path+"/freeze", `{"reason":" "}`, admin...)
	expect(t, "a freeze without a reason", st, body, 400, apperr.CodeInvalidArgument)
	st, body = a.do("POST", "/internal/margin/accounts/not-a-user/MARGIN_CROSS/freeze", `{"reason":"checks"}`, admin...)
	expect(t, "a freeze of no user", st, body, 400, apperr.CodeInvalidArgument)
	st, body = a.do("POST", path+"/freeze", `{"reason":"checks"}`, admin...)
	expect(t, "a freeze", st, body, 200, "")
	if body["status"] != "FROZEN" || body["frozen_by"] != "ops@example.com" || body["frozen_reason"] != "checks" {
		t.Fatalf("frozen %v", body)
	}
	st, body = a.do("POST", "/v1/margin/borrow", `{"account":"MARGIN_CROSS","asset":"USDT","amount":"10"}`,
		"X-User-Id", user, "Idempotency-Key", "b")
	expect(t, "a borrow while frozen", st, body, 409, "MARGIN_FROZEN")
	st, body = a.do("POST", path+"/unfreeze", "", admin...)
	expect(t, "an unfreeze", st, body, 200, "")
	if body["status"] != "NORMAL" || body["frozen_by"] != nil {
		t.Fatalf("unfrozen %v", body)
	}
	st, body = a.do("POST", path+"/liquidate", `{"approval_id":"approved"}`, admin...)
	expect(t, "a liquidation without an approval's ID", st, body, 400, apperr.CodeInvalidArgument)
	st, body = a.do("POST", path+"/liquidate", fmt.Sprintf(`{"approval_id":%q}`, uuid.Must(uuid.NewV7()).String()), admin...)
	expect(t, "a liquidation of an account owing nothing", st, body, 409, "MARGIN_NOTHING_OWED")
}
