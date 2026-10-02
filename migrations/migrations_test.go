package migrations_test

import (
	"context"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func apply(t *testing.T, fsys fs.FS) *pg.DB {
	t.Helper()
	db := testenv.Postgres(t)
	if err := migrate.Up(context.Background(), db, fsys, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("up: %v", err)
	}
	return db
}

// rejects asserts that a statement violates a constraint.
func rejects(t *testing.T, db *pg.DB, why, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err == nil {
		t.Errorf("%s: statement was accepted: %s", why, sql)
	}
}

func accepts(t *testing.T, db *pg.DB, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func TestAuthSchema(t *testing.T) {
	db := apply(t, migrations.Auth())
	user, other := uuid.New(), uuid.New()
	ins := `INSERT INTO identities (id, user_id, kind, value, verified_at) VALUES ($1, $2, $3, $4, now())`
	accepts(t, db, ins, uuid.New(), user, "EMAIL", "a@example.com")
	accepts(t, db, ins, uuid.New(), user, "PHONE", "+8613812341234")
	rejects(t, db, "an identity belongs to one user", ins, uuid.New(), other, "EMAIL", "a@example.com")
	rejects(t, db, "one identity per kind and user", ins, uuid.New(), user, "EMAIL", "b@example.com")
	rejects(t, db, "unknown identity kind", ins, uuid.New(), other, "WECHAT", "x")

	rejects(t, db, "unknown OTP scene", `INSERT INTO otp_challenges (id, scene, channel, target, device_id, code_hash, expires_at)
		VALUES ($1, 'NOPE', 'EMAIL', 'a@example.com', 'd', '\x00', now())`, uuid.New())
	rejects(t, db, "refresh tokens need a session", `INSERT INTO refresh_tokens (token_hash, session_id, generation, expires_at)
		VALUES ('\x01', $1, 1, now())`, uuid.New())
}

func TestUsersSchema(t *testing.T) {
	db := apply(t, migrations.Users())
	id := uuid.New()
	accepts(t, db, `INSERT INTO users (id, region) VALUES ($1, 'CN')`, id)
	rejects(t, db, "unknown status", `INSERT INTO users (id, region, status) VALUES ($1, 'CN', 'BANNED')`, uuid.New())
	accepts(t, db, `INSERT INTO consents (user_id, document, version) VALUES ($1, 'TERMS', '2026-09-28')`, id)
	rejects(t, db, "consents need a user", `INSERT INTO consents (user_id, document, version) VALUES ($1, 'TERMS', 'v1')`, uuid.New())
	var status string
	if err := db.QueryRow(context.Background(), `SELECT status FROM users WHERE id = $1`, id).Scan(&status); err != nil || status != "ACTIVE" {
		t.Fatalf("new users start ACTIVE: %q %v", status, err)
	}
}

func TestNotifySchema(t *testing.T) {
	db := apply(t, migrations.Notify())
	accepts(t, db, `INSERT INTO deliveries (id, kind, channel, template, target_mask) VALUES ($1, 'OTP', 'EMAIL', 'otp', 'a***@x.com')`, uuid.New())
	rejects(t, db, "unknown delivery status", `INSERT INTO deliveries (id, kind, channel, template, target_mask, status)
		VALUES ($1, 'OTP', 'EMAIL', 'otp', 'a***@x.com', 'LOST')`, uuid.New())
}

// TestDownMigrations checks that every schema can be rolled back to empty.
func TestDownMigrations(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"auth": migrations.Auth(), "users": migrations.Users(), "notify": migrations.Notify(), "config": migrations.Config(),
		"instrument": migrations.Instrument(), "ledger": migrations.Ledger(), "risk": migrations.Risk(), "trading": migrations.Trading(), "matching": migrations.Matching(), "market": migrations.Market(),
		"wallet": migrations.Wallet(), "signer": migrations.Signer(), "admin": migrations.Admin(),
		"derivatives": migrations.Derivatives(), "marketsim": migrations.MarketSim(),
	} {
		t.Run(name, func(t *testing.T) {
			db := apply(t, fsys)
			sqlDB := stdlib.OpenDBFromPool(db.Pool)
			defer sqlDB.Close()
			p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys, goose.WithDisableGlobalRegistry(true))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.DownTo(context.Background(), 0); err != nil {
				t.Fatalf("down: %v", err)
			}
			var tables int
			err = db.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables
				WHERE table_schema = $1 AND table_name <> 'goose_db_version'`, db.Schema()).Scan(&tables)
			if err != nil || tables != 0 {
				t.Fatalf("%d tables left after down: %v", tables, err)
			}
		})
	}
}

func TestInstrumentSchema(t *testing.T) {
	db := apply(t, migrations.Instrument())
	accepts(t, db, `INSERT INTO fee_schedules (tier, maker_fee_rate, taker_fee_rate) VALUES ('default', 0.001, 0.001)`)
	rejects(t, db, "fee rates stay below 10%", `INSERT INTO fee_schedules (tier, maker_fee_rate, taker_fee_rate) VALUES ('greedy', 0.5, 0.001)`)
	asset := `INSERT INTO assets (asset_code, name, decimals) VALUES ($1, $1, $2)`
	accepts(t, db, asset, "BTC", 8)
	accepts(t, db, asset, "USDT", 6)
	rejects(t, db, "lower-case codes", asset, "btc", 8)
	rejects(t, db, "at most 18 decimals", asset, "WEI", 19)
	pair := `INSERT INTO trading_pairs (symbol, base_asset, quote_asset, tick_size, lot_size, min_quantity, max_quantity,
		min_notional, price_band, fee_tier) VALUES ($1, $2, $3, 0.01, 0.00001, 0.00001, 100, 5, 0.1, 'default')`
	accepts(t, db, pair, "BTC-USDT", "BTC", "USDT")
	rejects(t, db, "symbol names its assets", pair, "XBT-USDT", "BTC", "USDT")
	rejects(t, db, "unknown asset", pair, "ETH-USDT", "ETH", "USDT")
	rejects(t, db, "unknown status", `UPDATE trading_pairs SET status = 'LIVE'`)
	rejects(t, db, "networks need an asset", `INSERT INTO networks (asset_code, network, chain, confirmations, min_deposit,
		min_withdraw, withdraw_fee) VALUES ('ETH', 'ETH-SEPOLIA', '11155111', 12, 0, 0, 0)`)
	// Asset profiles: a logo always has its type, and stays small.
	accepts(t, db, `UPDATE assets SET display_name = 'Bitcoin', logo = '\x89504e47'::bytea, logo_mime = 'image/png',
		description = '{"en": "x"}', profile_version = 1 WHERE asset_code = 'BTC'`)
	rejects(t, db, "a logo without its type", `UPDATE assets SET logo = '\x3c737667'::bytea WHERE asset_code = 'USDT'`)
	rejects(t, db, "a type without a logo", `UPDATE assets SET logo_mime = 'image/png' WHERE asset_code = 'USDT'`)
	rejects(t, db, "only PNG, SVG and WebP", `UPDATE assets SET logo = '\x47494638'::bytea, logo_mime = 'image/gif' WHERE asset_code = 'USDT'`)
	rejects(t, db, "at most 200 KB", `UPDATE assets SET logo = decode(repeat('00', 204801), 'hex'), logo_mime = 'image/png' WHERE asset_code = 'USDT'`)
	rejects(t, db, "a display name of at most 32 characters", `UPDATE assets SET display_name = repeat('x', 33) WHERE asset_code = 'USDT'`)
	rejects(t, db, "introductions by language", `UPDATE assets SET description = '["x"]' WHERE asset_code = 'USDT'`)
	// The history names where a change came from (C3: the console's edits survive deploys).
	history := `INSERT INTO config_history (entity, key, version, value, actor, reason, source) VALUES ('TRADING_PAIR', 'BTC-USDT', $1, '{}', 'x', 'y', $2)`
	accepts(t, db, history, 1, "FILE")
	accepts(t, db, history, 2, "CONSOLE")
	accepts(t, db, history, 3, "")
	rejects(t, db, "a known source", history, 4, "API")
}

func TestLedgerSchema(t *testing.T) {
	db := apply(t, migrations.Ledger())
	ctx := context.Background()
	user, adj, journal := uuid.New(), uuid.New(), uuid.New()
	accounts := `INSERT INTO accounts (id, owner_type, owner_id, account_type, asset, available) VALUES ($1, $2, $3, $4, 'USDT', $5)`
	accepts(t, db, accounts, user, "USER", uuid.NewString(), "SPOT", 0)
	accepts(t, db, accounts, adj, "SYSTEM", "SYSTEM", "ADJUSTMENT", -5)
	rejects(t, db, "user accounts never go negative", accounts, uuid.New(), "USER", uuid.NewString(), "SPOT", -1)
	rejects(t, db, "system accounts are not SPOT", accounts, uuid.New(), "SYSTEM", "SYSTEM", "SPOT", 0)
	rejects(t, db, "users own SPOT or FUTURES only", accounts, uuid.New(), "USER", uuid.NewString(), "FEE_REVENUE", 0)

	// A balanced journal commits; an unbalanced one fails at commit.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO journals (id, idem_key, request_hash, entry_type) VALUES ('` + journal.String() + `', 'k1', '\x00', 'MANUAL_ADJUSTMENT')`,
		`INSERT INTO journal_lines (journal_id, account_id, asset, amount, balance_kind, available_after, frozen_after, account_version)
			VALUES ('` + journal.String() + `', '` + user.String() + `', 'USDT', 5, 'AVAILABLE', 5, 0, 1)`,
		`INSERT INTO journal_lines (journal_id, account_id, asset, amount, balance_kind, available_after, frozen_after, account_version)
			VALUES ('` + journal.String() + `', '` + adj.String() + `', 'USDT', -5, 'AVAILABLE', -5, 0, 1)`,
	} {
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("balanced journal: %v", err)
	}
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	_, _ = tx.Exec(ctx, `INSERT INTO journals (id, idem_key, request_hash, entry_type) VALUES ($1, 'k2', '\x00', 'MANUAL_ADJUSTMENT')`, other)
	_, _ = tx.Exec(ctx, `INSERT INTO journal_lines (journal_id, account_id, asset, amount, balance_kind, available_after, frozen_after, account_version)
		VALUES ($1, $2, 'USDT', 5, 'AVAILABLE', 10, 0, 2)`, other, user)
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("an unbalanced journal must not commit")
	}

	rejects(t, db, "journals are append-only", `UPDATE journals SET memo = 'x'`)
	rejects(t, db, "lines are append-only", `DELETE FROM journal_lines`)
	rejects(t, db, "no truncation", `TRUNCATE journal_lines`)
	rejects(t, db, "idempotency keys are unique", `INSERT INTO journals (id, idem_key, request_hash, entry_type) VALUES ($1, 'k1', '\x00', 'TRADE_FEE')`, uuid.New())
}

func TestRiskSchema(t *testing.T) {
	db := apply(t, migrations.Risk())
	user, event := uuid.New(), uuid.New()
	accepts(t, db, `INSERT INTO user_devices (user_id, device_id, first_seen_at) VALUES ($1, 'd1', now())`, user)
	rejects(t, db, "a device has an ID", `INSERT INTO user_devices (user_id, device_id, first_seen_at) VALUES ($1, '', now())`, user)
	velocity := `INSERT INTO velocity_events (rule, key, event_id, at) VALUES ('r', 'd1', $1, now())`
	accepts(t, db, velocity, event)
	rejects(t, db, "an event counts once per rule and key", velocity, event)
	assessment := `INSERT INTO assessments (id, user_id, source_event_id, source_event_type, score, action, hits, enforced, created_at)
		VALUES ($1, $2, $3, 'auth.UserRegistered', $4, $5, '[]', false, now())`
	accepts(t, db, assessment, uuid.New(), user, event, 60, "REVIEW")
	rejects(t, db, "one assessment per source event", assessment, uuid.New(), user, event, 60, "REVIEW")
	rejects(t, db, "scores stay within 0..100", assessment, uuid.New(), user, uuid.New(), 101, "REVIEW")
	rejects(t, db, "known actions only", assessment, uuid.New(), user, uuid.New(), 10, "BAN")
}

func TestTradingSchema(t *testing.T) {
	db := apply(t, migrations.Trading())
	user := uuid.New()
	order := `INSERT INTO orders (id, user_id, client_order_id, symbol, side, type, time_in_force, stp, price, quantity, quote_amount,
		status, frozen_asset, frozen_amount, freeze_state, maker_fee_rate, taker_fee_rate, base_decimals, quote_decimals, created_at, updated_at)
		VALUES ($1, $2, $3, 'BTC-USDT', $4, $5, 'GTC', 'CANCEL_NEWEST', $6, $7, $8, 'NEW', 'USDT', 60, 'PENDING', 0.001, 0.001, 8, 6, now(), now())`
	accepts(t, db, order, uuid.New(), user, "c1", "BUY", "LIMIT", 60000, 0.001, nil)
	rejects(t, db, "client_order_id is unique per user", order, uuid.New(), user, "c1", "BUY", "LIMIT", 60000, 0.001, nil)
	accepts(t, db, order, uuid.New(), uuid.New(), "c1", "BUY", "LIMIT", 60000, 0.001, nil)
	accepts(t, db, order, uuid.New(), user, "c2", "BUY", "MARKET", nil, nil, 100)
	accepts(t, db, order, uuid.New(), user, "c3", "SELL", "MARKET", nil, 0.5, nil)
	rejects(t, db, "a limit order needs a price", order, uuid.New(), user, "c4", "BUY", "LIMIT", nil, 0.001, nil)
	rejects(t, db, "a market buy spends a quote amount", order, uuid.New(), user, "c5", "BUY", "MARKET", nil, 0.5, nil)
	rejects(t, db, "a market sell sells a quantity", order, uuid.New(), user, "c6", "SELL", "MARKET", nil, nil, 100)
	rejects(t, db, "client order IDs are short tokens", order, uuid.New(), user, "has space", "BUY", "LIMIT", 60000, 0.001, nil)
	rejects(t, db, "amounts are positive", order, uuid.New(), user, "c7", "BUY", "LIMIT", 60000, -1, nil)
	rejects(t, db, "known statuses only", `UPDATE orders SET status = 'LIVE'`)
}

func TestWalletSchema(t *testing.T) {
	db := apply(t, migrations.Wallet())
	user := uuid.New()
	addr := `INSERT INTO deposit_addresses (user_id, network, derivation_index, address, created_at) VALUES ($1, 'ETH-SEPOLIA', $2, $3, now())`
	accepts(t, db, addr, user, 0, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	rejects(t, db, "one address per user and network", addr, user, 1, "0x0000000000000000000000000000000000000001")
	rejects(t, db, "an index is used once", addr, uuid.New(), 0, "0x0000000000000000000000000000000000000002")
	rejects(t, db, "an address belongs to one user", addr, uuid.New(), 2, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed")

	// A custodian creates its addresses (ADR-0011): no derivation index, and
	// not every network is EVM.
	custodied := `INSERT INTO deposit_addresses (user_id, network, derivation_index, address, provider, created_at) VALUES ($1, $2, $3, $4, $5, now())`
	accepts(t, db, custodied, user, "BTC", nil, "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", "UDUN")
	accepts(t, db, custodied, user, "TRON", nil, "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8", "UDUN")
	rejects(t, db, "the platform's own addresses have an index", custodied, uuid.New(), "ETH-SEPOLIA", nil, "0x0000000000000000000000000000000000000003", "")
	rejects(t, db, "a custodian's addresses have none", custodied, uuid.New(), "BTC", 4, "bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh", "UDUN")

	dep := `INSERT INTO deposits (id, user_id, asset, network, address, tx_hash, log_index, block_number, block_hash, amount,
		raw_amount, required_confirmations, unclaimed, reason, status, journal_id, detected_at)
		VALUES ($1, $2, $3, 'ETH-SEPOLIA', '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed', $4, $5, 100, '0xb', 0.002, 2000000000000000,
		12, $6, $7, $8, $9, now())`
	tx := "0x" + strings.Repeat("ab", 32)
	accepts(t, db, dep, uuid.New(), user, "ETH", tx, -1, false, nil, "DETECTED", nil)
	rejects(t, db, "a transfer is one deposit", dep, uuid.New(), user, "ETH", tx, -1, false, nil, "DETECTED", nil)
	accepts(t, db, dep, uuid.New(), user, "ETH", tx, 3, false, nil, "DETECTED", nil)
	rejects(t, db, "lower-case transaction hashes", dep, uuid.New(), user, "ETH", strings.ToUpper(tx), 5, false, nil, "DETECTED", nil)
	rejects(t, db, "only unsupported tokens lack an asset", dep, uuid.New(), user, nil, tx, 6, false, nil, "DETECTED", nil)
	accepts(t, db, dep, uuid.New(), user, nil, tx, 7, false, "UNSUPPORTED_TOKEN", "REJECTED", nil)
	rejects(t, db, "unclaimed deposits say why", dep, uuid.New(), user, "ETH", tx, 8, true, nil, "CONFIRMED", nil)
	rejects(t, db, "credited deposits have a journal", dep, uuid.New(), user, "ETH", tx, 9, false, nil, "CREDITED", nil)
	rejects(t, db, "unclaimed deposits end REJECTED", dep, uuid.New(), user, "ETH", tx, 10, true, "BELOW_MINIMUM", "CREDITED", uuid.New())
	rejects(t, db, "known statuses only", dep, uuid.New(), user, "ETH", tx, 11, false, nil, "LOST", nil)

	// A custodian's deposit is its trade: one transaction may pay several
	// addresses, and its hash need not be EVM's.
	reported := `INSERT INTO deposits (id, user_id, asset, network, address, tx_hash, log_index, block_number, block_hash, amount,
		raw_amount, required_confirmations, provider_tx_id, status, detected_at)
		VALUES ($1, $2, 'BTC', 'BTC', 'bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq', $3, -1, 0, '', 0.01, 1000000, 0, $4, 'CONFIRMED', now())`
	btcTx := strings.Repeat("cd", 32)
	accepts(t, db, reported, uuid.New(), user, btcTx, "trade-1")
	accepts(t, db, reported, uuid.New(), user, btcTx, "trade-2")
	rejects(t, db, "a trade is one deposit", reported, uuid.New(), user, strings.Repeat("ef", 32), "trade-1")

	wd := `INSERT INTO withdrawals (id, user_id, asset, network, address, amount, fee, status, required_confirmations, provider,
		submitted_at, created_at, updated_at) VALUES ($1, $2, 'BTC', 'BTC', 'bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh', 0.01, 0.0001,
		$3, 0, $4, $5, now(), now())`
	accepts(t, db, wd, uuid.New(), user, "SUBMITTED", "UDUN", time.Now())
	rejects(t, db, "a submitted withdrawal names its custodian", wd, uuid.New(), user, "SUBMITTED", "", time.Now())
	rejects(t, db, "a submitted withdrawal says when", wd, uuid.New(), user, "SUBMITTED", "UDUN", nil)
	accepts(t, db, wd, uuid.New(), user, "CONFIRMED", "UDUN", time.Now())
	rejects(t, db, "the platform's own withdrawals confirm a transaction", wd, uuid.New(), user, "CONFIRMED", "", nil)
	withdrawTo := `INSERT INTO withdraw_addresses (id, user_id, network, address, created_at, usable_at) VALUES ($1, $2, 'BTC', $3, now(), now())`
	accepts(t, db, withdrawTo, uuid.New(), user, "bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh")

	callback := `INSERT INTO custody_callbacks (id, provider, trade_id, status, raw, signature_ok, result, received_at)
		VALUES ($1, 'UDUN', $2, $3, $4, $5, $6, now())`
	accepts(t, db, callback, uuid.New(), "trade-1", 3, "{}", true, "APPLIED")
	rejects(t, db, "a verified callback is kept once per trade and status", callback, uuid.New(), "trade-1", 3, "{}", true, "IGNORED")
	accepts(t, db, callback, uuid.New(), "trade-1", 3, "{}", false, "REJECTED")
	accepts(t, db, callback, uuid.New(), "trade-1", 3, "{}", false, "REJECTED")
	rejects(t, db, "a forged callback is rejected", callback, uuid.New(), "trade-1", 4, "{}", false, "APPLIED")
	rejects(t, db, "at most 16 KiB of a callback is kept", callback, uuid.New(), "trade-2", 3, strings.Repeat("x", 16385), true, "RECEIVED")
}

func TestSignerSchema(t *testing.T) {
	db := apply(t, migrations.Signer())
	ins := `INSERT INTO signatures (request_id, request_hash, purpose, reference, chain_id, from_address, to_address, value, nonce,
		gas_limit, max_fee, max_tip, tx_hash, raw_tx) VALUES ($1, '\x00', $2, 'w1', 11155111, '0xa', '0xb', 1, 0, 21000, 1, 1, '0xc', '0xd')`
	accepts(t, db, ins, "r1", "WITHDRAWAL")
	rejects(t, db, "a request ID signs once", ins, "r1", "WITHDRAWAL")
	rejects(t, db, "known purposes only", ins, "r2", "GIFT")
	rejects(t, db, "signatures are append-only", `UPDATE signatures SET to_address = '0xe'`)
	rejects(t, db, "no deletes", `DELETE FROM signatures`)
	rejects(t, db, "no truncation", `TRUNCATE signatures`)
	accepts(t, db, `INSERT INTO refusals (request_id, purpose, reference, reason, request) VALUES ('r3', 'SWEEP', 's1', 'why', '{}')`)
	rejects(t, db, "refusals are append-only", `DELETE FROM refusals`)
}

func TestAdminSchema(t *testing.T) {
	db := apply(t, migrations.Admin())
	a, b := uuid.New(), uuid.New()
	ins := `INSERT INTO admins (id, email, name, role, password_hash, totp_sealed, created_at) VALUES ($1, $2, 'x', $3, 'h', '\x00', now())`
	accepts(t, db, ins, a, "ann@example.com", "ADMIN")
	accepts(t, db, ins, b, "bob@example.com", "FINANCE")
	rejects(t, db, "emails are unique regardless of case", ins, uuid.New(), "Ann@Example.com", "AUDITOR")
	rejects(t, db, "known roles only", ins, uuid.New(), "eve@example.com", "ROOT")
	ap := `INSERT INTO approvals (id, kind, payload, reason, status, requested_by, decided_by, created_at) VALUES ($1, 'LEDGER_ADJUSTMENT', '{}', 'r', $2, $3, $4, now())`
	accepts(t, db, ap, uuid.New(), "PENDING", a, nil)
	accepts(t, db, ap, uuid.New(), "EXECUTED", a, b)
	rejects(t, db, "no self-approval", ap, uuid.New(), "EXECUTED", a, a)
	rejects(t, db, "a decided request names who decided", ap, uuid.New(), "REJECTED", a, nil)
	accepts(t, db, ap, uuid.New(), "REJECTED", a, a) // a requester withdraws their own request
	single := `INSERT INTO approvals (id, kind, payload, reason, status, requested_by, decided_by, created_at, mode, value_usdt)
		VALUES ($1, 'LEDGER_ADJUSTMENT', '{}', 'r', 'EXECUTED', $2, $2, now(), $3, 12.5)`
	accepts(t, db, single, uuid.New(), a, "SINGLE")
	rejects(t, db, "known modes only", single, uuid.New(), a, "ALONE")

	settings := `INSERT INTO settings (single_max_usdt, daily_max_usdt, withdrawal_max_usdt, updated_by, updated_at) VALUES ($1, $2, 1, 'x', now())`
	rejects(t, db, "a day's limit covers one operation", settings, 100, 50)
	rejects(t, db, "positive limits", settings, 0, 50)
	accepts(t, db, settings, 100, 500)
	rejects(t, db, "one row of settings", settings, 100, 500)

	user := uuid.New()
	note := `INSERT INTO user_notes (id, user_id, admin_id, body, created_at) VALUES ($1, $2, $3, $4, now())`
	accepts(t, db, note, uuid.New(), user, a, "called about a deposit")
	rejects(t, db, "a note says something", note, uuid.New(), user, a, "")
	tag := `INSERT INTO user_tags (user_id, tag, added_by, added_at) VALUES ($1, $2, $3, now())`
	accepts(t, db, tag, user, "VIP", a)
	rejects(t, db, "a tag once per account", tag, user, "VIP", b)
	rejects(t, db, "tags are upper case codes", tag, user, "vip", a)
}
