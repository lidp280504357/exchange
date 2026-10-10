package main

import (
	"bytes"
	"context"
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
	if _, err := pickPolicies(retentionPolicies(), "trading, auth"); err != nil {
		t.Fatal(err)
	}
	if _, err := pickPolicies(retentionPolicies(), "trading,nosuch"); err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("an unknown schema: %v", err)
	}
}

// retention run over seeded history: orders ended and released go with
// their fills, an order kept keeps its old fill; revoked sessions go with
// their tokens; each flag keeps its latest 50 changes. A dry run counts the
// same and deletes nothing.
func TestRetentionRun(t *testing.T) {
	ctx := context.Background()
	dbs := map[string]*pg.DB{"trading": migrated(ctx, t, "trading"), "auth": migrated(ctx, t, "auth"), "config": migrated(ctx, t, "config")}
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

	config := dbs["config"]
	for i := range 60 {
		if _, err := config.Exec(ctx, `INSERT INTO flag_changes (key, new_value, changed_by, reason, changed_at)
			VALUES ('spot.trading', 'true', 'test', $1, $2)`, strconv.Itoa(i), old.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	own := func(schema string) (*pg.DB, func(), error) { return dbs[schema], func() {}, nil }
	policies, err := pickPolicies(retentionPolicies(), "trading,auth,config")
	if err != nil {
		t.Fatal(err)
	}
	w := retention.Window{Now: time.Now(), Days: 15, KeyDays: 90, DryRun: true, Batch: 2}
	var out bytes.Buffer
	if err := runRetention(ctx, policies, own, w, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"dry run, nothing deleted", "would delete 14 rows"} {
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
	if n := count(trading, `SELECT count(*) FROM orders`); n != 4 {
		t.Fatalf("the dry run deleted orders: %d left", n)
	}

	w.DryRun = false
	out.Reset()
	if err := runRetention(ctx, policies, own, w, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "deleted 14 rows") {
		t.Fatalf("run:\n%s", out.String())
	}
	// 1 order and its fill; 1 session and its token; 10 flag changes.
	for id, want := range map[string]int{gone: 0, unreleased: 1, open: 1, fresh: 1} {
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
	if count(auth, `SELECT count(*) FROM sessions WHERE id = $1`, revoked) != 0 || count(auth, `SELECT count(*) FROM sessions WHERE id = $1`, live) != 1 ||
		count(auth, `SELECT count(*) FROM refresh_tokens`) != 1 {
		t.Fatal("the revoked session and its token should be gone, the live one kept")
	}
	// The migrations' own changes of other flags are recent and stay.
	if n := count(config, `SELECT count(*) FROM flag_changes WHERE key = 'spot.trading'`); n != 50 {
		t.Fatalf("flag changes left: %d, want the latest 50", n)
	}
	if n := count(config, `SELECT min(reason::int) FROM flag_changes WHERE key = 'spot.trading'`); n != 10 {
		t.Fatalf("the oldest change kept is #%d, want #10", n)
	}
}
