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
	walletpg "github.com/lidp280504357/exchange/internal/wallet/adapters/postgres"
	walletdomain "github.com/lidp280504357/exchange/internal/wallet/domain"
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

	// Nothing in flight: no --force needed (C5.5 ⑳).
	quiet, quietHold := uuid.NewString(), uuid.NewString()
	if _, err := svc.CreditDeposit(ctx, uuid.NewString(), domain.Deposit{
		ID: uuid.NewString(), UserID: quiet, Asset: "USDT", Amount: decimal.NewFromInt(1000), Network: "TRON", TxHash: "t2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlaceHold(ctx, quietHold, quiet, "USDT", decimal.NewFromInt(100), "risk@example.com", "chargeback under review"); err != nil {
		t.Fatal(err)
	}
	if out, err := run("release-hold", "--id", quietHold, "--amount", "10", "--reason", "nothing else is frozen"); err != nil ||
		!strings.Contains(out, "released: 10 of 100") || !strings.Contains(out, "(0)") {
		t.Fatalf("nothing in flight: %v\n%s", err, out)
	}

	// A pending order, a frozen withdrawal and a trade the ledger has not
	// settled each keep their part, and want --force (C5.5 ⑳).
	busy, busyHold, trade := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := svc.CreditDeposit(ctx, uuid.NewString(), domain.Deposit{
		ID: uuid.NewString(), UserID: busy, Asset: "USDT", Amount: decimal.NewFromInt(1000), Network: "TRON", TxHash: "t3",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlaceHold(ctx, busyHold, busy, "USDT", decimal.NewFromInt(100), "risk@example.com", "chargeback under review"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		key    string
		amount int64
	}{{"order-pending", 20}, {"withdrawal-frozen", 15}, {"order-filled", 25}, {"order-improved", 26}} {
		if _, err := svc.Freeze(ctx, f.key, domain.EntryOrderFreeze, busy, domain.AccountSpot, "USDT", decimal.NewFromInt(f.amount), f.key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Unfreeze(ctx, "stray-busy", domain.EntryOrderUnfreeze, busy, domain.AccountSpot, "USDT", decimal.NewFromInt(40), "a stray unfreeze"); err != nil {
		t.Fatal(err)
	}
	if _, err := trading.Exec(ctx, `INSERT INTO orders (id, user_id, client_order_id, symbol, side, type, time_in_force, stp, price, quantity,
		status, frozen_asset, frozen_amount, freeze_state, maker_fee_rate, taker_fee_rate, base_decimals, quote_decimals, created_at, updated_at)
		VALUES ($1, $2, 'pending', 'BTC-USDT', 'BUY', 'LIMIT', 'GTC', 'CANCEL_NEWEST', 20000, 0.001, 'NEW', 'USDT', 20, 'PENDING', 0, 0, 6, 2, $3, $3)`,
		uuid.NewString(), busy, now); err != nil {
		t.Fatal(err)
	}
	if _, err := trading.Exec(ctx, `INSERT INTO fills (trade_id, order_id, user_id, symbol, side, maker, price, quantity, quote_quantity, fee_asset,
		fee, sequence, executed_at) VALUES ($1, $2, $3, 'BTC-USDT', 'BUY', false, 25000, 0.001, 25, 'BTC', 0, 1, $4)`,
		trade, uuid.NewString(), busy, now); err != nil {
		t.Fatal(err)
	}
	// A limit buy at 26,000 filled at 25,000 keeps its 26 frozen until the
	// ledger settles it and releases the 1 (C5.5 ㉒).
	improved := uuid.NewString()
	if _, err := trading.Exec(ctx, `INSERT INTO orders (id, user_id, client_order_id, symbol, side, type, time_in_force, stp, price, quantity,
		status, frozen_asset, frozen_amount, freeze_state, maker_fee_rate, taker_fee_rate, base_decimals, quote_decimals, created_at, updated_at, released)
		VALUES ($1, $2, 'improved', 'BTC-USDT', 'BUY', 'LIMIT', 'GTC', 'CANCEL_NEWEST', 26000, 0.001, 'FILLED', 'USDT', 26, 'FROZEN', 0, 0, 6, 2, $3, $3,
		true)`, improved, busy, now); err != nil {
		t.Fatal(err)
	}
	if _, err := trading.Exec(ctx, `INSERT INTO fills (trade_id, order_id, user_id, symbol, side, maker, price, quantity, quote_quantity, fee_asset,
		fee, sequence, executed_at) VALUES ($1, $2, $3, 'BTC-USDT', 'BUY', false, 25000, 0.001, 25, 'BTC', 0, 2, $4)`,
		uuid.NewString(), improved, busy, now); err != nil {
		t.Fatal(err)
	}
	wallet, err := open("wallet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wallet.Exec(ctx, `INSERT INTO withdrawals (id, user_id, asset, network, address, amount, fee, status, required_confirmations,
		freeze_journal_id, created_at, updated_at) VALUES ($1, $2, 'USDT', 'TRON', 'TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj', 14, 1, 'PENDING_REVIEW', 20,
		$3, $4, $4)`, uuid.NewString(), busy, uuid.NewString(), now); err != nil {
		t.Fatal(err)
	}
	if out, err := run("release-hold", "--id", busyHold, "--reason", "cleared, 40 went elsewhere"); err == nil ||
		!strings.Contains(err.Error(), "1 orders, 1 withdrawals and 2 trades") {
		t.Fatalf("in flight: %v\n%s", err, out)
	}
	// 146 frozen: 20 + 15 + 25 + 26 is theirs, 60 the hold's.
	out, err = run("release-hold", "--id", busyHold, "--force", "--reason", "cleared, 40 went elsewhere")
	if err != nil || !strings.Contains(out, "spot orders 20 (1)") || !strings.Contains(out, "withdrawals 15 (1)") ||
		!strings.Contains(out, "trades not settled 51 (2)") || !strings.Contains(out, "released: 60 of 100") {
		t.Fatalf("forced: %v\n%s", err, out)
	}
}

// TestLedgerCustodyReset takes a stand-in custodian's simulated deposits out
// of the expectation, records the journal for the wallet's custody check
// once, and puts them back (the real gateway's integration, B1).
func TestLedgerCustodyReset(t *testing.T) {
	ctx := context.Background()
	wallet := testenv.Postgres(t)
	dbs := ledgerDBs{
		ledger: testenv.Postgres(t), instrument: testenv.Postgres(t), config: testenv.Postgres(t),
		open: func(string) (*pg.DB, error) { return wallet, nil },
	}
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
	// The stand-in reported 396.25 USDT deposited.
	if _, err := svc.CreditDeposit(ctx, uuid.NewString(), domain.Deposit{
		ID: uuid.NewString(), UserID: uuid.NewString(), Asset: "USDT", Amount: decimal.RequireFromString("396.25"), Network: "TRON", TxHash: "t1",
	}); err != nil {
		t.Fatal(err)
	}
	reset := []string{"custody-reset", "--asset", "usdt", "--amount", "396.25", "--reason", "the stand-in replaced", "--key", "switch"}
	if _, err := run(reset...); err == nil || !strings.Contains(err.Error(), "no check") {
		t.Fatalf("a reset without a custody check: %v", err)
	}
	// The stand-in's holding at its last check bounds the reset (review AH).
	check := walletdomain.NewChainCheck(walletdomain.ProviderUdun, "USDT", decimal.RequireFromString("396.25"),
		decimal.RequireFromString("396.25"), decimal.Zero, 3, time.Now())
	if err := walletpg.NewStore(wallet, event.NewFactory("exchangectl-test", "t")).Read().Checks().Insert(ctx, check); err != nil {
		t.Fatal(err)
	}
	if _, err := run("custody-reset", "--asset", "USDT", "--amount", "400", "--reason", "more than held", "--key", "x"); err == nil ||
		!strings.Contains(err.Error(), "hides funds missing") {
		t.Fatalf("a reset beyond the holding: %v", err)
	}
	if _, err := run(reset...); err == nil {
		t.Fatal("a reset needs ledger.manual_adjustment")
	}
	if out, err := cli(t, dbs.config, "set", flags.KeyManualAdjustment, "--on", "--reason", "test"); err != nil {
		t.Fatalf("flag: %v\n%s", err, out)
	}
	for _, replayed := range []string{"false", "true"} {
		out, err := run(reset...)
		if err != nil || !strings.Contains(out, "replayed "+replayed) || !strings.Contains(out, "simulated USDT at no custodian: 396.25") {
			t.Fatalf("reset (replayed %s): %v\n%s", replayed, err, out)
		}
	}
	if _, err := run("custody-reset", "--asset", "USDT", "--amount", "1", "--reason", "more", "--key", "more"); err == nil {
		t.Fatal("reset below nothing")
	}
	if _, err := run("custody-reset", "--asset", "USDT", "--amount", "400", "--reason", "too much back", "--reverse"); err == nil {
		t.Fatal("reversed more than was reset")
	}
	out, err := run("custody-reset", "--asset", "USDT", "--amount", "396.25", "--reason", "back to the stand-in", "--reverse")
	if err != nil || !strings.Contains(out, "simulated USDT at no custodian: 0") {
		t.Fatalf("reverse: %v\n%s", err, out)
	}
	if out, err = run("reconcile"); err != nil || !regexp.MustCompile(`ACCOUNT_MATCHES_LINES\s+0 mismatches`).MatchString(out) {
		t.Fatalf("reconcile: %v\n%s", err, out)
	}
}
