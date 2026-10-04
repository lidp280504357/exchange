package backends_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/skill/exchange/internal/admin/adapters/backends"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func TestFlags(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Config(), log); err != nil {
		t.Fatal(err)
	}
	f := backends.Flags{DB: db, Events: event.NewFactory("admin-test", "t")}
	list, err := f.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(flags.Known) {
		t.Fatalf("%d flags listed, want the %d known ones", len(list), len(flags.Known))
	}
	for _, fl := range list {
		if fl.Enabled || fl.Version != 0 || fl.UpdatedAt != nil {
			t.Fatalf("a flag never set is not off: %+v", fl)
		}
	}
	if _, err := f.Switch(ctx, "no.such.flag", true, "ann@example.com", "test"); err == nil {
		t.Fatal("an unknown flag was switched")
	}
	on, err := f.Switch(ctx, flags.KeyReferenceKline, true, "ann@example.com", "show candles")
	if err != nil {
		t.Fatal(err)
	}
	if !on.Enabled || on.Version != 1 || on.UpdatedBy != "ann@example.com" || on.Description != flags.Known[flags.KeyReferenceKline] {
		t.Fatalf("switched on: %+v", on)
	}
	off, err := f.Switch(ctx, flags.KeyReferenceKline, false, "bob@example.com", "hide them")
	if err != nil || off.Enabled || off.Version != 2 {
		t.Fatalf("switched off: %+v, %v", off, err)
	}
	var changes, audits int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM flag_changes), (SELECT count(*) FROM outbox WHERE topic = 'audit.events')`).
		Scan(&changes, &audits); err != nil {
		t.Fatal(err)
	}
	if changes != 2 || audits != 2 {
		t.Fatalf("%d changes and %d audit events, want 2 and 2", changes, audits)
	}
}
