package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// fakeTrading is the platform as the bots see it: limit orders rest until
// canceled, market orders are counted, balances are fixed.
type fakeTrading struct {
	mu       sync.Mutex
	pair     domain.Pair
	seq      int
	open     map[string][]domain.Order // by user
	markets  map[domain.Side]int
	cancels  int
	cancelAl map[string]int
	balances map[string]map[string]decimal.Decimal
}

func newFakeTrading() *fakeTrading {
	return &fakeTrading{
		pair: domain.Pair{Symbol: "ASTRA-USDT", Tick: d("0.0001"), Lot: d("1"), MinQty: d("1"), MinNotional: d("5"), Trading: true},
		open: map[string][]domain.Order{}, markets: map[domain.Side]int{}, cancelAl: map[string]int{},
		balances: map[string]map[string]decimal.Decimal{},
	}
}

func (f *fakeTrading) Pair(context.Context, string) (domain.Pair, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pair, nil
}

func (f *fakeTrading) Open(_ context.Context, user, _ string) ([]domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Order(nil), f.open[user]...), nil
}

func (f *fakeTrading) Limit(_ context.Context, user, _ string, side domain.Side, price, qty decimal.Decimal) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !qty.IsPositive() {
		return "", fmt.Errorf("quantity %s", qty)
	}
	f.seq++
	id := fmt.Sprintf("o%d", f.seq)
	f.open[user] = append(f.open[user], domain.Order{ID: id, Side: side, Price: price})
	return id, nil
}

func (f *fakeTrading) Market(_ context.Context, _, _ string, side domain.Side, quote, qty decimal.Decimal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if (side == domain.Buy && !quote.IsPositive()) || (side == domain.Sell && !qty.IsPositive()) {
		return fmt.Errorf("market %s: quote %s qty %s", side, quote, qty)
	}
	f.markets[side]++
	return nil
}

func (f *fakeTrading) Cancel(_ context.Context, user, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, o := range f.open[user] {
		if o.ID == id {
			f.open[user] = append(f.open[user][:i], f.open[user][i+1:]...)
			f.cancels++
			return nil
		}
	}
	return nil
}

func (f *fakeTrading) CancelAll(_ context.Context, user, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelAl[user]++
	delete(f.open, user)
	return nil
}

func (f *fakeTrading) Balances(_ context.Context, user string) (map[string]decimal.Decimal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.balances[user]; ok {
		return b, nil
	}
	return map[string]decimal.Decimal{"USDT": d("100000"), "ASTRA": d("40000000")}, nil
}

func (f *fakeTrading) orders(user string) []domain.Order {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Order(nil), f.open[user]...)
}

type fakePrices struct {
	mu             sync.Mutex
	btc, eth, last decimal.Decimal
}

func (p *fakePrices) Reference(_ context.Context, symbol string) (decimal.Decimal, bool, error) {
	if symbol == btcPair {
		return p.btc, true, nil
	}
	return p.eth, true, nil
}

func (p *fakePrices) Last(context.Context, string) (decimal.Decimal, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last, nil
}

type memStore struct {
	bots    []ports.Bot
	params  *domain.Params
	version int64
	state   *domain.State
	saves   int
	byWhom  string
	events  []domain.Event
	audits  []ports.Audit
	mu      sync.Mutex
}

func (m *memStore) Bots(context.Context) ([]ports.Bot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ports.Bot(nil), m.bots...), nil
}

func (m *memStore) AddBot(_ context.Context, b ports.Bot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bots = append(m.bots, b)
	return nil
}

func (m *memStore) Settings(context.Context) (domain.Params, int64, bool, error) {
	if m.params == nil {
		return domain.Params{}, 0, false, nil
	}
	return *m.params, m.version, true, nil
}

func (m *memStore) SaveSettings(_ context.Context, p domain.Params, actor string, audit *ports.Audit) (int64, error) {
	m.params, m.byWhom = &p, actor
	m.version++
	if audit != nil {
		m.audits = append(m.audits, *audit)
	}
	return m.version, nil
}

