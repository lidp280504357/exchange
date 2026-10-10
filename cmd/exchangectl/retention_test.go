package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// The migrations of each schema a policy runs in.
var retentionSchemas = map[string]fs.FS{
	"ledger": migrations.Ledger(), "trading": migrations.Trading(), "auth": migrations.Auth(), "users": migrations.Users(), "notify": migrations.Notify(),
	"wallet": migrations.Wallet(), "admin": migrations.Admin(), "risk": migrations.Risk(), "instrument": migrations.Instrument(),
	"config": migrations.Config(), "marketsim": migrations.MarketSim(), "marketmaker": migrations.MarketMaker(),
	"signer": migrations.Signer(), "derivatives": migrations.Derivatives(), "margin": migrations.Margin(), "market": migrations.Market(),
}

// migrated opens a fresh schema with the platform tables and the
// migrations of the schema named.
func migrated(ctx context.Context, t *testing.T, schema string) *pg.DB {
	t.Helper()
	m, ok := retentionSchemas[schema]
	if !ok {
		t.Fatalf("no migrations for %s", schema)
	}
	db := testenv.Postgres(t)
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, m, quiet); err != nil {
		t.Fatal(err)
	}
	return db
}

// Every policy's statements run against its schema as migrated, in a dry
// run and for real (M1): a column or table renamed fails here.
func TestRetentionPoliciesRun(t *testing.T) {
	ctx := context.Background()
	for _, p := range retentionPolicies() {
		t.Run(p.Schema(), func(t *testing.T) {
			db := migrated(ctx, t, p.Schema())
			for _, dry := range []bool{true, false} {
				w := retention.Window{Now: time.Now(), Days: 15, KeyDays: 90, DryRun: dry, Batch: 100}
				results, err := p.Run(ctx, db, w)
				if err != nil {
					t.Fatalf("dry run %t: %v", dry, err)
				}
				if len(results) == 0 {
					t.Fatal("no rules")
				}
				for _, r := range results {
					if r.Rows != 0 || r.Table == "" || r.Rule == "" {
						t.Fatalf("an empty schema: %+v", r)
					}
				}
			}
		})
	}
	if got, ch, err := pickPolicies(retentionPolicies(), "trading, auth"); err != nil || len(got) != 2 || ch {
		t.Fatalf("two schemas: %d %t %v", len(got), ch, err)
	}
	if got, ch, err := pickPolicies(retentionPolicies(), "clickhouse"); err != nil || len(got) != 0 || !ch {
		t.Fatalf("the read models alone: %d %t %v", len(got), ch, err)
	}
	if _, _, err := pickPolicies(retentionPolicies(), "trading,nosuch"); err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("an unknown schema: %v", err)
	}
}

