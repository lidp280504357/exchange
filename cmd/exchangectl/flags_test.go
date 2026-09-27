package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func cli(t *testing.T, db *pg.DB, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := flagsCmd(context.Background(), settings{}, db, args, &out)
	return out.String(), err
}

func TestFlagsSetUpdatesOnlyGivenOptions(t *testing.T) {
	db := testenv.Postgres(t)

	out, err := cli(t, db, "set", flags.KeyTransfer, "--on", "--deny-regions", "KP,IR", "--reason", "open transfers")
	if err != nil {
		t.Fatalf("set: %v\n%s", err, out)
	}
	// Kafka is not configured here, so the audit event waits in the outbox.
	if !strings.Contains(out, "stays queued") {
		t.Fatalf("expected a queued-audit warning:\n%s", out)
	}

	out, err = cli(t, db, "set", flags.KeyTransfer, "--allow-users", "u-1", "--reason", "only u-1")
	if err != nil {
		t.Fatalf("second set: %v\n%s", err, out)
	}
	var f flags.Flag
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&f); err != nil {
		t.Fatalf("output is not a flag: %v\n%s", err, out)
	}
	if !f.Enabled || f.Version != 2 || f.Rules.Regions == nil || len(f.Rules.Regions.Deny) != 2 || f.Rules.Users.Allow[0] != "u-1" {
		t.Fatalf("second set must keep earlier options: %+v", f)
	}

	var pending int
	if err := db.QueryRow(context.Background(),
		"SELECT count(*) FROM outbox WHERE event_type = 'audit.ConfigChanged' AND published_at IS NULL").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 2 {
		t.Fatalf("each change must queue one audit event, got %d", pending)
	}

	if out, err = cli(t, db, "list"); err != nil || !strings.Contains(out, flags.KeyTransfer) || !strings.Contains(out, "off (unset)") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if out, err = cli(t, db, "history", flags.KeyTransfer); err != nil || !strings.Contains(out, "only u-1") {
		t.Fatalf("history: %v\n%s", err, out)
	}
}

func TestFlagsSetRejectsBadInput(t *testing.T) {
	db := testenv.Postgres(t)
	for name, args := range map[string][]string{
		"unknown key": {"set", "made.up", "--on", "--reason", "x"},
		"no reason":   {"set", flags.KeyTransfer, "--on"},
		"on and off":  {"set", flags.KeyTransfer, "--on", "--off", "--reason", "x"},
		"no key":      {"set", "--on"},
	} {
		if _, err := cli(t, db, args...); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