func (m *memStore) Events(_ context.Context, open bool, limit int) ([]domain.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Event
	for _, e := range m.events {
		if !open || e.Status == domain.EventScheduled || e.Status == domain.EventRunning {
			out = append(out, e)
		}
	}
	if !open && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (m *memStore) EventsSince(_ context.Context, t time.Time) ([]domain.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Event
	for _, e := range m.events {
		if !e.CreatedAt.Before(t) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memStore) SaveEvent(_ context.Context, e domain.Event, audit *ports.Audit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i := slices.IndexFunc(m.events, func(x domain.Event) bool { return x.ID == e.ID }); i >= 0 {
		m.events[i] = e
	} else {
		m.events = append(m.events, e)
	}
	if audit != nil {
		m.audits = append(m.audits, *audit)
	}
	return nil
}

func (m *memStore) event(id string) domain.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.ID == id {
			return e
		}
	}
	return domain.Event{}
}

// fakePairs records the pair's status changes.
type fakePairs struct {
	mu      sync.Mutex
	trading *fakeTrading
	changes []string
}

func (f *fakePairs) SetPairStatus(_ context.Context, _, to, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changes = append(f.changes, to)
	f.trading.mu.Lock()
	f.trading.pair.Trading = to == "TRADING"
	f.trading.mu.Unlock()
	return nil
}

func (m *memStore) State(context.Context) (domain.State, bool, error) {
	if m.state == nil {
		return domain.State{}, false, nil
	}
	return *m.state, true, nil
}

func (m *memStore) SaveState(_ context.Context, st domain.State) error {
	m.state = &st
	m.saves++
	return nil
}

type flagSet struct{ on, events bool }

func (f *flagSet) Enabled(key string, _ flags.Subject) bool {
	switch key {
	case flags.KeySimEnabled:
		return f.on
	case flags.KeySimEvents:
		return f.events
	}
	return false
}

type rig struct {
	sim     *Sim
	trading *fakeTrading
	pairs   *fakePairs
	store   *memStore
	flags   *flagSet
	prices  *fakePrices
	now     time.Time
}

func newRig(t *testing.T, store *memStore) *rig {
	t.Helper()
	if store == nil {
		store = &memStore{}
	}
	if len(store.bots) == 0 {
		store.bots = []ports.Bot{
			{UserID: "m1", Role: domain.RoleMaker, Label: "bot-01", Enabled: true},
			{UserID: "m2", Role: domain.RoleMaker, Label: "bot-02", Enabled: true},
			{UserID: "t1", Role: domain.RoleTaker, Label: "bot-03", Enabled: true},
			{UserID: "r1", Role: domain.RoleTrend, Label: "bot-04", Enabled: true},
		}
	}
	r := &rig{trading: newFakeTrading(), store: store, flags: &flagSet{on: true, events: true}, now: time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)}
	r.pairs = &fakePairs{trading: r.trading}
	r.prices = &fakePrices{btc: d("60000"), eth: d("3000"), last: d("1.0001")}
	r.sim = New(Config{Symbol: "ASTRA-USDT", Quote: "USDT", Tick: 250 * time.Millisecond, Seed: 11}, r.trading,
		r.prices, r.pairs, store, r.flags, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	r.sim.now = func() time.Time { return r.now }
	if err := r.sim.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) rounds(n int) {
	for range n {
		r.now = r.now.Add(250 * time.Millisecond)
		r.sim.Round(context.Background())
	}
}

func TestMakersQuoteLaddersAroundTheTarget(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(40) // ten seconds: both makers have quoted, within the throttle
	for _, m := range []string{"m1", "m2"} {
		var bids, asks int
		for _, o := range r.trading.orders(m) {
			if o.Side == domain.Buy {
				bids++
				if o.Price.GreaterThanOrEqual(d("1")) {
					t.Fatalf("%s bids at %s", m, o.Price)
				}
			} else {
				asks++
				if o.Price.LessThanOrEqual(d("1")) {
					t.Fatalf("%s asks at %s", m, o.Price)
				}
			}
		}
		if bids != 8 || asks != 8 {
			t.Fatalf("%s: %d bids, %d asks", m, bids, asks)
		}
	}
	// The two makers stand on different phases of the grid.
	if a, b := r.trading.orders("m1")[0].Price, r.trading.orders("m2")[0].Price; a.Equal(b) {
		t.Fatalf("both makers quote %s", a)
	}
	// The defaults were saved on the first start, the state is saved.
	if r.store.params == nil || r.store.byWhom != "market-sim" || r.store.saves == 0 {
		t.Fatalf("settings %+v by %q, %d saves", r.store.params, r.store.byWhom, r.store.saves)
	}
}

