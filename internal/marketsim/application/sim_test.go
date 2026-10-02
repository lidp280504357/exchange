package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
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
// canceled (refused beyond the pair's band around the last trade, or with
// a user's refusal), market orders are counted and, with prices, trade at
// the best price on the other side, which becomes the last trade.
type fakeTrading struct {
	mu        sync.Mutex
	pair      domain.Pair
	seq       int
	open      map[string][]domain.Order // by user
	markets   map[domain.Side]int
	cancels   int
	cancelAl  map[string]int
	balances  map[string]map[string]decimal.Decimal
	prices    *fakePrices
	anchor    decimal.Decimal  // the band's anchor when set, else the last trade
	refuse    map[string]error // by user
	tries     map[string]int   // limit orders asked for, by user
	outOfBand int
	quantity  map[string]map[domain.Side]decimal.Decimal // placed, by user and side
}

func newFakeTrading() *fakeTrading {
	return &fakeTrading{
		pair: domain.Pair{Symbol: "ASTRA-USDT", Tick: d("0.0001"), Lot: d("1"), MinQty: d("1"), MinNotional: d("5"), Status: "TRADING", Trading: true},
		open: map[string][]domain.Order{}, markets: map[domain.Side]int{}, cancelAl: map[string]int{},
		balances: map[string]map[string]decimal.Decimal{}, refuse: map[string]error{}, tries: map[string]int{},
		quantity: map[string]map[domain.Side]decimal.Decimal{},
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
	f.tries[user]++
	if !qty.IsPositive() {
		return "", fmt.Errorf("quantity %s", qty)
	}
	if err := f.refuse[user]; err != nil {
		return "", err
	}
	anchor := f.anchor
	if anchor.IsZero() && f.prices != nil {
		anchor = f.prices.lastPrice()
	}
	if band := decimal.NewFromFloat(f.pair.Band); band.IsPositive() && anchor.IsPositive() && price.Sub(anchor).Abs().GreaterThan(anchor.Mul(band)) {
		f.outOfBand++
		return "", ports.ErrOutOfBand
	}
	f.seq++
	id := fmt.Sprintf("o%d", f.seq)
	f.open[user] = append(f.open[user], domain.Order{ID: id, Side: side, Price: price})
	if f.quantity[user] == nil {
		f.quantity[user] = map[domain.Side]decimal.Decimal{}
	}
	f.quantity[user][side] = f.quantity[user][side].Add(qty)
	return id, nil
}

func (f *fakeTrading) Market(_ context.Context, user, _ string, side domain.Side, quote, qty decimal.Decimal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if (side == domain.Buy && !quote.IsPositive()) || (side == domain.Sell && !qty.IsPositive()) {
		return fmt.Errorf("market %s: quote %s qty %s", side, quote, qty)
	}
	if err := f.refuse[user]; err != nil {
		return err
	}
	f.markets[side]++
	if f.prices == nil {
		return nil
	}
	// It trades at the best price on the other side, which becomes the
	// last trade.
	var best decimal.Decimal
	for _, orders := range f.open {
		for _, o := range orders {
			if o.Side == side {
				continue
			}
			if best.IsZero() || (side == domain.Buy && o.Price.LessThan(best)) || (side == domain.Sell && o.Price.GreaterThan(best)) {
				best = o.Price
			}
		}
	}
	if best.IsPositive() {
		f.prices.trade(best)
	}
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

// fakePrices are the reference prices and the pair's market data: its
// last trade (at lastAt; zero: just now), its own reference ref, the
// prices reported, the perpetual's mark.
type fakePrices struct {
	mu             sync.Mutex
	btc, eth, last decimal.Decimal
	lastAt         time.Time
	frozen         bool // market orders do not trade
	ref, mark      decimal.Decimal
	index          decimal.Decimal
	reported       []decimal.Decimal
	now            func() time.Time
	lastErr        error // the last trade cannot be read
}

func (p *fakePrices) Reference(_ context.Context, symbol string) (decimal.Decimal, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch symbol {
	case btcPair:
		return p.btc, true, nil
	case ethPair:
		return p.eth, true, nil
	}
	return p.ref, p.ref.IsPositive(), nil
}

func (p *fakePrices) LastTrade(context.Context, string) (decimal.Decimal, time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastErr != nil {
		return decimal.Zero, time.Time{}, p.lastErr
	}
	at := p.lastAt
	if at.IsZero() {
		at = p.now()
	}
	return p.last, at, nil
}

func (p *fakePrices) Report(_ context.Context, _ string, price decimal.Decimal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reported = append(p.reported, price)
	return nil
}

func (p *fakePrices) Mark(context.Context, string) (decimal.Decimal, decimal.Decimal, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mark, p.index, nil
}

func (p *fakePrices) lastPrice() decimal.Decimal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

func (p *fakePrices) trade(price decimal.Decimal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.frozen {
		p.last = price
	}
}

type memStore struct {
	bots    []ports.Bot
	params  *domain.Params
	version int64
	state   *domain.State
	saves   int
	byWhom  string
	changes []ports.ParamChange
	samples []ports.Sample
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

func (m *memStore) SaveSettings(_ context.Context, p domain.Params, change ports.ParamChange, audit *ports.Audit) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.params, m.byWhom = &p, change.Actor
	m.version++
	m.changes = append(m.changes, change)
	if change.State != nil {
		st := *change.State
		m.state = &st
	}
	if audit != nil {
		m.audits = append(m.audits, *audit)
	}
	return m.version, nil
}

func (m *memStore) ParamChanges(_ context.Context, from, to time.Time) ([]ports.ParamChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ports.ParamChange
	for _, c := range m.changes {
		if !c.At.Before(from) && !c.At.After(to) {
			out = append(out, c)
		}
	}
	return out, nil
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

func (m *memStore) EventsStarting(_ context.Context, from, to time.Time) ([]domain.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Event
	for _, e := range m.events {
		if e.Status != domain.EventCanceled && !e.StartsAt.Before(from) && !e.StartsAt.After(to) {
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

// fakePairs records the pair's and the contract's status changes; the
// contract's next change fails with contractErr.
type fakePairs struct {
	mu          sync.Mutex
	trading     *fakeTrading
	changes     []string
	contracts   []string
	perp        *fakeDerivatives
	contractErr error
}

func (f *fakePairs) SetContractStatus(_ context.Context, _, to, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.contractErr; err != nil {
		f.contractErr = nil
		return err
	}
	f.contracts = append(f.contracts, to)
	if f.perp != nil {
		f.perp.mu.Lock()
		f.perp.contract.Status, f.perp.contract.Trading = to, to == "TRADING"
		f.perp.mu.Unlock()
	}
	return nil
}

func (f *fakePairs) SetPairStatus(_ context.Context, _, to, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trading.mu.Lock()
	defer f.trading.mu.Unlock()
	if f.trading.pair.Status == to {
		return fmt.Errorf("pair already %s", to) // as instrument-service refuses it
	}
	f.changes = append(f.changes, to)
	f.trading.pair.Status, f.trading.pair.Trading = to, to == "TRADING"
	return nil
}

func (m *memStore) SaveSample(_ context.Context, x ports.Sample) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, x)
	return nil
}

func (m *memStore) Samples(_ context.Context, t time.Time) ([]ports.Sample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ports.Sample
	for _, x := range m.samples {
		if !x.At.Before(t) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (m *memStore) PruneSamples(_ context.Context, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = slices.DeleteFunc(m.samples, func(x ports.Sample) bool { return x.At.Before(t) })
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

type flagSet struct{ on, events, perp bool }

func (f *flagSet) Enabled(key string, _ flags.Subject) bool {
	switch key {
	case flags.KeySimEnabled:
		return f.on
	case flags.KeySimEvents:
		return f.events
	case flags.KeySimPerp:
		return f.perp
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
	r.prices = &fakePrices{btc: d("60000"), eth: d("3000"), last: d("1.0001"), now: func() time.Time { return r.now }}
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
	// 69,120,000 a day, 80% of it the takers' in orders of a mean 551: 1.16
	// a second, 1.5 at 14:00 UTC (Europe and America: the hour's weight
	// 1.3); the trend follower adds one now and then.
	p.DailyVolume, p.OrderSize = 86_400*400*2, 400
	r := newRig(t, &memStore{params: &p, version: 1})
	r.rounds(4 * 60)
	got := r.trading.markets[domain.Buy] + r.trading.markets[domain.Sell]
	if got < 60 || got > 130 {
		t.Fatalf("%d market orders in a minute, want about 90", got)
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
	if _, err := r.sim.UpdateParams(context.Background(), p, "ops", ""); err == nil {
		t.Fatal("a spread of 0 passed")
	}
	p = domain.DefaultParams()
	p.Levels = 3
	v, err := r.sim.UpdateParams(context.Background(), p, "ops", "")
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

// fakeDerivatives is the perpetual as the bots see it: limit orders rest
// until canceled, market orders move the position, transfers fund margin.
type fakeDerivatives struct {
	mu        sync.Mutex
	contract  domain.Pair
	seq       int
	open      map[string][]domain.Order
	positions map[string]decimal.Decimal
	futures   map[string]decimal.Decimal
	markets   []string // "user side qty reduce"
	transfers map[string]bool
	cancelAll map[string]int
}

func newFakeDerivatives() *fakeDerivatives {
	return &fakeDerivatives{
		contract: domain.Pair{Symbol: "ASTRA-USDT-PERP", Tick: d("0.0001"), Lot: d("1"), MinQty: d("1"), MinNotional: d("5"), Status: "TRADING", Trading: true},
		open:     map[string][]domain.Order{}, positions: map[string]decimal.Decimal{}, futures: map[string]decimal.Decimal{},
		transfers: map[string]bool{}, cancelAll: map[string]int{},
	}
}

func (f *fakeDerivatives) Contract(context.Context, string) (domain.Pair, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.contract, nil
}

func (f *fakeDerivatives) OpenContract(_ context.Context, user, _ string) ([]domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.open[user]), nil
}

func (f *fakeDerivatives) LimitContract(_ context.Context, user, _ string, side domain.Side, price, _ decimal.Decimal) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("k%d", f.seq)
	f.open[user] = append(f.open[user], domain.Order{ID: id, Side: side, Price: price})
	return id, nil
}

func (f *fakeDerivatives) MarketContract(_ context.Context, user, _ string, side domain.Side, qty decimal.Decimal, reduce bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if side == domain.Buy {
		f.positions[user] = f.positions[user].Add(qty)
	} else {
		f.positions[user] = f.positions[user].Sub(qty)
	}
	f.markets = append(f.markets, fmt.Sprintf("%s %s %s %v", user, side, qty, reduce))
	return nil
}

func (f *fakeDerivatives) CancelContract(_ context.Context, user, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.open[user] = slices.DeleteFunc(f.open[user], func(o domain.Order) bool { return o.ID == id })
	return nil
}

func (f *fakeDerivatives) CancelAllContract(_ context.Context, user, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelAll[user]++
	delete(f.open, user)
	return nil
}

func (f *fakeDerivatives) Position(_ context.Context, user, _ string) (decimal.Decimal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.positions[user], nil
}

func (f *fakeDerivatives) Futures(_ context.Context, user string) (decimal.Decimal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.futures[user], nil
}

func (f *fakeDerivatives) ToFutures(_ context.Context, user string, amount decimal.Decimal, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.transfers[key] {
		f.transfers[key] = true
		f.futures[user] = f.futures[user].Add(amount)
	}
	return nil
}

func (f *fakeDerivatives) orders(user string) []domain.Order {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.open[user])
}

func perpRig(t *testing.T, p domain.Params) (*rig, *fakeDerivatives) {
	t.Helper()
	r := newRig(t, &memStore{params: &p, version: 1})
	fd := newFakeDerivatives()
	r.sim.cfg.Perp = "ASTRA-USDT-PERP"
	r.sim.Derivatives = fd
	r.pairs.perp = fd
	r.flags.perp = true
	return r, fd
}

func TestTheBotsMakeThePerpetual(t *testing.T) {
	p := domain.DefaultParams()
	p.DailyVolume, p.PerpDailyVolume = 0, 86_400*400*2 // two perpetual orders a second
	r, fd := perpRig(t, p)
	r.rounds(4 * 20)
	for _, m := range []string{"m1", "m2"} {
		bids, asks := 0, 0
		for _, o := range fd.orders(m) {
			if o.Side == domain.Buy {
				bids++
			} else {
				asks++
			}
		}
		if bids != 8 || asks != 8 {
			t.Fatalf("%s on the perpetual: %d bids, %d asks", m, bids, asks)
		}
	}
	fd.mu.Lock()
	markets, funded := len(fd.markets), fd.futures["m1"]
	fd.mu.Unlock()
	if markets < 10 || !funded.Equal(d("30000")) {
		t.Fatalf("%d market orders, m1's margin %s", markets, funded)
	}
	if st := r.sim.Status(); !st.PerpOn || st.Perp != "ASTRA-USDT-PERP" {
		t.Fatalf("status %+v", st)
	}
	// Switched off, the makers leave the perpetual (the spot pair stays).
	r.flags.perp = false
	r.rounds(1)
	if fd.cancelAll["m1"] != 1 || len(fd.orders("m1")) != 0 || len(r.trading.orders("m1")) == 0 {
		t.Fatalf("perpetual off: %v, spot %d orders", fd.cancelAll, len(r.trading.orders("m1")))
	}
}

func TestAPositionAtTheCapOnlyReduces(t *testing.T) {
	p := domain.DefaultParams()
	p.DailyVolume, p.PerpDailyVolume, p.PerpBotCap = 0, 86_400*400*2, 1000
	r, fd := perpRig(t, p)
	fd.mu.Lock()
	fd.positions["t1"] = d("5000") // long, worth about 5,000: over the cap
	fd.positions["m1"] = d("-5000")
	fd.mu.Unlock()
	r.rounds(4 * 10)
	fd.mu.Lock()
	defer fd.mu.Unlock()
	// Replayed from 5,000: while the position is worth the cap or more,
	// every order reduces it; below, the taker trades either way again.
	pos, reduced := d("5000"), 0
	for _, m := range fd.markets {
		var user, side, qty string
		var reduce bool
		if _, err := fmt.Sscanf(m, "%s %s %s %v", &user, &side, &qty, &reduce); err != nil {
			t.Fatal(err)
		}
		if user != "t1" {
			continue
		}
		if pos.GreaterThanOrEqual(d("1001")) { // the cap at a target near 1
			if side != "SELL" || !reduce {
				t.Fatalf("over the cap at %s: %s", pos, m)
			}
			reduced++
		}
		if side == "SELL" {
			pos = pos.Sub(d(qty))
		} else {
			pos = pos.Add(d(qty))
		}
	}
	if reduced == 0 {
		t.Fatalf("t1 did not reduce: %v", fd.markets)
	}
	// The maker short at the cap quotes no asks.
	for _, o := range fd.open["m1"] {
		if o.Side == domain.Sell {
			t.Fatalf("m1 short at the cap asks at %s", o.Price)
		}
	}
}

// bandRig is a rig whose pair has a band of 10% around the last trade,
// which market orders move.
func bandRig(t *testing.T, store *memStore) *rig {
	t.Helper()
	r := newRig(t, store)
	r.trading.mu.Lock()
	r.trading.pair.Band = 0.1
	r.trading.prices = r.prices
	r.trading.mu.Unlock()
	r.sim.pairAt = time.Time{} // read the band at once
	return r
}

func executorStore() *memStore {
	p := domain.DefaultParams()
	p.DailyVolume = 0
	return &memStore{params: &p, version: 1, bots: []ports.Bot{
		{UserID: "m1", Role: domain.RoleMaker, Label: "bot-01", Enabled: true},
		{UserID: "m2", Role: domain.RoleMaker, Label: "bot-02", Enabled: true},
		{UserID: "x1", Role: domain.RoleExecutor, Label: "bot-03", Enabled: true},
	}}
}

// A target 35% above the last trade is beyond the band of 10%: the makers
// quote at the band's edge, never beyond it, the executors trade there,
// the anchor follows, and the quotes walk up to the target (ASTRA design
// §4: the band must not lock the market).
func TestTheQuotesWalkTheBandToATargetBeyondIt(t *testing.T) {
	r := bandRig(t, executorStore())
	r.rounds(8)
	r.create(t, domain.Event{Type: domain.EventTarget, Price: d("1.35"), Hold: time.Hour, ApprovedBy: "ops2"})
	r.rounds(2)
	if st := r.sim.Status(); !st.Walking || st.Center > 1.0001*1.1 || st.Band != 0.1 || st.Anchor != 1.0001 {
		t.Fatalf("the quotes do not walk from the band's edge: %+v", st)
	}
	for range 4 * 60 {
		r.rounds(1)
		if last := r.prices.lastPrice(); last.GreaterThan(d("1.33")) {
			break
		}
	}
	if last := r.prices.lastPrice(); last.LessThan(d("1.33")) {
		t.Fatalf("a minute later the last trade is %s, the target 1.35", last)
	}
	r.rounds(4 * 10)
	if r.trading.outOfBand != 0 {
		t.Fatalf("%d levels refused for the band", r.trading.outOfBand)
	}
	if st := r.sim.Status(); st.Walking || st.Deadlocks != 0 {
		t.Fatalf("arrived: %+v", st)
	}
	for _, m := range []string{"m1", "m2"} {
		var bids, asks int
		for _, o := range r.trading.orders(m) {
			if o.Side == domain.Buy {
				bids++
			} else {
				asks++
			}
		}
		if bids != 8 || asks != 8 {
			t.Fatalf("%s at the target: %d bids, %d asks", m, bids, asks)
		}
	}
}

// Nothing trades for three minutes (the market orders go nowhere): the
// watchdog ends the target that holds the price beyond the band and
// rebases the model at the band's anchor. A pause is no lock.
func TestTheWatchdogRebasesALockedMarket(t *testing.T) {
	r := bandRig(t, executorStore())
	r.prices.mu.Lock()
	r.prices.frozen, r.prices.lastAt = true, r.now
	r.prices.mu.Unlock()
	r.rounds(4)
	e := r.create(t, domain.Event{Type: domain.EventPause, Duration: 4 * time.Minute})
	r.rounds(4*4*60 + 8)
	if st := r.sim.Status(); st.Deadlocks != 0 {
		t.Fatalf("a pause of four minutes: %+v", st)
	}
	if got := r.store.event(e.ID); got.Status != domain.EventDone {
		t.Fatalf("the pause: %s", got.Status)
	}
	e = r.create(t, domain.Event{Type: domain.EventTarget, Price: d("1.35"), Hold: time.Hour, ApprovedBy: "ops2"})
	r.rounds(4 * 170)
	if st := r.sim.Status(); st.Deadlocks != 0 || st.Target != 1.35 {
		t.Fatalf("before three minutes: %+v", st)
	}
	r.rounds(4 * 15)
	st := r.sim.Status()
	if st.Deadlocks != 1 || st.Target > 1.0001*1.01 || st.Target < 1.0001*0.99 {
		t.Fatalf("after three minutes: deadlocks %d, target %v", st.Deadlocks, st.Target)
	}
	got := r.store.event(e.ID)
	if got.Status != domain.EventDone || got.EndedBy != watchdogActor {
		t.Fatalf("the target: %+v", got)
	}
	if a := r.store.audits[len(r.store.audits)-1]; a.Action != "market.sim.event_ended" || a.Actor != watchdogActor {
		t.Fatalf("audit %+v", a)
	}
}

// The watchdog waits while the last trade cannot be read (a locked market
// and a silent market-data-service look the same), and, without takers,
// while nothing need trade.
func TestTheWatchdogWaitsWithoutReadsOrTakers(t *testing.T) {
	r := bandRig(t, nil)
	r.prices.mu.Lock()
	r.prices.frozen, r.prices.lastAt = true, r.now
	r.prices.lastErr = errors.New("market-data-service unavailable")
	r.prices.mu.Unlock()
	r.rounds(4 * 4 * 60)
	if st := r.sim.Status(); st.Deadlocks != 0 {
		t.Fatalf("four minutes without reads: %+v", st)
	}
	r.prices.mu.Lock()
	r.prices.lastErr = nil
	r.prices.mu.Unlock()
	r.rounds(4 * 170)
	if st := r.sim.Status(); st.Deadlocks != 0 {
		t.Fatalf("reads back, before three minutes: %+v", st)
	}
	r.rounds(4 * 15)
	if st := r.sim.Status(); st.Deadlocks != 1 {
		t.Fatalf("reads back for three minutes, nothing traded: %+v", st)
	}

	p := domain.DefaultParams()
	p.DailyVolume = 0
	r = bandRig(t, &memStore{params: &p, version: 1})
	r.prices.mu.Lock()
	r.prices.frozen, r.prices.lastAt = true, r.now
	r.prices.mu.Unlock()
	r.rounds(4 * 6 * 60)
	if st := r.sim.Status(); st.Deadlocks != 0 {
		t.Fatalf("six minutes without takers: %+v", st)
	}
}

// Every level the makers place is refused for the band (the platform's
// anchor is not where the bots read it) for three minutes: the watchdog
// fires although the takers keep trading.
func TestTheWatchdogSeesEveryLevelRefused(t *testing.T) {
	r := bandRig(t, executorStore())
	r.trading.mu.Lock()
	r.trading.anchor = d("0.5")
	r.trading.mu.Unlock()
	r.rounds(4 * 170)
	if st := r.sim.Status(); st.Deadlocks != 0 || r.trading.outOfBand == 0 {
		t.Fatalf("before three minutes: %+v, %d refused", st, r.trading.outOfBand)
	}
	r.rounds(4 * 15)
	if st := r.sim.Status(); st.Deadlocks != 1 {
		t.Fatalf("after three minutes: %+v", st)
	}
	// A refusal for the band makes the maker wait (five seconds at most):
	// far fewer tries than rounds.
	if n := r.trading.tries["m1"]; n > 40 {
		t.Fatalf("m1 tried %d orders in three minutes", n)
	}
}

// refusal is the platform's answer of 4xx (ports.Refused).
type refusal string

func (r refusal) Error() string { return string(r) }
func (refusal) Refused() bool   { return true }

// A refused order costs no token and makes its bot wait, longer each
// time; the other bots trade on.
func TestARefusedBotWaitsAndCostsNoToken(t *testing.T) {
	p := domain.DefaultParams()
	p.OrdersPerSecond, p.DailyVolume = 4, 0
	r := newRig(t, &memStore{params: &p, version: 1})
	r.trading.mu.Lock()
	r.trading.refuse["m1"] = refusal("HTTP 400 ORDER_INVALID")
	r.trading.mu.Unlock()
	r.rounds(4 * 20)
	r.trading.mu.Lock()
	tries := r.trading.tries["m1"]
	r.trading.mu.Unlock()
	if tries < 3 || tries > 6 {
		t.Fatalf("m1 tried %d times in twenty seconds (waiting 1, 2, 4, 8 s)", tries)
	}
	if n := len(r.trading.orders("m2")); n != 16 {
		t.Fatalf("m2 has %d orders", n)
	}
	if st := r.sim.Status(); !st.Bots[0].RetryAt.After(r.now) || st.Bots[0].Error == "" {
		t.Fatalf("m1 %+v", st.Bots[0])
	}
}

// A maker short of USDT against the makers' average quotes smaller bids
// and larger asks; one rich in it the other way round.
func TestTheMakersLeanByTheirUSDT(t *testing.T) {
	p := domain.DefaultParams()
	p.DailyVolume = 0
	r := newRig(t, &memStore{params: &p, version: 1})
	r.trading.mu.Lock()
	r.trading.balances["m1"] = map[string]decimal.Decimal{"USDT": d("20000"), "ASTRA": d("40000000")}
	r.trading.balances["m2"] = map[string]decimal.Decimal{"USDT": d("180000"), "ASTRA": d("40000000")}
	r.trading.mu.Unlock()
	r.rounds(4 * 10)
	r.trading.mu.Lock()
	defer r.trading.mu.Unlock()
	poor, rich := r.trading.quantity["m1"], r.trading.quantity["m2"]
	if !poor[domain.Buy].Mul(d("1.5")).LessThan(poor[domain.Sell]) || !rich[domain.Sell].Mul(d("1.5")).LessThan(rich[domain.Buy]) {
		t.Fatalf("short of USDT: bids %s, asks %s; rich: bids %s, asks %s", poor[domain.Buy], poor[domain.Sell], rich[domain.Buy], rich[domain.Sell])
	}
}

// A request that failed on the way may have reached the book: it keeps
// its token. One refused for the price band costs none, and its bot waits
// five seconds at most.
func TestOnlyRefusalsGiveTheirTokenBack(t *testing.T) {
	p := domain.DefaultParams()
	p.OrdersPerSecond, p.DailyVolume = 4, 0
	r := newRig(t, &memStore{params: &p, version: 1})
	r.rounds(1)
	b := r.sim.bots[0]
	r.sim.mu.Lock()
	defer r.sim.mu.Unlock()
	now := r.now.Add(time.Minute) // the bucket full again
	before := r.sim.orders.Tokens(now)
	if !r.sim.orders.Take(now) {
		t.Fatal("no token")
	}
	r.sim.placed(context.Background(), now, b, errors.New("HTTP 502"), true)
	if got := r.sim.orders.Tokens(now); got != before-1 || b.retryAt.Sub(now) != time.Second {
		t.Fatalf("a failure: tokens %v -> %v, waits %v", before, got, b.retryAt.Sub(now))
	}
	r.sim.placed(context.Background(), now, b, errors.New("HTTP 502"), true)
	if b.retryAt.Sub(now) != 2*time.Second {
		t.Fatalf("a second failure waits %v", b.retryAt.Sub(now))
	}
	for range 3 {
		if !r.sim.orders.Take(now) {
			t.Fatal("no token")
		}
		r.sim.placed(context.Background(), now, b, fmt.Errorf("limit: %w", ports.ErrOutOfBand), true)
	}
	if got := r.sim.orders.Tokens(now); got != before-1 || b.retryAt.Sub(now) != 5*time.Second {
		t.Fatalf("out of band: tokens %v, waits %v", got, b.retryAt.Sub(now))
	}
}

// The makers leave the takers their share of the throttle: with four
// orders a second and both makers building their ladders, the takers'
// orders still go out.
func TestTheMakersLeaveTheTakersTheirShare(t *testing.T) {
	p := domain.DefaultParams()
	p.OrdersPerSecond, p.DailyVolume, p.OrderSize = 4, 86_400*400, 400 // a taker order a second
	r := newRig(t, &memStore{params: &p, version: 1})
	r.rounds(4 * 5)
	r.trading.mu.Lock()
	markets := r.trading.markets[domain.Buy] + r.trading.markets[domain.Sell]
	r.trading.mu.Unlock()
	if markets < 2 {
		t.Fatalf("%d taker orders in five seconds while the makers quote", markets)
	}
}

// A re-anchoring keeps its anchor P0 in the settings: a restart goes on
// from it rather than from the old P0.
func TestAReanchorKeepsItsAnchor(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	r.create(t, domain.Event{Type: domain.EventJump, Size: 0.2})
	r.rounds(4)
	r.create(t, domain.Event{Type: domain.EventReanchor})
	r.rounds(1)
	target := r.sim.Status().Target
	if math.Abs(r.store.params.P0/target-1) > 0.001 || r.store.version < 2 || r.store.byWhom != "ops" {
		t.Fatalf("P0 %v (target %v), version %d by %q", r.store.params.P0, target, r.store.version, r.store.byWhom)
	}
	if a := r.store.audits[len(r.store.audits)-1]; a.Action != "market.sim.params_changed" && a.Action != "market.sim.event_created" {
		t.Fatalf("audit %+v", a)
	}
	// The state was saved with the new P0: a crash before the next save
	// goes on from the anchor too.
	crashed := newRig(t, r.store)
	crashed.now = r.now
	crashed.rounds(1)
	if p := crashed.sim.Status().Target; p < target*0.99 || p > target*1.01 {
		t.Fatalf("restarted after a crash at %v, was %v", p, target)
	}
	r.rounds(4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.sim.Run(ctx); err != nil {
		t.Fatal(err)
	}
	again := newRig(t, r.store)
	again.now = r.now
	again.rounds(4)
	if p := again.sim.Status().Target; p < target*0.99 || p > target*1.01 {
		t.Fatalf("restarted at %v, was %v", p, target)
	}
}

// The heartbeat reports the target of the latest round, on the tick, and
// nothing once no round started for 30 seconds; with no recent trade the
// band's anchor is the pair's reference, as the trading service's.
func TestTheTargetIsReportedAndTheAnchorFallsBack(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.sim.beatOnce(ctx)
	r.rounds(4 * 11)
	r.sim.beatOnce(ctx)
	r.now = r.now.Add(29 * time.Second)
	r.sim.beatOnce(ctx)
	r.now = r.now.Add(time.Second)
	r.sim.beatOnce(ctx) // a stuck round loop: silent
	r.now = r.now.Add(-30 * time.Second)
	r.prices.mu.Lock()
	reported := slices.Clone(r.prices.reported)
	r.prices.lastAt, r.prices.ref = r.now.Add(-6*time.Minute), d("1.2")
	r.prices.mu.Unlock()
	if len(reported) != 2 || !reported[0].Equal(reported[0].Round(4)) || !reported[0].Equal(reported[1]) {
		t.Fatalf("reported %v", reported)
	}
	r.rounds(8)
	if st := r.sim.Status(); st.Anchor != 1.2 {
		t.Fatalf("the anchor of an old trade: %+v", st)
	}
}

// The perpetual's makers quote around its mark price pulled halfway to
// its index (a premium decays), within its band.
func TestThePerpetualQuotesAroundItsMark(t *testing.T) {
	p := domain.DefaultParams()
	p.DailyVolume, p.PerpDailyVolume = 0, 0
	r, fd := perpRig(t, p)
	fd.mu.Lock()
	fd.contract.Band = 0.05
	fd.mu.Unlock()
	r.prices.mu.Lock()
	r.prices.mark, r.prices.index = d("1.05"), d("1.03")
	r.prices.mu.Unlock()
	r.rounds(4 * 20)
	orders := fd.orders("m1")
	if len(orders) != 16 {
		t.Fatalf("%d orders", len(orders))
	}
	for _, o := range orders {
		if (o.Side == domain.Buy && o.Price.GreaterThanOrEqual(d("1.04"))) || (o.Side == domain.Sell && o.Price.LessThanOrEqual(d("1.04"))) ||
			o.Price.Sub(d("1.05")).Abs().GreaterThan(d("0.0525")) {
			t.Fatalf("%s at %s around 1.04, between a mark of 1.05 and an index of 1.03", o.Side, o.Price)
		}
	}
}

// A halt stops the perpetual with its index pair, and its end lets both
// trade again (ASTRA design §5.2).
func TestAHaltStopsThePerpetualToo(t *testing.T) {
	p := domain.DefaultParams()
	p.DailyVolume, p.PerpDailyVolume = 0, 0
	r, fd := perpRig(t, p)
	r.rounds(20)
	e := r.create(t, domain.Event{Type: domain.EventHalt})
	r.rounds(2)
	if !slices.Equal(r.pairs.changes, []string{"HALT"}) || !slices.Equal(r.pairs.contracts, []string{"HALT"}) || len(fd.orders("m1")) != 0 {
		t.Fatalf("halted: pair %v, contract %v, %d perpetual orders", r.pairs.changes, r.pairs.contracts, len(fd.orders("m1")))
	}
	if _, err := r.sim.EndEvent(context.Background(), e.ID, "ops", "resume"); err != nil {
		t.Fatal(err)
	}
	r.rounds(4 * 10)
	if !slices.Equal(r.pairs.contracts, []string{"HALT", "TRADING"}) || len(fd.orders("m1")) == 0 {
		t.Fatalf("resumed: contract %v, %d perpetual orders", r.pairs.contracts, len(fd.orders("m1")))
	}
}

// Ending a halt again after the perpetual failed to resume goes on where
// the first try stopped: the pair, already trading, is left alone.
func TestEndingAHaltAgainResumesWhatIsStillHalted(t *testing.T) {
	p := domain.DefaultParams()
	p.DailyVolume, p.PerpDailyVolume = 0, 0
	r, _ := perpRig(t, p)
	r.rounds(20)
	e := r.create(t, domain.Event{Type: domain.EventHalt})
	r.rounds(2)
	r.pairs.mu.Lock()
	r.pairs.contractErr = errors.New("instrument-service unavailable")
	r.pairs.mu.Unlock()
	if _, err := r.sim.EndEvent(context.Background(), e.ID, "ops", "resume"); err == nil {
		t.Fatal("the perpetual failed, the end passed")
	}
	if _, err := r.sim.EndEvent(context.Background(), e.ID, "ops", "resume again"); err != nil {
		t.Fatalf("again: %v", err)
	}
	if !slices.Equal(r.pairs.changes, []string{"HALT", "TRADING"}) || !slices.Equal(r.pairs.contracts, []string{"HALT", "TRADING"}) {
		t.Fatalf("pair %v, contract %v", r.pairs.changes, r.pairs.contracts)
	}
}

// The budget counts by when the moves take effect: a jump scheduled two
// hours ahead leaves this hour's budget alone; one within the hour takes
// from it. An event is due within a day; a jump or a target is at most
// +100% even approved.
func TestTheBudgetCountsByWhenEventsStart(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	r.create(t, domain.Event{Type: domain.EventJump, Size: 0.25, StartsAt: r.now.Add(2 * time.Hour)})
	r.create(t, domain.Event{Type: domain.EventJump, Size: 0.25, StartsAt: r.now.Add(10 * time.Minute)})
	ev := domain.Event{Type: domain.EventJump, Size: 0.3, CreatedBy: "ops", Reason: "test", StartsAt: r.now.Add(20 * time.Minute)}
	if _, err := r.sim.CreateEvent(context.Background(), ev); !apperrIs(err, "SIM_EVENT_NEEDS_APPROVAL") {
		t.Fatalf("0.25 and 0.3 within an hour of the first: %v", err)
	}
	ev.StartsAt = r.now.Add(80 * time.Minute) // within an hour of the one at 2 h, not of the one at 10 min
	if _, err := r.sim.CreateEvent(context.Background(), ev); !apperrIs(err, "SIM_EVENT_NEEDS_APPROVAL") {
		t.Fatalf("0.25 at 2 h and 0.3 at 80 min: %v", err)
	}
	ev.StartsAt = r.now.Add(5 * time.Hour)
	if _, err := r.sim.CreateEvent(context.Background(), ev); err != nil {
		t.Fatalf("an hour of its own: %v", err)
	}
	ev.StartsAt = r.now.Add(25 * time.Hour)
	if _, err := r.sim.CreateEvent(context.Background(), ev); !apperrIs(err, apperr.CodeInvalidArgument) {
		t.Fatalf("more than a day ahead: %v", err)
	}
	big := domain.Event{Type: domain.EventJump, Size: 1.5, CreatedBy: "ops", ApprovedBy: "ops2", Reason: "test"}
	if _, err := r.sim.CreateEvent(context.Background(), big); !apperrIs(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a jump of 150%%, approved: %v", err)
	}
	far := domain.Event{Type: domain.EventTarget, Price: d("2.5"), CreatedBy: "ops", ApprovedBy: "ops2", Reason: "test"}
	if _, err := r.sim.CreateEvent(context.Background(), far); !apperrIs(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a target 150%% away, approved: %v", err)
	}
	// A trend of 40% a day is beyond one operator.
	trend := domain.Event{Type: domain.EventTrend, Mu: 0.4, CreatedBy: "ops", Reason: "test", StartsAt: r.now.Add(10 * time.Hour)}
	if _, err := r.sim.CreateEvent(context.Background(), trend); !apperrIs(err, "SIM_EVENT_NEEDS_APPROVAL") {
		t.Fatalf("a trend of 40%% a day: %v", err)
	}
}

// A change of the settings takes from the same budget: P0 40% up needs a
// second operator, recorded with the change; then a jump in the hour is
// beyond one operator too. max_minute_move stops at 5%.
func TestSettingsChangesShareTheBudget(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4)
	p := domain.DefaultParams()
	p.P0 = 1.4
	if _, err := r.sim.UpdateParams(context.Background(), p, "ops", ""); !apperrIs(err, "SIM_PARAMS_NEED_APPROVAL") {
		t.Fatalf("P0 40%% up alone: %v", err)
	}
	if _, err := r.sim.UpdateParams(context.Background(), p, "ops", "ops"); !apperrIs(err, "SIM_PARAMS_NEED_APPROVAL") {
		t.Fatalf("approved by the same operator: %v", err)
	}
	if _, err := r.sim.UpdateParams(context.Background(), p, "ops", "ops2"); err != nil {
		t.Fatal(err)
	}
	last := r.store.changes[len(r.store.changes)-1]
	if last.ApprovedBy != "ops2" || math.Abs(last.Move-0.4) > 1e-9 {
		t.Fatalf("change %+v", last)
	}
	jump := domain.Event{Type: domain.EventJump, Size: 0.15, CreatedBy: "ops", Reason: "test"}
	if _, err := r.sim.CreateEvent(context.Background(), jump); !apperrIs(err, "SIM_EVENT_NEEDS_APPROVAL") {
		t.Fatalf("a jump after P0 moved 40%%: %v", err)
	}
	q := p
	q.MaxMinuteMove = 0.06
	if _, err := r.sim.UpdateParams(context.Background(), q, "ops", "ops2"); !apperrIs(err, apperr.CodeInvalidArgument) {
		t.Fatalf("max_minute_move 6%%: %v", err)
	}
	q = p
	q.DailyVolume = 5_000_000 // 2.5 times
	if _, err := r.sim.UpdateParams(context.Background(), q, "ops", ""); !apperrIs(err, "SIM_PARAMS_NEED_APPROVAL") {
		t.Fatalf("the turnover 2.5 times alone: %v", err)
	}
	q.DailyVolume = 3_000_000
	if _, err := r.sim.UpdateParams(context.Background(), q, "ops", ""); err != nil {
		t.Fatalf("the turnover 1.5 times alone: %v", err)
	}
}

// The chart's samples outlive a restart: the store keeps them and a new
// start loads the day before.
func TestTheChartOutlivesARestart(t *testing.T) {
	r := newRig(t, nil)
	r.rounds(4 * 60) // a minute: six samples
	if n := len(r.store.samples); n < 5 || n > 7 {
		t.Fatalf("%d samples kept", n)
	}
	again := newRig(t, r.store)
	if got := again.sim.History(r.now.Add(-time.Hour)); len(got) != len(r.store.samples) {
		t.Fatalf("%d samples after the restart, %d kept", len(got), len(r.store.samples))
	}
}
