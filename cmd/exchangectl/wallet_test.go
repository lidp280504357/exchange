package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func TestWalletCommandsQueue(t *testing.T) {
	db := testenv.Postgres(t)
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := walletWith(context.Background(), db, args, &out); err != nil {
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
	if err := walletWith(context.Background(), db, []string{"fund"}, &buf); err == nil {
		t.Fatal("fund needs --tx")
	}
}