func TestTheThrottleHoldsOrdersBack(t *testing.T) {
	p := domain.DefaultParams()
	p.OrdersPerSecond, p.DailyVolume = 1, 0
	r := newRig(t, &memStore{params: &p, version: 1})
	r.rounds(40) // ten seconds at one order a second
	n := len(r.trading.orders("m1")) + len(r.trading.orders("m2"))
	if n < 5 || n > 11 {
		t.Fatalf("%d orders in ten seconds at one a second", n)
	}
}

func TestTakersTradeTheDaysTurnover(t *testing.T) {
	p := domain.DefaultParams()
	p.DailyVolume, p.OrderSize = 86_400*400*2, 400 // two orders a second
	r := newRig(t, &memStore{params: &p, version: 1})
	r.rounds(4 * 60)
	got := r.trading.markets[domain.Buy] + r.trading.markets[domain.Sell]
	if got < 120 || got > 360 {
		t.Fatalf("%d market orders in a minute, want about 120 to 360 (two a second, by the hour's weight)", got)
	}
	if r.trading.markets[domain.Buy] == 0 || r.trading.markets[domain.Sell] == 0 {
		t.Fatalf("one-sided: %v", r.trading.markets)
	}
}

func TestSwitchingOffCancelsTheMakersOrders(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(20)
	if len(r.trading.orders("m1")) == 0 {
		t.Fatal("no quotes")
	}
	r.flags.on = false
	r.rounds(1)
	if r.trading.cancelAl["m1"] != 1 || r.trading.cancelAl["m2"] != 1 || len(r.trading.orders("m1")) != 0 {
		t.Fatalf("cancel all %v, left %v", r.trading.cancelAl, r.trading.orders("m1"))
	}
	if st := r.sim.Status(); st.Running || st.Enabled {
		t.Fatalf("status %+v", st)
	}
	r.rounds(4)
	if r.trading.cancelAl["m1"] != 1 {
		t.Fatal("canceled again while off")
	}
	// A pair that stops trading does the same.
	r.flags.on = true
	r.rounds(20)
	r.trading.mu.Lock()
	r.trading.pair.Trading = false
	r.trading.mu.Unlock()
	r.now = r.now.Add(pairEvery)
	r.rounds(1)
	if r.trading.cancelAl["m1"] != 2 {
		t.Fatalf("the pair halted: cancel all %v", r.trading.cancelAl)
	}
}

func TestARestartGoesOnFromTheSavedState(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(40)
	// Run saves the state when it stops.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.sim.Run(ctx); err != nil {
		t.Fatal(err)
	}
	target := r.sim.Status().Target
	again := newRig(t, r.store)
	if got := again.sim.model.State.P; got != target {
		t.Fatalf("restarted at %v, was %v", got, target)
	}
	// The makers adopt their resting orders instead of quoting anew.
	again.trading = r.trading
	again.sim.trading = r.trading
	before := len(r.trading.orders("m1"))
	again.now = r.now
	again.rounds(8)
	if after := len(r.trading.orders("m1")); after != before {
		t.Fatalf("%d orders after the restart, were %d", after, before)
	}
}

func TestUpdateParams(t *testing.T) {
	r := newRig(t, nil)
	p := domain.DefaultParams()
	p.Spread = 0
	if _, err := r.sim.UpdateParams(context.Background(), p, "ops"); err == nil {
		t.Fatal("a spread of 0 passed")
	}
	p = domain.DefaultParams()
	p.Levels = 3
	v, err := r.sim.UpdateParams(context.Background(), p, "ops")
	if err != nil || v != 2 || r.store.byWhom != "ops" {
		t.Fatalf("version %d, %v, by %q", v, err, r.store.byWhom)
	}
	r.rounds(40)
	if n := len(r.trading.orders("m1")); n != 6 {
		t.Fatalf("%d orders with 3 levels a side", n)
	}
	if st := r.sim.Status(); st.Version != 2 || st.Params.Levels != 3 || len(st.Bots) != 4 {
		t.Fatalf("status %+v", st)
	}
}

