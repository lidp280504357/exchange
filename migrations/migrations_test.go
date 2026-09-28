package migrations_test

import (
	"context"
	"io/fs"
	"log/slog"
	"testing"

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
		"instrument": migrations.Instrument(),
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
}
