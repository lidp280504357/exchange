package application

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketsim/domain"
	"github.com/skill/exchange/internal/marketsim/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// SaveEventsEach keeps one open overlay a pair, as the unique index does.
func (m *memStore) SaveEventsEach(ctx context.Context, es []domain.Event, audits []ports.Audit) error {
	m.mu.Lock()
	for _, e := range es {
		if slices.ContainsFunc(m.events, func(x domain.Event) bool {
			return x.ID != e.ID && x.Type == domain.EventOverlay && e.Type == domain.EventOverlay && x.Symbol == e.Symbol &&
				(x.Status == domain.EventScheduled || x.Status == domain.EventRunning)
		}) {
			m.mu.Unlock()
			return ports.ErrOverlayOpen
		}
	}
	m.mu.Unlock()
	for i, e := range es {
		if err := m.SaveEvent(ctx, e, &audits[i]); err != nil {
			return err
		}
	}
	return nil
}

// fakeOverlayMarket is market-data: the followed pairs' reference prices,
// shown with the factor last pushed; the pushes and clears it took.
type fakeOverlayMarket struct {
	mu       sync.Mutex
	refs     map[string]decimal.Decimal
	factors  map[string]decimal.Decimal
	pushes   []overlayPushed
	clears   []string
	failPush bool
	pushErr  error // a push's answer instead (a refusal)
	// hold, when set for a pair, holds its pushes until it is closed (a
	// market-data that does not answer).
	hold map[string]chan struct{}
}

// pushRefusal is market-data refusing a push with code.
type pushRefusal string

func (r pushRefusal) Error() string     { return "HTTP 409 " + string(r) }
func (pushRefusal) Refused() bool       { return true }
func (r pushRefusal) ErrorCode() string { return string(r) }

type overlayPushed struct {
	symbol string
	ports.OverlayPush
}

func (f *fakeOverlayMarket) Followed(_ context.Context, symbol string) (ports.Followed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.refs[symbol]
	if !ok {
		return ports.Followed{}, nil
	}
	factor, ok := f.factors[symbol]
	if !ok {
		factor = decimal.NewFromInt(1)
	}
	return ports.Followed{Followed: true, Source: p, Shown: p.Mul(factor).Round(2), Factor: factor, Fresh: true}, nil
}

