package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func TestWalletCommandsQueue(t *testing.T) {
	db, idb := testenv.Postgres(t), testenv.Postgres(t)
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := walletWith(context.Background(), db, idb, args, &out); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	if out := run("sweep", "--min", "0.002"); !strings.HasPrefix(out, "queued SWEEP") {
		t.Fatal(out)
	}
	if out := run("fund", "--tx", "0x"+strings.Repeat("a", 64)); !strings.HasPrefix(out, "queued FUND") {
		t.Fatal(out)
	}
	if out := run("reconcile"); !strings.Contains(out, "no chain check yet") || strings.Contains(out, "backfilled") {
		t.Fatal(out)
	}
	if out := run("checks", "--network", "UDUN"); !strings.Contains(out, "no backfilled deposit waits for its callback") {
		t.Fatal(out)
	}
	out := run("commands")
	if strings.Count(out, "PENDING") != 3 || !strings.Contains(out, "SWEEP") || !strings.Contains(out, "cli:") {
		t.Fatal(out)
	}
	var buf bytes.Buffer
	if err := walletWith(context.Background(), db, idb, []string{"fund"}, &buf); err == nil {
		t.Fatal("fund needs --tx")
	}

	// The custodian's fees (review ④): none held; a fee unit only for a
	// network the custodian serves.
	if out := run("custody-fees"); !strings.Contains(out, "no custodian fee waits") {
		t.Fatal(out)
	}
	if out := run("custody-fee-unit"); !strings.Contains(out, "no fee unit confirmed") {
		t.Fatal(out)
	}
	unit := []string{"custody-fee-unit", "--asset", "USDT", "--network", "TRON", "--unit", "self", "--reason", "the first withdrawal"}
	if err := walletWith(context.Background(), db, idb, unit, &buf); err == nil || !strings.Contains(err.Error(), "does not serve") {
		t.Fatalf("no such network yet: %v", err)
	}
	seed, err := os.ReadFile("../../deploy/instruments/test.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := instrumentsWith(context.Background(), idb, []string{"apply", "--file", "-", "--reason", "seed"}, bytes.NewReader(seed), &buf); err != nil {
		t.Fatal(err)
	}
	if out := run(unit...); !strings.Contains(out, "USDT TRON as SELF") {
		t.Fatal(out)
	}
	if out := run("custody-fee-unit"); !strings.Contains(out, "SELF") || !strings.Contains(out, "the first withdrawal") {
		t.Fatal(out)
	}
	// An asset's withdrawals suspended and resumed by hand (review B4).
	if out := run("withdrawals-suspended"); !strings.Contains(out, "no asset's withdrawals are suspended") {
		t.Fatal(out)
	}
	if out := run("withdrawals-suspend", "--asset", "usdt", "--reason", "the custodian's incident"); !strings.Contains(out, "withdrawals of USDT suspended") {
		t.Fatal(out)
	}
	if out := run("withdrawals-suspended"); !strings.Contains(out, "USDT") || !strings.Contains(out, "the custodian's incident") {
		t.Fatal(out)
	}
	if out := run("withdrawals-resume", "--asset", "USDT", "--reason", "it is over"); !strings.Contains(out, "withdrawals of USDT resumed") {
		t.Fatal(out)
	}
	if err := walletWith(context.Background(), db, idb, []string{"withdrawals-resume", "--asset", "USDT", "--reason", "again"}, &buf); err == nil {
		t.Fatal("resumed twice")
	}
	if err := walletWith(context.Background(), db, idb, []string{
		"custody-fee", "0190a0b0-0000-7000-8000-000000000000", "--write-off",
		"--reason", "nothing",
	}, &buf); err == nil || !strings.Contains(err.Error(), "no such withdrawal") {
		t.Fatalf("no such withdrawal: %v", err)
	}
}
