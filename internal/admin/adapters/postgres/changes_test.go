package postgres_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func TestInstrumentChanges(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("admin-test", "t"))
	now := time.Now().UTC().Truncate(time.Microsecond)
	var boss, deputy domain.Admin
	for i, email := range []string{"boss@example.com", "deputy@example.com"} {
		a, err := domain.NewAdmin(uuid.Must(uuid.NewV7()).String(), email, "Test", domain.RoleAdmin, now)
		if err != nil {
			t.Fatal(err)
		}
		a.PasswordHash, a.TOTPSealed = "h", []byte{1}
		if err := store.Read().Admins().Insert(ctx, a); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			boss = a
		} else {
			deputy = a
		}
	}

	// The settings keep the delay; the default until changed is 5 minutes.
	set := domain.DefaultSettings()
	set.ChangeDelay, set.UpdatedBy, set.UpdatedAt = 2*time.Minute, "boss@example.com", now
	if err := store.Read().Settings().Put(ctx, set); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Read().Settings().Get(ctx); err != nil || got.ChangeDelay != 2*time.Minute {
		t.Fatalf("settings %+v %v", got, err)
	}

	changes := store.Read().Changes()
	summary := json.RawMessage(`{"fingerprint":"f","params":[],"impacts":[],"items":[]}`)
	scheduled := domain.NewInstrumentChange(uuid.Must(uuid.NewV7()).String(), domain.ChangeConfig, "instruments", json.RawMessage(`{"pairs":[]}`),
		summary, "maker fee up", boss.ID, false, time.Minute, now)
	pending := domain.NewInstrumentChange(uuid.Must(uuid.NewV7()).String(), domain.ChangePairStatus, "pair:BTC-USDT",
		json.RawMessage(`{"symbol":"BTC-USDT","from":"HALT","to":"TRADING"}`), summary, "resume", boss.ID, true, time.Minute, now.Add(time.Second))
	for _, c := range []domain.InstrumentChange{scheduled, pending} {
		if err := changes.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := changes.Open(ctx); err != nil || n != 2 {
		t.Fatalf("open %d %v", n, err)
	}
	got, err := changes.Get(ctx, pending.ID)
	if err != nil || got == nil || got.Status != domain.ChangePendingApproval || got.RequestedByEmail != "boss@example.com" ||
		!got.EffectiveAt.IsZero() || string(got.Payload) != `{"to": "TRADING", "from": "HALT", "symbol": "BTC-USDT"}` {
		t.Fatalf("pending %+v %v", got, err)
	}
	if none, err := changes.Get(ctx, "not-a-uuid"); err != nil || none != nil {
		t.Fatalf("a bad id %+v %v", none, err)
	}

	// Due locks what is due and skips what another transaction holds.
	if due, err := changes.Due(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("nothing due yet: %+v %v", due, err)
	}
	later := now.Add(2 * time.Minute)
	err = store.Tx(ctx, func(r ports.Repos) error {
		due, err := r.Changes().Due(ctx, later, 10)
		if err != nil || len(due) != 1 || due[0].ID != scheduled.ID {
			t.Fatalf("due %+v %v", due, err)
		}
		// Another round meanwhile sees it locked.
		if err := store.Tx(ctx, func(r2 ports.Repos) error {
			other, err := r2.Changes().Due(ctx, later, 10)
			if err != nil || len(other) != 0 {
				t.Fatalf("a second round %+v %v", other, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		c := due[0]
		c.Settle(true, "1 changed, 0 unchanged", later)
		return r.Changes().Update(ctx, c)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Approve, then a list with its emails, newest first, paged.
	if err := pending.Approve(deputy.ID, time.Minute, later); err != nil {
		t.Fatal(err)
	}
	if err := changes.Update(ctx, pending); err != nil {
		t.Fatal(err)
	}
	list, err := changes.List(ctx, "", time.Time{}, "", 1)
	if err != nil || len(list) != 1 || list[0].ID != pending.ID || list[0].ApprovedByEmail != "deputy@example.com" || list[0].Status != domain.ChangeScheduled {
		t.Fatalf("first page %+v %v", list, err)
	}
	list, err = changes.List(ctx, "", list[0].CreatedAt, list[0].ID, 10)
	if err != nil || len(list) != 1 || list[0].ID != scheduled.ID || list[0].Status != domain.ChangeApplied || list[0].Result != "1 changed, 0 unchanged" {
		t.Fatalf("second page %+v %v", list, err)
	}
	if list, err := changes.List(ctx, domain.ChangeApplied, time.Time{}, "", 10); err != nil || len(list) != 1 {
		t.Fatalf("applied %+v %v", list, err)
	}
	// The database refuses an approval by the requester.
	pending.ApprovedBy = boss.ID
	if err := changes.Update(ctx, pending); err == nil {
		t.Fatal("a requester approved their own change")
	}

	// A confirmation confirms one change; a round's claim keeps other
	// rounds off it for a while (C5.5 ⑩).
	confirmed := domain.NewInstrumentChange(uuid.Must(uuid.NewV7()).String(), domain.ChangePairStatus, "pair:ETH-BTC",
		json.RawMessage(`{"symbol":"ETH-BTC","from":"HALT","to":"TRADING"}`), summary, "resume", boss.ID, false, time.Minute, now)
	confirmed.ConfirmationHash = "hash-1"
	if err := changes.Create(ctx, confirmed); err != nil {
		t.Fatal(err)
	}
	twice := confirmed
	twice.ID = uuid.Must(uuid.NewV7()).String()
	if _, dup := pg.UniqueViolation(changes.Create(ctx, twice)); !dup {
		t.Fatal("a confirmation confirmed a second change")
	}
	if got, err := changes.ByConfirmation(ctx, "hash-1"); err != nil || got == nil || got.ID != confirmed.ID || got.ConfirmationHash != "hash-1" {
		t.Fatalf("by its confirmation %+v %v", got, err)
	}
	if got, err := changes.ByConfirmation(ctx, "hash-2"); err != nil || got != nil {
		t.Fatalf("an unknown confirmation %+v %v", got, err)
	}
	due := later.Add(time.Minute)
	confirmed.Claim(due)
	if err := changes.Update(ctx, confirmed); err != nil {
		t.Fatal(err)
	}
	if list, err := changes.Due(ctx, due.Add(domain.ClaimHold/2), 10); err != nil || len(list) != 1 || list[0].ID != pending.ID {
		t.Fatalf("claimed a moment ago %+v %v", list, err)
	}
	list, err = changes.Due(ctx, due.Add(domain.ClaimHold), 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("a claim left %+v %v", list, err)
	}
	for _, c := range list {
		if c.ID == confirmed.ID && !c.ApplyingAt.Equal(due) {
			t.Fatalf("its claim %v", c.ApplyingAt)
		}
	}
}