func TestAddBot(t *testing.T) {
	r := newRig(t, nil)
	if err := r.sim.AddBot(context.Background(), ports.Bot{UserID: "x", Role: "BOSS", Label: "bot-09"}); err == nil {
		t.Fatal("an unknown role passed")
	}
	if err := r.sim.AddBot(context.Background(), ports.Bot{UserID: "m3", Role: domain.RoleMaker, Label: "bot-05"}); err != nil {
		t.Fatal(err)
	}
	// The operators see it at once, before it trades.
	if st := r.sim.Status(); len(st.Bots) != 5 {
		t.Fatalf("%d bots", len(st.Bots))
	}
	r.rounds(40)
	if len(r.trading.orders("m3")) == 0 {
		t.Fatal("the new maker does not quote")
	}
}

// Bots stored while the simulation is off still show.
func TestBotsShowWhileOff(t *testing.T) {
	r := newRig(t, nil)
	r.flags.on = false
	r.store.bots = append(r.store.bots, ports.Bot{UserID: "t9", Role: domain.RoleTaker, Label: "bot-09", Enabled: true})
	r.now = r.now.Add(botsEvery)
	r.rounds(1)
	if st := r.sim.Status(); len(st.Bots) != 5 || st.Running {
		t.Fatalf("status %+v", st)
	}
}

