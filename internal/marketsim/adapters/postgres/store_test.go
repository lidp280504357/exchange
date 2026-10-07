package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketsim/adapters/postgres"
	"github.com/skill/exchange/internal/marketsim/domain"
	"github.com/skill/exchange/internal/marketsim/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func TestBotsSettingsAndState(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, db, migrations.MarketSim(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, nil)

	// Bots: once per user, a label belongs to one.
	a, b := uuid.NewString(), uuid.NewString()
	if err := store.AddBot(ctx, ports.Bot{UserID: a, Role: domain.RoleMaker, Label: "bot-02", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddBot(ctx, ports.Bot{UserID: a, Role: domain.RoleTaker, Label: "bot-09", Enabled: true}); err != nil {
		t.Fatalf("the same user again: %v", err)
	}
	if err := store.AddBot(ctx, ports.Bot{UserID: b, Role: domain.RoleTaker, Label: "bot-02", Enabled: true}); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("a label in use: %v", err)
	}
	if err := store.AddBot(ctx, ports.Bot{UserID: b, Role: domain.RoleTaker, Label: "bot-01", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	bots, err := store.Bots(ctx)
	if err != nil || len(bots) != 2 || bots[0].Label != "bot-01" || bots[1].UserID != a || bots[1].Role != domain.RoleMaker {
		t.Fatalf("bots %+v %v", bots, err)
	}

	// Settings: none, then versions.
	if _, _, ok, err := store.Settings(ctx); ok || err != nil {
		t.Fatalf("settings before any: %v %v", ok, err)
	}
	p := domain.DefaultParams()
	at := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	if v, err := store.SaveSettings(ctx, p, ports.ParamChange{At: at.Add(-2 * time.Hour), Actor: "market-sim"}, nil); err != nil || v != 1 {
		t.Fatalf("first settings: %d %v", v, err)
	}
	p.Levels, p.P0 = 5, 1.1
	change := ports.ParamChange{At: at, Actor: "ops", ApprovedBy: "ops2", Move: 0.1, Volume: 0.2}
	if v, err := store.SaveSettings(ctx, p, change, nil); err != nil || v != 2 {
		t.Fatalf("second settings: %d %v", v, err)
	}
	got, v, ok, err := store.Settings(ctx)
	if err != nil || !ok || v != 2 || got.Levels != 5 || got.Spread != p.Spread {
		t.Fatalf("settings %+v v%d %v %v", got, v, ok, err)
	}
	changes, err := store.ParamChanges(ctx, at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil || len(changes) != 1 || changes[0].ApprovedBy != "ops2" || changes[0].Move != 0.1 || changes[0].Volume != 0.2 ||
		!changes[0].At.Equal(at) {
		t.Fatalf("changes of the hour %+v %v", changes, err)
	}

	// Samples: a day for the chart, the older ones pruned.
	for i, last := range []string{"0", "1.01", "1.02"} {
		if err := store.SaveSample(ctx, ports.Sample{At: at.Add(time.Duration(i) * time.Hour), Target: 1 + float64(i)/100, Last: decimal.RequireFromString(last)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PruneSamples(ctx, at.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	samples, err := store.Samples(ctx, at)
	if err != nil || len(samples) != 2 || samples[0].Target != 1.01 || !samples[1].Last.Equal(decimal.RequireFromString("1.02")) {
		t.Fatalf("samples %+v %v", samples, err)
	}

	// State: the random source and the minute come back as saved.
	m := domain.NewModel(domain.DefaultParams(), domain.State{}, 9)
	m.Step(at, 60000, 3000, domain.Shape{})
	m.Step(at.Add(time.Second), 60100, 3001, domain.Shape{})
	if err := store.SaveState(ctx, m.Snapshot()); err != nil {
		t.Fatal(err)
	}
	st, ok, err := store.State(ctx)
	if err != nil || !ok || st.P != m.State.P || len(st.Minute) != 2 || len(st.RNG) == 0 || !st.At.Equal(m.State.At) {
		t.Fatalf("state %+v %v %v", st, ok, err)
	}
	next := domain.NewModel(domain.DefaultParams(), st, 1)
	want, _ := m.Step(at.Add(2*time.Second), 60100, 3001, domain.Shape{})
	if got, _ := next.Step(at.Add(2*time.Second), 60100, 3001, domain.Shape{}); got != want {
		t.Fatalf("restored: %v, want %v", got, want)
	}

	// Events: created, open, started and ended, recent.
	e := domain.Event{
		ID: uuid.NewString(), Type: domain.EventTarget, Price: decimal.RequireFromString("1.2345"), Duration: time.Minute,
		Hold: 30 * time.Second, StartsAt: at, Status: domain.EventScheduled, CreatedBy: "ops", ApprovedBy: "ops2", Reason: "a test",
		CreatedAt: at,
	}
	if err := store.SaveEvent(ctx, e, nil); err != nil {
		t.Fatal(err)
	}
	open, err := store.Events(ctx, true, 0)
	if err != nil || len(open) != 1 || !open[0].Price.Equal(e.Price) || open[0].Hold != e.Hold || open[0].ApprovedBy != "ops2" ||
		!open[0].StartedAt.IsZero() {
		t.Fatalf("open %+v %v", open, err)
	}
	e.Status, e.StartedAt, e.FromLogE, e.FromP = domain.EventRunning, at.Add(time.Second), 0.05, decimal.RequireFromString("0.99876543")
	if err := store.SaveEvent(ctx, e, nil); err != nil {
		t.Fatal(err)
	}
	e.Status, e.EndedAt, e.EndedBy = domain.EventDone, at.Add(time.Minute), "ops"
	if err := store.SaveEvent(ctx, e, nil); err != nil {
		t.Fatal(err)
	}
	if open, err := store.Events(ctx, true, 0); err != nil || len(open) != 0 {
		t.Fatalf("open after it ended: %+v %v", open, err)
	}
	all, err := store.Events(ctx, false, 10)
	if err != nil || len(all) != 1 || all[0].Status != domain.EventDone || all[0].FromLogE != 0.05 || all[0].FromP.String() != "0.99876543" ||
		all[0].EndedBy != "ops" || !all[0].EndedAt.Equal(e.EndedAt) {
		t.Fatalf("latest %+v %v", all, err)
	}
	if starting, err := store.EventsStarting(ctx, at.Add(-time.Hour), at.Add(time.Hour)); err != nil || len(starting) != 1 {
		t.Fatalf("starting within the hour %+v %v", starting, err)
	}
	if starting, err := store.EventsStarting(ctx, at.Add(time.Minute), at.Add(time.Hour)); err != nil || len(starting) != 0 {
		t.Fatalf("starting later %+v %v", starting, err)
	}
	canceled := e
	canceled.ID, canceled.Status, canceled.StartsAt = uuid.NewString(), domain.EventCanceled, at.Add(10*time.Minute)
	if err := store.SaveEvent(ctx, canceled, nil); err != nil {
		t.Fatal(err)
	}
	if starting, err := store.EventsStarting(ctx, at, at.Add(time.Hour)); err != nil || len(starting) != 1 {
		t.Fatalf("a canceled one counts: %+v %v", starting, err)
	}

	// A threshold target and its spike (A6), saved together: the target's
	// direction, then, crossing and result, the spike's width and parent.
	target := domain.Event{
		ID: uuid.NewString(), Type: domain.EventTarget, Direction: domain.Below, Then: domain.ThenFollow, Price: decimal.RequireFromString("0.95"),
		Duration: 10 * time.Minute, StartsAt: at.Add(2 * time.Hour), Status: domain.EventScheduled, CreatedBy: "ops", Reason: "a test", CreatedAt: at,
	}
	spike := domain.Event{
		ID: uuid.NewString(), Type: domain.EventSpike, Size: -0.04, Width: 20 * time.Second, ParentID: target.ID,
		StartsAt: at.Add(2*time.Hour + 3*time.Minute), Status: domain.EventScheduled, CreatedBy: "ops", Reason: "a test", CreatedAt: at,
	}
	if err := store.SaveEvents(ctx, []domain.Event{target, spike}, nil); err != nil {
		t.Fatal(err)
	}
	target.Status, target.StartedAt, target.CrossedAt, target.Result = domain.EventRunning, at.Add(2*time.Hour), at.Add(2*time.Hour+9*time.Minute), domain.ResultHit
	if err := store.SaveEvent(ctx, target, nil); err != nil {
		t.Fatal(err)
	}
	open, err = store.Events(ctx, true, 0)
	if err != nil || len(open) != 2 {
		t.Fatalf("open %+v %v", open, err)
	}
	for _, x := range open {
		switch x.ID {
		case target.ID:
			if x.Direction != domain.Below || x.Then != domain.ThenFollow || x.Result != domain.ResultHit || !x.CrossedAt.Equal(target.CrossedAt) ||
				x.ParentID != "" || x.Width != 0 {
				t.Fatalf("the target %+v", x)
			}
		case spike.ID:
			if x.Type != domain.EventSpike || x.ParentID != target.ID || x.Width != 20*time.Second || x.Size != -0.04 {
				t.Fatalf("the spike %+v", x)
			}
		default:
			t.Fatalf("another event %+v", x)
		}
	}

	// Price events on followed pairs (design 2026-10-07): their pair,
	// factor, ramps and course; one open a pair, saved with an audit
	// record each.
	overlay := domain.Event{
		ID: uuid.NewString(), Type: domain.EventOverlay, Symbol: "BTC-USDT", TargetFactor: 1.16, RampUp: 15 * time.Second,
		RampDown: 5 * time.Second, Risk: true, Price: decimal.RequireFromString("116000"), StartsAt: at.Add(3 * time.Hour),
		Status: domain.EventRunning, StartedAt: at.Add(3 * time.Hour), BasePrice: decimal.RequireFromString("100000"), CreatedBy: "ops",
		Reason: "a spike", CreatedAt: at,
	}
	other := overlay
	other.ID, other.Symbol = uuid.NewString(), "ETH-USDT"
	audits := []ports.Audit{{Action: "a", Target: "t"}, {Action: "a", Target: "t"}}
	if err := store.SaveEventsEach(ctx, []domain.Event{overlay, other}, audits); err != nil {
		t.Fatal(err)
	}
	again := overlay
	again.ID = uuid.NewString()
	if err := store.SaveEventsEach(ctx, []domain.Event{again}, audits[:1]); !errors.Is(err, ports.ErrOverlayOpen) {
		t.Fatalf("a second open overlay on the pair: %v", err)
	}
	overlay.Status, overlay.EndedAt, overlay.Result = domain.EventDone, at.Add(3*time.Hour+20*time.Second), ""
	overlay.PeakPrice, overlay.EndReferencePrice = decimal.RequireFromString("116010.5"), decimal.RequireFromString("100020")
	overlay.EndPlatformPrice = decimal.RequireFromString("100020")
	if err := store.SaveEvent(ctx, overlay, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEvent(ctx, again, nil); err != nil {
		t.Fatalf("the pair is free once it is done: %v", err)
	}
	all, err = store.Events(ctx, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range all {
		if x.ID != overlay.ID {
			continue
		}
		if x.Symbol != "BTC-USDT" || x.TargetFactor != 1.16 || x.RampUp != 15*time.Second || x.RampDown != 5*time.Second || !x.Risk ||
			x.BasePrice.String() != "100000" || x.PeakPrice.String() != "116010.5" || x.EndReferencePrice.String() != "100020" ||
			x.EndPlatformPrice.String() != "100020" || !x.Price.Equal(overlay.Price) {
			t.Fatalf("the overlay %+v", x)
		}
		return
	}
	t.Fatalf("the overlay not listed: %+v", all)
}