func (f *fakeOverlayMarket) Push(_ context.Context, symbol string, p ports.OverlayPush) error {
	f.mu.Lock()
	wait := f.hold[symbol]
	f.mu.Unlock()
	if wait != nil {
		<-wait
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPush {
		return errors.New("market-data down")
	}
	if f.pushErr != nil {
		return f.pushErr
	}
	f.pushes = append(f.pushes, overlayPushed{symbol: symbol, OverlayPush: p})
	f.factors[symbol] = p.Factor
	return nil
}

func (f *fakeOverlayMarket) Clear(_ context.Context, symbol string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clears = append(f.clears, symbol)
	delete(f.factors, symbol)
	return nil
}

func (f *fakeOverlayMarket) last(symbol string) (overlayPushed, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	var last overlayPushed
	for _, p := range f.pushes {
		if p.symbol == symbol {
			last, n = p, n+1
		}
	}
	return last, n
}

type fakeHouse map[string]ports.Rooms

func (h fakeHouse) Rooms(_ context.Context, symbol string) (ports.Rooms, bool, error) {
	r, ok := h[symbol]
	return r, ok, nil
}

type overlayRig struct {
	*rig
	o      *Overlays
	market *fakeOverlayMarket
	house  fakeHouse
}

func newOverlayRig(t *testing.T) *overlayRig {
	t.Helper()
	r := &overlayRig{rig: newRig(t, nil)}
	r.flags.overlay = true
	r.market = &fakeOverlayMarket{
		refs: map[string]decimal.Decimal{"BTC-USDT": d("100000"), "ETH-USDT": d("4000")}, factors: map[string]decimal.Decimal{},
	}
	r.house = fakeHouse{
		"BTC-USDT": {Buy: d("1"), Sell: d("2"), UnitValue: d("100000")},
		"ETH-USDT": {Buy: d("10"), Sell: d("10"), UnitValue: d("4000")},
	}
	r.o = r.newOverlays(t)
	return r
}

// newOverlays is another instance on the rig's store (a restart).
func (r *overlayRig) newOverlays(t *testing.T) *Overlays {
	t.Helper()
	o := NewOverlays(OverlayConfig{MaxLoss: d("200000"), MaxTotal: 10 * time.Minute, Every: time.Second}, r.market, r.house, r.store,
		r.flags, r.sim, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	o.now = func() time.Time { return r.now }
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return o
}

// seconds steps the clock a second at a time, n times, pushing each.
func (r *overlayRig) seconds(n int) {
	for range n {
		r.now = r.now.Add(time.Second)
		r.o.Round(context.Background())
		r.o.settle()
	}
}

func pct(x float64) *float64 { return &x }

func btcSpike(change float64) OverlayRequest {
	return OverlayRequest{
		Symbols: []string{"btc-usdt"}, TargetPct: pct(change), RampUp: 15 * time.Second, RampDown: 5 * time.Second, Risk: true,
		Actor: "ops", Reason: "a spike for the demo",
	}
}

func code(err error) string {
	if e := (*apperr.Error)(nil); errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// BTC-USDT +16% in 15 seconds, held 0, back in 5 (design 2026-10-07 §5
// J2): the factor climbs a fifteenth of the way a second, comes back down
// in 5, and the event is done at 1 with the reference and platform prices
// of its end; every push holds 4 seconds, its seq ever larger.
func TestAnOverlayRunsItsScheduleAndComesBack(t *testing.T) {
	r := newOverlayRig(t)
	made, err := r.o.Create(context.Background(), btcSpike(16))
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 1 || made[0].Symbol != "BTC-USDT" || made[0].Event.Status != domain.EventRunning || !near(made[0].TargetFactor, 1.16) ||
		!made[0].BasePrice.Equal(d("100000")) || !made[0].Event.Price.Equal(d("116000")) {
		t.Fatalf("made %+v", made)
	}
	id := made[0].Event.ID
	want := map[int]float64{1: 1 + 0.16/15, 7: 1 + 0.16*7/15, 15: 1.16, 17: 1.16 - 0.16*2/5, 19: 1.16 - 0.16*4/5}
	var seq int64
	for s := 1; s <= 19; s++ {
		r.seconds(1)
		last, _ := r.market.last("BTC-USDT")
		if f, ok := want[s]; ok && !near(last.Factor.InexactFloat64(), f) {
			t.Fatalf("second %d: factor %s, want %v", s, last.Factor, f)
		}
		if last.Seq <= seq || !last.Until.Equal(r.now.Add(4*time.Second)) || !last.Risk || last.EventID != id {
			t.Fatalf("second %d: push %+v", s, last)
		}
		seq = last.Seq
	}
	if e := r.store.event(id); e.Status != domain.EventRunning || !e.PeakPrice.Equal(d("116000")) {
		t.Fatalf("before its end: %+v", e)
	}
	r.seconds(1)
	last, _ := r.market.last("BTC-USDT")
	e := r.store.event(id)
	if !last.Factor.Equal(decimal.NewFromInt(1)) || e.Status != domain.EventDone || e.Result != "" || !e.EndedAt.Equal(r.now) ||
		!e.EndReferencePrice.Equal(d("100000")) || !e.EndPlatformPrice.Equal(d("100000")) || !e.PeakPrice.Equal(d("116000")) {
		t.Fatalf("at its end: push %+v, event %+v", last, e)
	}
	if r.o.Has(id) {
		t.Fatal("a done event still open")
	}
	_, n := r.market.last("BTC-USDT")
	r.seconds(3)
	if _, m := r.market.last("BTC-USDT"); m != n {
		t.Fatal("pushes after the end")
	}
	var actions []string
	for _, a := range r.store.audits {
		actions = append(actions, a.Action)
	}
	if !slices.Equal(actions, []string{"market.sim.event_created", "market.sim.event_done"}) {
		t.Fatalf("audit %v", actions)
	}
}

// The checks of the J0 contract §3.1: a pair no reference market follows,
// a target beyond ±90%, HOUSE's worst loss beyond the cap (with its
// estimate), a second event on a pair, the switch off, a target price for
// several pairs; beyond 30% alone a second operator approves.
func TestOverlayRefusals(t *testing.T) {
	r := newOverlayRig(t)
	ctx := context.Background()
	check := func(name string, req OverlayRequest, want string) {
		t.Helper()
		if _, err := r.o.Create(ctx, req); code(err) != want {
			t.Fatalf("%s: %v, want %s", name, err, want)
		}
	}
	req := btcSpike(16)
	req.Symbols = []string{"DOGE-BTC"}
	check("not followed", req, "SIM_NOT_OVERLAYABLE")
	check("too far", btcSpike(95), "SIM_OVERLAY_TOO_FAR")
	check("alone beyond 30%", btcSpike(-35), "SIM_EVENT_NEEDS_APPROVAL")
	r.house["BTC-USDT"] = ports.Rooms{Buy: d("20"), Sell: d("2"), UnitValue: d("100000")}
	_, err := r.o.Create(ctx, btcSpike(16))
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != "SIM_OVERLAY_LOSS_CAP" || e.Details["estimate_usdt"] != "320000" || e.Details["cap_usdt"] != "200000" {
		t.Fatalf("loss cap: %v %+v", err, e)
	}
	check("down with the sell room", btcSpike(-16), "") // 2 BTC × 100,000 × 0.16
	check("a second on the pair", btcSpike(1), "SIM_OVERLAY_RUNNING")
	req = btcSpike(16)
	req.Symbols, req.TargetPct, req.TargetPrice = []string{"ETH-USDT", "BTC-USDT"}, nil, d("5000")
	check("a price for two pairs", req, apperr.CodeInvalidArgument)
	req = btcSpike(16)
	req.RampDown = 2 * time.Second
	check("a ramp down too short", req, apperr.CodeInvalidArgument)
	req.RampDown, req.Hold = 5*time.Second, 10*time.Minute
	check("too long", req, apperr.CodeInvalidArgument)
	r.flags.overlay = false
	check("switched off", btcSpike(16), "SIM_OVERLAY_OFF")
	r.flags.overlay = true
	approved := btcSpike(-35)
	approved.Symbols, approved.ApprovedBy = []string{"ETH-USDT"}, "ops2"
	check("approved beyond 30%", approved, "")
}

// The guards count each pair's moves within any hour (J0 contract §3.1):
// +25% and +25% one after the other pass alone, a third move does not.
func TestOverlaysSpendTheirPairsBudget(t *testing.T) {
	r := newOverlayRig(t)
	ctx := context.Background()
	quick := func(change float64) OverlayRequest {
		req := btcSpike(change)
		req.RampUp, req.RampDown = time.Second, 3*time.Second
		return req
	}
	for i := range 2 {
		if _, err := r.o.Create(ctx, quick(25)); err != nil {
			t.Fatalf("move %d: %v", i+1, err)
		}
		r.seconds(5)
	}
	if _, err := r.o.Create(ctx, quick(5)); code(err) != "SIM_EVENT_NEEDS_APPROVAL" {
		t.Fatalf("a third move in the hour: %v", err)
	}
	other := quick(25)
	other.Symbols = []string{"ETH-USDT"}
	if _, err := r.o.Create(ctx, other); err != nil {
		t.Fatalf("another pair's budget: %v", err)
	}
}

// "Restore now" (J0 contract §3.4): from where the factor stands, back to
// 1 in 3 seconds, then done, CANCELED, ended by the operator.
func TestEndingAnOverlayTakesItBackInThreeSeconds(t *testing.T) {
	r := newOverlayRig(t)
	made, err := r.o.Create(context.Background(), btcSpike(15))
	if err != nil {
		t.Fatal(err)
	}
	id := made[0].Event.ID
	r.seconds(10) // 1.10
	e, err := r.o.End(context.Background(), id, "ops2", "enough")
	if err != nil || e.Status != domain.EventRunning || e.EndedBy != "ops2" || e.Result != domain.ResultCanceled {
		t.Fatalf("ended %+v %v", e, err)
	}
	r.seconds(1)
	if last, _ := r.market.last("BTC-USDT"); !near(last.Factor.InexactFloat64(), 1.1-0.1/3) {
		t.Fatalf("a second after: %s", last.Factor)
	}
	r.seconds(2)
	last, _ := r.market.last("BTC-USDT")
	if e := r.store.event(id); !last.Factor.Equal(decimal.NewFromInt(1)) || e.Status != domain.EventDone || e.Result != domain.ResultCanceled ||
		e.EndedBy != "ops2" {
		t.Fatalf("three seconds after: %s %+v", last.Factor, e)
	}
	// A scheduled one is canceled outright.
	req := btcSpike(5)
	req.StartsAt = r.now.Add(time.Hour)
	made, err = r.o.Create(context.Background(), req)
	if err != nil || made[0].Event.Status != domain.EventScheduled {
		t.Fatalf("scheduled %+v %v", made, err)
	}
	if e, err := r.o.End(context.Background(), made[0].Event.ID, "ops", "not today"); err != nil || e.Status != domain.EventCanceled {
		t.Fatalf("canceled %+v %v", e, err)
	}
}

// A scheduled event starts at its time from the reference price then; a
// restart goes on along its schedule, not from 1; the switch turned off
// ends it, CANCELED.
func TestOverlaysStartOnTimeResumeAndStopWithTheSwitch(t *testing.T) {
	r := newOverlayRig(t)
	req := btcSpike(10)
	req.StartsAt, req.Hold = r.now.Add(30*time.Second), time.Minute
	made, err := r.o.Create(context.Background(), req)
	if err != nil || made[0].Event.Status != domain.EventScheduled || made[0].BasePrice.IsPositive() {
		t.Fatalf("made %+v %v", made, err)
	}
	id := made[0].Event.ID
	r.market.refs["BTC-USDT"] = d("101000")
	r.seconds(29)
	if _, n := r.market.last("BTC-USDT"); n != 0 {
		t.Fatal("pushed before its time")
	}
	r.seconds(1)
	if e := r.store.event(id); e.Status != domain.EventRunning || !e.BasePrice.Equal(d("101000")) || !e.StartedAt.Equal(r.now) {
		t.Fatalf("started %+v", e)
	}
	r.seconds(20) // in its hold
	r.o = r.newOverlays(t)
	r.seconds(1)
	if last, _ := r.market.last("BTC-USDT"); !near(last.Factor.InexactFloat64(), 1.1) {
		t.Fatalf("after a restart: %s", last.Factor)
	}
	r.flags.overlay = false
	r.seconds(1)
	e := r.store.event(id)
	if e.Status != domain.EventDone || e.Result != domain.ResultCanceled || e.EndedBy != "system:market.overlay" ||
		!slices.Equal(r.market.clears, []string{"BTC-USDT"}) || r.o.Has(id) {
		t.Fatalf("switched off: %+v, clears %v", e, r.market.clears)
	}
}

// A request naming the simulated market's pair too makes it a JUMP of the
// model over the ramp up (it has no reference market to come back to),
// the followed pairs OVERLAY events; refused for one, made for none.
func TestTheSimulatedPairGetsAJump(t *testing.T) {
	r := newOverlayRig(t)
	r.rounds(8) // the model has a price
	req := btcSpike(10)
	req.Symbols = []string{"ASTRA-USDT", "BTC-USDT", "ETH-USDT"}
	made, err := r.o.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 3 || made[0].Symbol != "ASTRA-USDT" || made[0].Event.Type != domain.EventJump || !near(made[0].Event.Size, 0.1) ||
		made[0].Event.Duration != 15*time.Second || made[1].Event.Type != domain.EventOverlay || made[2].Symbol != "ETH-USDT" {
		t.Fatalf("made %+v", made)
	}
	r.seconds(1)
	if last, n := r.market.last("ETH-USDT"); n != 1 || !near(last.Factor.InexactFloat64(), 1+0.1/15) {
		t.Fatalf("ETH-USDT's first push: %d, %s", n, last.Factor)
	}
	n := len(r.store.events)
	req.Symbols = []string{"ASTRA-USDT", "DOGE-BTC"}
	if _, err := r.o.Create(context.Background(), req); code(err) != "SIM_NOT_OVERLAYABLE" || len(r.store.events) != n {
		t.Fatalf("a refused pair: %v, %d events", err, len(r.store.events)-n)
	}
}

// HOUSE's worst loss counts its rooms on the pair and, with risk, on the
// pair's perpetuals (an inverse contract's loss in its own terms); a
// perpetual it does not quote adds nothing, the pair it does not quote is
// unknown.
func TestOverlayLossCountsThePerpetualsWithRisk(t *testing.T) {
	r := newOverlayRig(t)
	r.house["BTC-USDT-PERP"] = ports.Rooms{Buy: d("3"), Sell: d("1"), UnitValue: d("100000")}
	r.house["BTC-USD-PERP"] = ports.Rooms{Buy: d("1000"), Sell: d("500"), UnitValue: d("100"), Inverse: true}
	ctx := context.Background()
	loss, err := r.o.lossOf(ctx, "BTC-USDT", 1.25, true)
	// (1 + 3) BTC × 100,000 × 0.25 + 1,000 × 100 × (1 - 1/1.25)
	if err != nil || !loss.Equal(d("120000")) {
		t.Fatalf("up with risk: %s %v", loss, err)
	}
	if loss, _ := r.o.lossOf(ctx, "BTC-USDT", 1.25, false); !loss.Equal(d("25000")) {
		t.Fatalf("up without risk: %s", loss)
	}
	// (2 + 1) BTC × 100,000 × 0.2 + 500 × 100 × (1/0.8 - 1)
	if loss, _ := r.o.lossOf(ctx, "BTC-USDT", 0.8, true); !loss.Round(6).Equal(d("72500")) {
		t.Fatalf("down with risk: %s", loss)
	}
	if loss, _ := r.o.lossOf(ctx, "ETH-USDT", 1.1, true); !loss.Equal(d("4000")) {
		t.Fatalf("no perpetuals quoted: %s", loss)
	}
	if _, err := r.o.lossOf(ctx, "SOL-USDT", 1.1, true); err == nil {
		t.Fatal("a pair HOUSE does not quote estimated")
	}
}

// The pushes failing (review C57 ③): one run of five or more (market-data
// dropped the factor: a restart) is gone through, the pushes going on
// after it; a second, or two minutes of them, cancel the event - the pair
// would jump back and forth; a refusal that will not pass cancels it at
// once, one that may (market.overlay not yet on there) counts as a failure.
func TestAnEventWhosePushesFailIsCanceled(t *testing.T) {
	r := newOverlayRig(t)
	req := btcSpike(10)
	req.Hold = 5 * time.Minute
	made, err := r.o.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	id := made[0].Event.ID
	r.market.failPush = true
	r.seconds(20) // market-data restarting
	r.market.failPush = false
	r.seconds(1)
	if !r.o.Has(id) {
		t.Fatal("canceled for a market-data restart")
	}
	if last, _ := r.market.last("BTC-USDT"); !near(last.Factor.InexactFloat64(), 1.1) {
		t.Fatalf("after the restart, in its hold: %s", last.Factor)
	}
	r.market.failPush = true
	r.seconds(4)
	if !r.o.Has(id) {
		t.Fatal("canceled before the second run reached five")
	}
	r.seconds(1)
	e := r.store.event(id)
	if r.o.Has(id) || e.Status != domain.EventDone || e.Result != domain.ResultCanceled || e.EndedBy != "system:market-sim" ||
		!slices.Equal(r.market.clears, []string{"BTC-USDT"}) {
		t.Fatalf("after five failures: %+v, clears %v", e, r.market.clears)
	}
	if a := r.store.audits[len(r.store.audits)-1]; a.Action != "market.sim.event_ended" || a.Reason == "" {
		t.Fatalf("audit %+v", a)
	}

	// Two minutes of failures in one run: market-data gone for good.
	long := btcSpike(5)
	long.Hold = 5 * time.Minute
	made, err = r.o.Create(context.Background(), long)
	if err != nil {
		t.Fatal(err)
	}
	r.seconds(119)
	if !r.o.Has(made[0].Event.ID) {
		t.Fatal("canceled before two minutes of failures")
	}
	r.seconds(1)
	if e := r.store.event(made[0].Event.ID); r.o.Has(e.ID) || e.Status != domain.EventDone || e.Result != domain.ResultCanceled {
		t.Fatalf("after two minutes of failures: %+v", e)
	}

	r.market.failPush = false
	other := btcSpike(5)
	other.Symbols = []string{"ETH-USDT"}
	made, err = r.o.Create(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	r.market.pushErr = pushRefusal("MARKET_OVERLAY_OFF")
	r.seconds(2)
	if !r.o.Has(made[0].Event.ID) {
		t.Fatal("market.overlay not yet on in market-data canceled the event at once")
	}
	r.market.pushErr = pushRefusal("MARKET_NOT_FOLLOWED")
	r.seconds(1)
	if e := r.store.event(made[0].Event.ID); r.o.Has(e.ID) || e.Status != domain.EventDone || e.Result != domain.ResultCanceled {
		t.Fatalf("a pair no longer followed: %+v", e)
	}
}

// A push market-data is slow to answer holds up only its own event: the
// lookups, a new event and ending another event go on meanwhile (review
// C57 ②).
func TestASlowPushHoldsUpOnlyItsEvent(t *testing.T) {
	r := newOverlayRig(t)
	ctx := context.Background()
	btc, err := r.o.Create(ctx, btcSpike(5))
	if err != nil {
		t.Fatal(err)
	}
	eth := btcSpike(5)
	eth.Symbols = []string{"ETH-USDT"}
	ethMade, err := r.o.Create(ctx, eth)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	r.market.mu.Lock()
	r.market.hold = map[string]chan struct{}{"BTC-USDT": release}
	r.market.mu.Unlock()
	r.now = r.now.Add(time.Second)
	r.o.Round(ctx) // BTC-USDT's push hangs; the round does not wait for it
	// The next rounds push ETH-USDT again (once its last round's work is
	// done) and skip BTC-USDT, whose push still hangs.
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, n := r.market.last("ETH-USDT"); n >= 2 {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("ETH-USDT pushed %d times while BTC-USDT's push hung", n)
		}
		r.o.Round(ctx)
	}
	answered := make(chan error, 1)
	go func() {
		if !r.o.Has(btc[0].Event.ID) {
			answered <- errors.New("the BTC-USDT event not open")
			return
		}
		_, err := r.o.End(ctx, ethMade[0].Event.ID, "ops2", "enough")
		answered <- err
	}()
	select {
	case err := <-answered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the lookups and another event's end waited on a slow push")
	}
	close(release)
	r.o.settle()
}

// A push that fails is made again the next second; market-data, which
// drops a factor 5 seconds after its push, is never left with a stale one
// for long.
func TestAFailedPushIsRetried(t *testing.T) {
	r := newOverlayRig(t)
	if _, err := r.o.Create(context.Background(), btcSpike(10)); err != nil {
		t.Fatal(err)
	}
	r.seconds(2)
	r.market.failPush = true
	r.seconds(2)
	_, n := r.market.last("BTC-USDT")
	r.market.failPush = false
	r.seconds(1)
	last, m := r.market.last("BTC-USDT")
	if m != n+1 || !near(last.Factor.InexactFloat64(), 1+0.1*5/15) {
		t.Fatalf("after the failures: %d pushes, factor %s", m-n, last.Factor)
	}
}
