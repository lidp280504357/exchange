package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	instrumentpg "github.com/lidp280504357/exchange/internal/instrument/adapters/postgres"
	instrumentapp "github.com/lidp280504357/exchange/internal/instrument/application"
	"github.com/lidp280504357/exchange/internal/ledger/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/ledger/application"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func TestLedgerAdjust(t *testing.T) {
	ctx := context.Background()
	dbs := ledgerDBs{ledger: testenv.Postgres(t), instrument: testenv.Postgres(t), config: testenv.Postgres(t)}
	seed, err := os.ReadFile("../../deploy/instruments/test.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := instrumentsWith(ctx, dbs.instrument, []string{"apply", "--file", "-", "--reason", "seed"}, bytes.NewReader(seed), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := ledgerWith(ctx, dbs, args, &out)
		return out.String(), err
	}
	user := uuid.NewString()
	if _, err := run("adjust", "--user", user, "--asset", "USDT", "--amount", "100", "--reason", "test"); err == nil {
		t.Fatal("adjustments need the flag")
	}
	if out, err := cli(t, dbs.config, "set", flags.KeyManualAdjustment, "--on", "--reason", "test"); err != nil {
		t.Fatalf("flag: %v\n%s", err, out)
	}
	out, err := run("adjust", "--user", user, "--asset", "USDT", "--amount", "100.5", "--reason", "test credit", "--key", "k1")
	if err != nil || !strings.Contains(out, "replayed false") {
		t.Fatalf("adjust: %v\n%s", err, out)
	}
	if out, err = run("adjust", "--user", user, "--asset", "USDT", "--amount", "100.5", "--reason", "test credit", "--key", "k1"); err != nil || !strings.Contains(out, "replayed true") {
		t.Fatalf("retry: %v\n%s", err, out)
	}
	if _, err = run("adjust", "--user", user, "--asset", "USDT", "--amount", "0.0000001", "--reason", "too fine"); err == nil {
		t.Fatal("precision must be checked")
	}
	if out, err = run("balances", user); err != nil || !strings.Contains(out, "100.5") {
		t.Fatalf("balances: %v\n%s", err, out)
	}
	if out, err = run("reconcile"); err != nil || !regexp.MustCompile(`ACCOUNT_MATCHES_LINES\s+0 mismatches`).MatchString(out) ||
		!regexp.MustCompile(`TRADE_SETTLE_MATCHES_TRADES\s+0 mismatches`).MatchString(out) {
		t.Fatalf("reconcile: %v\n%s", err, out)
	}
	if out, err = run("trades", "--failed"); err != nil || strings.TrimSpace(out) != "TRADE  SYMBOL  NO  PRICE  QUANTITY  STATUS  ATTEMPTS  ERROR" {
		t.Fatalf("trades: %v\n%q", err, out)
	}
	if out, err = run("retry-trades"); err != nil || out != "settled 0, still failed 0\n" {
		t.Fatalf("retry-trades: %v\n%s", err, out)
	}
}

// TestLedgerReleaseHold releases a stuck hold's own part of the frozen
// balance, leaving a live order's freeze where it is (C5.5 ⑯).
func TestLedgerReleaseHold(t *testing.T) {
	ctx := context.Background()
	schemas := map[string]*pg.DB{}
	open := func(schema string) (*pg.DB, error) {
		if db, ok := schemas[schema]; ok {
			return db, nil
		}
		db := testenv.Postgres(t)
		set := map[string]fs.FS{"trading": migrations.Trading(), "wallet": migrations.Wallet()}[schema]
		if err := migrate.Up(ctx, db, set, quiet); err != nil {
			return nil, err
		}
		schemas[schema] = db
		return db, nil
	}
	dbs := ledgerDBs{ledger: testenv.Postgres(t), instrument: testenv.Postgres(t), config: testenv.Postgres(t), open: open}
	seed, err := os.ReadFile("../../deploy/instruments/test.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := instrumentsWith(ctx, dbs.instrument, []string{"apply", "--file", "-", "--reason", "seed"}, bytes.NewReader(seed), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := ledgerWith(ctx, dbs, args, &out)
		return out.String(), err
	}
	if _, err := run("balances", uuid.NewString()); err != nil { // migrates the ledger
		t.Fatal(err)
	}
	svc := &application.Service{
		Store:  postgres.NewStore(dbs.ledger, event.NewFactory("exchangectl-test", "t")),
		Assets: instrumentAssets{svc: &instrumentapp.Service{Store: instrumentpg.NewStore(dbs.instrument, nil)}},
		Flags:  staticFlags{}, Now: time.Now,
	}
	user, hold := uuid.NewString(), uuid.NewString()
	if _, err := svc.CreditDeposit(ctx, uuid.NewString(), domain.Deposit{
		ID: uuid.NewString(), UserID: user, Asset: "USDT", Amount: decimal.NewFromInt(1000),
		Network: "TRON", TxHash: "t1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlaceHold(ctx, hold, user, "USDT", decimal.NewFromInt(100), "risk@example.com", "chargeback under review"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Freeze(ctx, "order-live", domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", decimal.NewFromInt(30), "a live order"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Unfreeze(ctx, "stray", domain.EntryOrderUnfreeze, user, domain.AccountSpot, "USDT", decimal.NewFromInt(40), "a stray unfreeze"); err != nil {
		t.Fatal(err)
	}
	trading, err := open("trading")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := trading.Exec(ctx, `INSERT INTO orders (id, user_id, client_order_id, symbol, side, type, time_in_force, stp, price, quantity,
		status, frozen_asset, frozen_amount, freeze_state, maker_fee_rate, taker_fee_rate, base_decimals, quote_decimals, created_at, updated_at)
		VALUES ($1, $2, 'live', 'BTC-USDT', 'BUY', 'LIMIT', 'GTC', 'CANCEL_NEWEST', 30000, 0.001, 'OPEN', 'USDT', 30, 'FROZEN', 0, 0, 6, 2, $3, $3)`,
		uuid.NewString(), user, now); err != nil {
		t.Fatal(err)
	}
	// The live order moves the frozen balance meanwhile: not without
	// --force (C5.5 ⑱).
	if out, err := run("release-hold", "--id", hold, "--reason", "cleared, 40 went elsewhere"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("an order in flight: %v\n%s", err, out)
	}
	if _, err := run("release-hold", "--id", hold, "--amount", "61", "--force", "--reason", "more than its part"); err == nil {
		t.Fatal("released more than the hold's part")
	}
	out, err := run("release-hold", "--id", hold, "--force", "--reason", "cleared, 40 went elsewhere")
	if err != nil || !strings.Contains(out, "spot orders 30 (1)") || !strings.Contains(out, "at most 60") || !strings.Contains(out, "released: 60 of 100") {
		t.Fatalf("release-hold: %v\n%s", err, out)
	}
	if out, err = run("balances", user); err != nil || !strings.Contains(out, "30") {
		t.Fatalf("the order's 30 still frozen: %v\n%s", err, out)
	}
}