func (r *rig) create(t *testing.T, e domain.Event) domain.Event {
	t.Helper()
	if e.CreatedBy == "" {
		e.CreatedBy, e.Reason = "ops", "e2e of an event"
	}
	out, err := r.sim.CreateEvent(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAJumpMovesTheTargetAndStays(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(8)
	before := r.sim.Status().Target
	e := r.create(t, domain.Event{Type: domain.EventJump, Size: 0.1})
	r.rounds(1)
	after := r.sim.Status().Target
	if after < before*1.09 || after > before*1.11 {
		t.Fatalf("jumped from %v to %v", before, after)
	}
	if got := r.store.event(e.ID); got.Status != domain.EventDone || got.StartedAt.IsZero() || !got.FromP.IsPositive() {
		t.Fatalf("event %+v", got)
	}
	r.rounds(4 * 70) // more than a minute: the guard does not pull it back
	if p := r.sim.Status().Target; p < before*1.07 {
		t.Fatalf("a minute later %v (was %v before the jump)", p, before)
	}
	if len(r.store.audits) == 0 || r.store.audits[len(r.store.audits)-1].Action != "market.sim.event_created" {
		t.Fatalf("audits %+v", r.store.audits)
	}
}

func TestEventsNeedTheSwitchAndBeyondLimitsASecondOperator(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	r.flags.events = false
	if _, err := r.sim.CreateEvent(context.Background(), domain.Event{Type: domain.EventJump, Size: 0.1, CreatedBy: "ops", Reason: "test"}); !errors.Is(err, ErrEventsOff) {
		t.Fatalf("switched off: %v", err)
	}
	r.flags.events = true
	big := domain.Event{Type: domain.EventJump, Size: -0.35, CreatedBy: "ops", Reason: "test"}
	if _, err := r.sim.CreateEvent(context.Background(), big); err == nil || !apperrIs(err, "SIM_EVENT_NEEDS_APPROVAL") {
		t.Fatalf("alone: %v", err)
	}
	big.ApprovedBy = "ops"
	if _, err := r.sim.CreateEvent(context.Background(), big); !apperrIs(err, "SIM_EVENT_NEEDS_APPROVAL") {
		t.Fatalf("approved by the same operator: %v", err)
	}
	big.ApprovedBy = "ops2"
	if _, err := r.sim.CreateEvent(context.Background(), big); err != nil {
		t.Fatalf("approved: %v", err)
	}
}

func TestATargetIsReachedAndHeld(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	e := r.create(t, domain.Event{Type: domain.EventTarget, Price: d("1.2"), Duration: 10 * time.Second, Hold: 5 * time.Second})
	r.rounds(4 * 11)
	if p := r.sim.Status().Target; p != 1.2 {
		t.Fatalf("arrived at %v", p)
	}
	if got := r.store.event(e.ID); got.Status != domain.EventRunning {
		t.Fatalf("holding: %s", got.Status)
	}
	r.rounds(4 * 5)
	if got := r.store.event(e.ID); got.Status != domain.EventDone {
		t.Fatalf("after the hold: %s", got.Status)
	}
	if p := r.sim.Status().Target; p < 1.19 || p > 1.21 {
		t.Fatalf("goes on from 1.2: %v", p)
	}
}

func TestAPauseStopsTheTakers(t *testing.T) {
	p := domain.DefaultParams()
	p.DailyVolume = 86_400 * 400 * 4 // four orders a second
	r := newRig(t, &memStore{params: &p, version: 1})
	r.rounds(4)
	e := r.create(t, domain.Event{Type: domain.EventPause})
	r.rounds(1)
	r.trading.mu.Lock()
	before := r.trading.markets[domain.Buy] + r.trading.markets[domain.Sell]
	r.trading.mu.Unlock()
	held := r.sim.Status().Target
	r.rounds(40)
	r.trading.mu.Lock()
	during := r.trading.markets[domain.Buy] + r.trading.markets[domain.Sell]
	r.trading.mu.Unlock()
	if during != before || r.sim.Status().Target != held {
		t.Fatalf("paused: %d market orders more, target %v -> %v", during-before, held, r.sim.Status().Target)
	}
	if _, err := r.sim.EndEvent(context.Background(), e.ID, "ops", "resume"); err != nil {
		t.Fatal(err)
	}
	r.rounds(40)
	r.trading.mu.Lock()
	after := r.trading.markets[domain.Buy] + r.trading.markets[domain.Sell]
	r.trading.mu.Unlock()
	if after == during {
		t.Fatal("the takers did not come back")
	}
}

func TestAHaltCancelsTheBotsAndTheResumeRestoresThem(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(20)
	e := r.create(t, domain.Event{Type: domain.EventHalt})
	r.rounds(2)
	if r.trading.cancelAl["m1"] == 0 || len(r.trading.orders("m1")) != 0 || !slices.Equal(r.pairs.changes, []string{"HALT"}) {
		t.Fatalf("halt: cancel all %v, pair %v", r.trading.cancelAl, r.pairs.changes)
	}
	r.rounds(8)
	if len(r.trading.orders("m1")) != 0 {
		t.Fatal("quoted while halted")
	}
	if _, err := r.sim.EndEvent(context.Background(), e.ID, "ops", "resume trading"); err != nil {
		t.Fatal(err)
	}
	r.rounds(20)
	if !slices.Equal(r.pairs.changes, []string{"HALT", "TRADING"}) || len(r.trading.orders("m1")) == 0 {
		t.Fatalf("resumed: pair %v, %d orders", r.pairs.changes, len(r.trading.orders("m1")))
	}
	if _, err := r.sim.EndEvent(context.Background(), e.ID, "ops", "again"); !errors.Is(err, ErrEventNotOpen) {
		t.Fatalf("ended twice: %v", err)
	}
}

func TestExecutorsPushThePrintedPriceAfterAJump(t *testing.T) {
	store := &memStore{bots: []ports.Bot{
		{UserID: "m1", Role: domain.RoleMaker, Label: "bot-01", Enabled: true},
		{UserID: "x1", Role: domain.RoleExecutor, Label: "bot-02", Enabled: true},
	}}
	p := domain.DefaultParams()
	p.DailyVolume = 0
	store.params = &p
	store.version = 1
	r := newRig(t, store)
	r.rounds(4)
	r.create(t, domain.Event{Type: domain.EventJump, Size: 0.1})
	r.rounds(4 * 10) // the last price stays at 1.0001: they keep buying
	r.trading.mu.Lock()
	buys, sells := r.trading.markets[domain.Buy], r.trading.markets[domain.Sell]
	r.trading.mu.Unlock()
	if buys < 4 || sells != 0 {
		t.Fatalf("executors: %d buys, %d sells", buys, sells)
	}
}

func TestAScheduledEventIsCanceled(t *testing.T) {
	r := newRig(t, nil)
	e := r.create(t, domain.Event{Type: domain.EventJump, Size: 0.05, StartsAt: r.now.Add(time.Hour)})
	got, err := r.sim.EndEvent(context.Background(), e.ID, "ops", "not today")
	if err != nil || got.Status != domain.EventCanceled {
		t.Fatalf("%+v %v", got, err)
	}
	r.rounds(4)
	if st := r.sim.Status(); len(st.Events) != 0 {
		t.Fatalf("open events %+v", st.Events)
	}
}

func apperrIs(err error, code string) bool { return err != nil && apperr.Is(err, code) }
