package flags_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func TestAllows(t *testing.T) {
	cn := flags.Subject{UserID: "u-1", Region: "CN", Status: "ACTIVE", Asset: "USDT", Symbol: "BTC-USDT"}
	cases := []struct {
		name string
		flag flags.Flag
		subj flags.Subject
		want bool
	}{
		{"disabled", flags.Flag{}, cn, false},
		{"enabled without rules", flags.Flag{Enabled: true}, cn, true},
		{"enabled, empty subject", flags.Flag{Enabled: true}, flags.Subject{}, true},
		{"region allowed", flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Allow: []string{"CN", "US"}}}}, cn, true},
		{"region not in allow", flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Allow: []string{"US"}}}}, cn, false},
		{"region denied", flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Deny: []string{"CN"}}}}, cn, false},
		{"deny beats allow", flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Allow: []string{"CN"}, Deny: []string{"CN"}}}}, cn, false},
		{"unknown region fails closed", flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Deny: []string{"KP"}}}}, flags.Subject{}, false},
		{"empty list does not constrain", flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{}}}, flags.Subject{}, true},
		{"status gate", flags.Flag{Enabled: true, Rules: flags.Rules{Statuses: &flags.List{Allow: []string{"ACTIVE"}}}}, flags.Subject{Status: "FROZEN"}, false},
		{"asset halted", flags.Flag{Enabled: true, Rules: flags.Rules{Assets: &flags.List{Deny: []string{"USDT"}}}}, cn, false},
		{"symbol halted", flags.Flag{Enabled: true, Rules: flags.Rules{Symbols: &flags.List{Deny: []string{"BTC-USDT"}}}}, cn, false},
		{"user whitelist hit", flags.Flag{Enabled: true, Rules: flags.Rules{Users: &flags.List{Allow: []string{"u-1"}}}}, cn, true},
		{"user whitelist miss", flags.Flag{Enabled: true, Rules: flags.Rules{Users: &flags.List{Allow: []string{"u-2"}}}}, cn, false},
		{"every dimension must pass", flags.Flag{Enabled: true, Rules: flags.Rules{
			Users: &flags.List{Allow: []string{"u-1"}}, Regions: &flags.List{Deny: []string{"CN"}},
		}}, cn, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.flag.Allows(tc.subj); got != tc.want {
				t.Fatalf("Allows = %v, want %v", got, tc.want)
			}
		})
	}
}

func setup(t *testing.T) *pg.DB {
	t.Helper()
	db := testenv.Postgres(t)
	if err := migrate.Up(context.Background(), db, migrations.Config(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return db
}

func set(t *testing.T, db *pg.DB, f flags.Flag, reason string) (*flags.Flag, flags.Flag) {
	t.Helper()
	var old *flags.Flag
	var stored flags.Flag
	err := db.InTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		old, stored, err = flags.Set(context.Background(), tx, f, "cli:tester", reason)
		return err
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	return old, stored
}

func TestSetVersionsAndHistory(t *testing.T) {
	db := setup(t)
	ctx := context.Background()

	old, first := set(t, db, flags.Flag{Key: flags.KeyTransfer, Enabled: true}, "open transfers")
	if old != nil || first.Version != 1 || !first.Enabled || first.UpdatedBy != "cli:tester" {
		t.Fatalf("create: old=%v stored=%+v", old, first)
	}
	old, second := set(t, db, flags.Flag{
		Key: flags.KeyTransfer, Enabled: true,
		Rules: flags.Rules{Regions: &flags.List{Deny: []string{"KP"}}},
	}, "block KP")
	if old == nil || old.Version != 1 || second.Version != 2 {
		t.Fatalf("update: old=%v stored=%+v", old, second)
	}

	all, err := flags.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	got := all[flags.KeyTransfer]
	if got.Rules.Regions == nil || got.Rules.Regions.Deny[0] != "KP" || got.Allows(flags.Subject{Region: "KP"}) {
		t.Fatalf("loaded flag lost its rules: %+v", got)
	}
	history, err := flags.History(ctx, db, flags.KeyTransfer, 10)
	if err != nil || len(history) != 2 || history[0].Reason != "block KP" || history[1].Old != nil {
		t.Fatalf("history = %+v, %v", history, err)
	}

	if err := db.InTx(ctx, func(tx pgx.Tx) error {
		_, _, err := flags.Set(ctx, tx, flags.Flag{Key: "x"}, "", "")
		return err
	}); err == nil {
		t.Fatal("actor and reason are required")
	}
}

func TestClientRefreshes(t *testing.T) {
	db := setup(t)
	c := flags.NewClient(db, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	ctx := context.Background()
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Enabled(flags.KeyWithdraw, flags.Subject{}) {
		t.Fatal("a missing flag must be off")
	}
	set(t, db, flags.Flag{Key: flags.KeyWithdraw, Enabled: true}, "phase 2 test")
	if c.Enabled(flags.KeyWithdraw, flags.Subject{}) {
		t.Fatal("the local copy only changes on refresh")
	}
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !c.Enabled(flags.KeyWithdraw, flags.Subject{}) {
		t.Fatal("refresh must pick up the change")
	}
	if f, ok := c.Get(flags.KeyWithdraw); !ok || f.Version != 1 {
		t.Fatalf("Get = %+v %v", f, ok)
	}
}

// TestProductLinesSeededOpen: the product lines are seeded on (migration
// config 00002), a closed one says so, and one that is not stored counts as
// open (design 2026-10-07, product switches).
func TestProductLinesSeededOpen(t *testing.T) {
	db := setup(t)
	c := flags.NewClient(db, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	ctx := context.Background()
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range flags.ProductKeys {
		if f, ok := c.Get(key); !ok || !f.Enabled || c.Closed(key) {
			t.Fatalf("%s seeded: %+v %v", key, f, ok)
		}
	}
	if c.Closed("product.unknown") {
		t.Fatal("a product line that is not stored is open")
	}
	set(t, db, flags.Flag{Key: flags.KeyProductSpot, Enabled: false}, "close spot")
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !c.Closed(flags.KeyProductSpot) || c.Closed(flags.KeyProductUSDTM) {
		t.Fatal("only spot is closed")
	}
	err := flags.ErrProductClosed(flags.KeyProductSpot)
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != flags.CodeProductClosed || e.Details["product"] != "spot" || e.Kind.HTTPStatus() != http.StatusForbidden {
		t.Fatalf("closed: %v", err)
	}
}

func TestDenialNamesTheRule(t *testing.T) {
	f := flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Deny: []string{"US"}}, Statuses: &flags.List{Allow: []string{"ACTIVE"}}}}
	for _, tc := range []struct {
		s    flags.Subject
		want string
	}{
		{flags.Subject{Region: "SG", Status: "ACTIVE"}, ""},
		{flags.Subject{Region: "US", Status: "ACTIVE"}, "region"},
		{flags.Subject{Region: "SG", Status: "FROZEN"}, "status"},
		{flags.Subject{Status: "ACTIVE"}, "region"}, // a constrained dimension without a value fails closed
	} {
		if got := f.Denial(tc.s); got != tc.want {
			t.Errorf("Denial(%+v) = %q, want %q", tc.s, got, tc.want)
		}
	}
	f.Enabled = false
	if got := f.Denial(flags.Subject{Region: "SG", Status: "ACTIVE"}); got != "disabled" {
		t.Fatalf("disabled flag: %q", got)
	}
}
