package main

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
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
