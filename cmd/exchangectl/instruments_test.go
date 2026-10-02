package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func TestInstrumentsApplyTestData(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	seed, err := os.ReadFile("../../deploy/instruments/test.json")
	if err != nil {
		t.Fatal(err)
	}
	run := func(in []byte, args ...string) (string, error) {
		var out bytes.Buffer
		err := instrumentsWith(ctx, db, args, bytes.NewReader(in), &out)
		return out.String(), err
	}
	out, err := run(seed, "apply", "--file", "-", "--reason", "seed")
	if err != nil || !strings.Contains(out, "0 unchanged") {
		t.Fatalf("first apply: %v\n%s", err, out)
	}
	if out, err = run(seed, "apply", "--file", "-", "--reason", "seed"); err != nil || !strings.Contains(out, "0 changed") {
		t.Fatalf("second apply: %v\n%s", err, out)
	}
	if out, err = run(seed, "apply", "--file", "-", "--reason", "preview", "--dry-run"); err != nil || !strings.Contains(out, "0 would change, 0 kept") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if out, err = run(nil, "pair-status", "BTC-USDT", "--to", "TRADING", "--reason", "open"); err != nil || !strings.Contains(out, "PREPARE -> TRADING") {
		t.Fatalf("pair-status: %v\n%s", err, out)
	}
	if out, err = run(nil, "list"); err != nil || !strings.Contains(out, "ETH-SEPOLIA") || !strings.Contains(out, "TRADING") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if _, err = run([]byte(`{"assets": [{"asset_code": "X"}], "extra": 1}`), "apply", "--file", "-", "--reason", "bad"); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
}