// retention run over seeded history: orders ended and released (or never
// frozen) go with their fills, an order kept keeps its old fill; revoked
// sessions go with their tokens, idle ones once their tokens are gone;
// each flag keeps its latest 50 changes; withdrawals stay the keys'
// window, callbacks that matched nothing too, applied ones go; each
// instrument key keeps its latest change. A dry run counts the same and
// deletes nothing.
func TestRetentionRun(t *testing.T) {
	ctx := context.Background()
	dbs := map[string]*pg.DB{}
	for _, schema := range []string{"trading", "auth", "config", "wallet", "instrument"} {
		dbs[schema] = migrated(ctx, t, schema)
	}
	old, recent := time.Now().AddDate(0, 0, -20), time.Now().AddDate(0, 0, -1)

	trading := dbs["trading"]
	order := func(status string, released bool, at time.Time) string {
		id := uuid.NewString()
		if _, err := trading.Exec(ctx, `INSERT INTO orders (id, user_id, client_order_id, symbol, side, type, time_in_force, stp,
			price, quantity, status, frozen_asset, frozen_amount, freeze_state, maker_fee_rate, taker_fee_rate, base_decimals,
			quote_decimals, created_at, updated_at, released)
			VALUES ($1, $2, 'c1', 'BTC-USDT', 'BUY', 'LIMIT', 'GTC', 'CANCEL_NEWEST', 100, 1, $3, 'USDT', 100, 'FROZEN', 0.001,
				0.001, 8, 2, $4, $4, $5)`, id, uuid.NewString(), status, at, released); err != nil {
			t.Fatal(err)
		}
		if _, err := trading.Exec(ctx, `INSERT INTO fills (trade_id, order_id, user_id, symbol, side, maker, price, quantity,
			quote_quantity, fee_asset, fee, sequence, executed_at)
			VALUES ($1, $2, $3, 'BTC-USDT', 'BUY', true, 100, 1, 100, 'BTC', 0, 1, $4)`, uuid.NewString(), id, uuid.NewString(), old); err != nil {
			t.Fatal(err)
		}
		return id
	}
	gone := order("FILLED", true, old)
	unreleased := order("CANCELED", false, old)
	open := order("OPEN", false, old)
	fresh := order("CANCELED", true, recent)
	// Refused by the ledger: never frozen, released by nothing (B200).
	refused := order("REJECTED", false, old)
	if _, err := trading.Exec(ctx, `UPDATE orders SET freeze_state = 'NONE' WHERE id = $1`, refused); err != nil {
		t.Fatal(err)
	}

	auth := dbs["auth"]
	session := func(revoked *time.Time) string {
		id := uuid.NewString()
		if _, err := auth.Exec(ctx, `INSERT INTO sessions (id, user_id, device_id, client_type, revoked_at) VALUES ($1, $2, 'd', 'WEB', $3)`,
			id, uuid.NewString(), revoked); err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Exec(ctx, `INSERT INTO refresh_tokens (token_hash, session_id, generation, expires_at) VALUES ($1, $2, 1, $3)`,
			[]byte(id), id, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	revoked, live := session(&old), session(nil)
	// Idle since before the window: its tokens gone, it ends (B200); with
	// one still valid, it stays.
	idle, idleLive := uuid.NewString(), session(nil)
	if _, err := auth.Exec(ctx, `INSERT INTO sessions (id, user_id, device_id, client_type, last_seen_at) VALUES ($1, $2, 'd', 'WEB', $3)`,
		idle, uuid.NewString(), old); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id = $1`, idleLive, old); err != nil {
		t.Fatal(err)
	}

	wallet := dbs["wallet"]
	withdrawal := func(at time.Time) string {
		id := uuid.NewString()
		if _, err := wallet.Exec(ctx, `INSERT INTO withdrawals (id, user_id, asset, network, address, amount, fee, status,
			required_confirmations, created_at, updated_at) VALUES ($1, $2, 'USDT', 'TRON', 'T1', 10, 1, 'REJECTED', 1, $3, $3)`,
			id, uuid.NewString(), at); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Within the keys' window a user's monthly limit still counts it (B200).
	monthOld, ancient := withdrawal(old), withdrawal(time.Now().AddDate(0, 0, -100))
	callback := func(result string) {
		if _, err := wallet.Exec(ctx, `INSERT INTO custody_callbacks (id, provider, raw, signature_ok, result, received_at, processed_at)
			VALUES ($1, 'UDUN', '{}', true, $2, $3, $3)`, uuid.NewString(), result, old); err != nil {
			t.Fatal(err)
		}
	}
	callback("APPLIED")
	callback("UNMATCHED")

	instrument := dbs["instrument"]
	for i, k := range []string{"BTC-USDT", "BTC-USDT", "BTC-USDT", "ETH-USDT"} {
		if _, err := instrument.Exec(ctx, `INSERT INTO config_history (entity, key, version, value, actor, reason, created_at, source)
			VALUES ('TRADING_PAIR', $1, $2, '{}', 'test', 'test', $3, 'CONSOLE')`, k, i+1, old); err != nil {
			t.Fatal(err)
		}
	}

	config := dbs["config"]
	for i := range 60 {
		if _, err := config.Exec(ctx, `INSERT INTO flag_changes (key, new_value, changed_by, reason, changed_at)
			VALUES ('spot.trading', 'true', 'test', $1, $2)`, strconv.Itoa(i), old.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	own := func(schema string) (*pg.DB, func(), error) { return dbs[schema], func() {}, nil }
	policies, _, err := pickPolicies(retentionPolicies(), "trading,auth,config,wallet,instrument")
	if err != nil {
		t.Fatal(err)
	}
	w := retention.Window{Now: time.Now(), Days: 15, KeyDays: 90, DryRun: true, Batch: 2}
	var out bytes.Buffer
	if err := runRetention(ctx, policies, own, nil, w, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"dry run, nothing deleted", "would delete 21 rows"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry run: no %q in\n%s", want, out.String())
		}
	}
	count := func(db *pg.DB, q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(trading, `SELECT count(*) FROM orders`); n != 5 {
		t.Fatalf("the dry run deleted orders: %d left", n)
	}

	w.DryRun = false
	out.Reset()
	if err := runRetention(ctx, policies, own, nil, w, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "deleted 21 rows") {
		t.Fatalf("run:\n%s", out.String())
	}
	// 2 orders and their fills; a revoked session and its token, an idle
	// one; 10 flag changes; a withdrawal past the keys' window, an applied
	// callback; 2 of a pair's 3 changes.
	for id, want := range map[string]int{gone: 0, refused: 0, unreleased: 1, open: 1, fresh: 1} {
		if n := count(trading, `SELECT count(*) FROM orders WHERE id = $1`, id); n != want {
			t.Fatalf("order %s: %d, want %d", id, n, want)
		}
	}
	if n := count(trading, `SELECT count(*) FROM fills WHERE order_id = ANY($1)`, []string{unreleased, open}); n != 2 {
		t.Fatalf("the kept orders' old fills: %d, want 2", n)
	}
	if n := count(trading, `SELECT count(*) FROM fills`); n != 3 {
		t.Fatalf("fills left: %d, want 3", n)
	}
	if count(auth, `SELECT count(*) FROM sessions WHERE id = ANY($1)`, []string{revoked, idle}) != 0 ||
		count(auth, `SELECT count(*) FROM sessions WHERE id = ANY($1)`, []string{live, idleLive}) != 2 ||
		count(auth, `SELECT count(*) FROM refresh_tokens`) != 2 {
		t.Fatal("the revoked and the idle session should be gone, the live ones kept with their tokens")
	}
	if count(wallet, `SELECT count(*) FROM withdrawals WHERE id = $1`, monthOld) != 1 || count(wallet, `SELECT count(*) FROM withdrawals WHERE id = $1`, ancient) != 0 {
		t.Fatal("a withdrawal within the keys' window should stay, one past it go")
	}
	if n := count(wallet, `SELECT count(*) FROM custody_callbacks WHERE result = 'UNMATCHED'`); n != 1 || count(wallet, `SELECT count(*) FROM custody_callbacks`) != 1 {
		t.Fatal("the unmatched callback should stay, the applied one go")
	}
	if n := count(instrument, `SELECT count(*) FROM config_history WHERE key = 'BTC-USDT'`); n != 1 ||
		count(instrument, `SELECT version FROM config_history WHERE key = 'BTC-USDT'`) != 3 ||
		count(instrument, `SELECT count(*) FROM config_history WHERE key = 'ETH-USDT'`) != 1 {
		t.Fatal("each pair's latest change should stay")
	}
	// The migrations' own changes of other flags are recent and stay.
	if n := count(config, `SELECT count(*) FROM flag_changes WHERE key = 'spot.trading'`); n != 50 {
		t.Fatalf("flag changes left: %d, want the latest 50", n)
	}
	if n := count(config, `SELECT min(reason::int) FROM flag_changes WHERE key = 'spot.trading'`); n != 10 {
		t.Fatalf("the oldest change kept is #%d, want #10", n)
	}
}

// fakePolicy reports a row per run, or fails.
type fakePolicy struct {
	schema string
	rows   int64
	err    error
}

func (f fakePolicy) Schema() string { return f.schema }

func (f fakePolicy) Run(context.Context, *pg.DB, retention.Window) ([]retention.Result, error) {
	return []retention.Result{{Table: "t", Rule: "r", Rows: f.rows}}, f.err
}

// A schema that fails does not stop the next; the run fails naming it
// (B199). Fewer than 7 days need --force.
func TestRetentionGoesOn(t *testing.T) {
	ctx := context.Background()
	w := retention.Window{Now: time.Now(), Days: 15, KeyDays: 90, Batch: 10, DryRun: true}
	none := func(string) (*pg.DB, func(), error) { return nil, func() {}, nil }
	var out bytes.Buffer
	err := runRetention(ctx, []retention.Policy{fakePolicy{"first", 1, errors.New("boom")}, fakePolicy{"second", 2, nil}}, none, nil, w, &out)
	if err == nil || !strings.Contains(err.Error(), "first: boom") || !strings.Contains(out.String(), "second.t") ||
		!strings.Contains(out.String(), "FAILED") || !strings.Contains(out.String(), "would delete 3 rows") {
		t.Fatalf("%v\n%s", err, out.String())
	}
	out.Reset()
	if err := retentionCmd(ctx, settings{}, []string{"run", "--days", "3"}, &out); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("3 days without --force: %v", err)
	}
}
