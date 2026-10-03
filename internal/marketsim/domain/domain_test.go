package domain

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var (
	t0    = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	astra = Pair{Symbol: "ASTRA-USDT", Tick: d("0.0001"), Lot: d("1"), MinQty: d("1"), MinNotional: d("5"), QuoteDecimals: 6, Trading: true}
)

// quiet is the model without its own randomness: the target is the market
// factor alone.
func quiet() Params {
	p := DefaultParams()
	p.Sigma, p.Theta = 0, 0
	return p
}

func TestTheTargetFollowsTheReturnsOfBTCAndETH(t *testing.T) {
	m := NewModel(quiet(), State{}, 1)
	if p, _ := m.Step(t0, 60000, 3000, Shape{}); math.Abs(p-1) > 1e-12 {
		t.Fatalf("anchored at %v", p)
	}
	// Both up 1%: ASTRA up about 1% (beta 1, half each).
	p, g := m.Step(t0.Add(250*time.Millisecond), 60600, 3030, Shape{})
	if g != GuardNone || math.Abs(p-1.01) > 1e-9 {
		t.Fatalf("both up 1%%: %v %q", p, g)
	}
	// BTC up 2%, ETH flat: about +1%, as a weighted log return.
	p, _ = m.Step(t0.Add(500*time.Millisecond), 61200, 3000, Shape{})
	if want := math.Exp(0.5 * math.Log(1.02)); math.Abs(p-want) > 1e-9 {
		t.Fatalf("BTC up 2%%: %v, want %v", p, want)
	}
	// Prices that are not fresh keep the factor.
	if q, _ := m.Step(t0.Add(750*time.Millisecond), 0, 0, Shape{}); q != p {
		t.Fatalf("stale prices moved the target: %v -> %v", p, q)
	}
	// Beta 0 does not follow.
	flat := quiet()
	flat.Beta = 0
	m = NewModel(flat, State{}, 1)
	m.Step(t0, 60000, 3000, Shape{})
	if p, _ := m.Step(t0.Add(time.Second), 70000, 3500, Shape{}); p != 1 {
		t.Fatalf("beta 0: %v", p)
	}
}

func TestTheMinuteGuardBoundsTheSpeedNotTheLevel(t *testing.T) {
	m := NewModel(quiet(), State{}, 1)
	m.Step(t0, 60000, 3000, Shape{})
	// BTC and ETH jump 10%: the target moves 3% in the minute, then
	// catches up a minute at a time.
	p, g := m.Step(t0.Add(time.Second), 66000, 3300, Shape{})
	if g != GuardMinute || math.Abs(p-1.03) > 1e-9 {
		t.Fatalf("the jump: %v %q", p, g)
	}
	now := t0.Add(time.Second)
	for i := 0; i < 5; i++ {
		now = now.Add(61 * time.Second)
		p, _ = m.Step(now, 66000, 3300, Shape{})
	}
	if math.Abs(p-1.1) > 1e-9 {
		t.Fatalf("five minutes on: %v, want 1.1", p)
	}
}

func TestTheFloorAndCeilingHold(t *testing.T) {
	p := quiet()
	p.P0, p.Floor, p.Ceiling, p.MaxMinuteMove = 1, 0.99, 1.005, 0.5
	m := NewModel(p, State{}, 1)
	m.Step(t0, 60000, 3000, Shape{})
	if got, g := m.Step(t0.Add(time.Second), 66000, 3300, Shape{}); got != 1.005 || g != GuardCeil {
		t.Fatalf("ceiling: %v %q", got, g)
	}
	if got, g := m.Step(t0.Add(2*time.Second), 54000, 2700, Shape{}); got != 0.99 || g != GuardFloor {
		t.Fatalf("floor: %v %q", got, g)
	}
}

// The same seed, or the same saved state, gives the same path.
func TestThePathRepeatsFromItsSeedOrItsState(t *testing.T) {
	run := func(m *Model, from time.Time, n int) []float64 {
		var out []float64
		for i := range n {
			p, _ := m.Step(from.Add(time.Duration(i+1)*250*time.Millisecond), 60000, 3000, Shape{})
			out = append(out, p)
		}
		return out
	}
	a, b := run(NewModel(DefaultParams(), State{}, 42), t0, 50), run(NewModel(DefaultParams(), State{}, 42), t0, 50)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("step %d: %v != %v", i, a[i], b[i])
		}
	}
	if a[49] == 1 {
		t.Fatal("the own deviation never moved")
	}
	m := NewModel(DefaultParams(), State{}, 7)
	run(m, t0, 20)
	saved := m.Snapshot()
	at := m.State.At
	want := run(m, at, 20)
	got := run(NewModel(DefaultParams(), saved, 999), at, 20)
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("after a restart, step %d: %v != %v", i, got[i], want[i])
		}
	}
}

func TestReanchor(t *testing.T) {
	m := NewModel(quiet(), State{}, 1)
	m.Step(t0, 60000, 3000, Shape{})
	m.Step(t0.Add(time.Second), 60600, 3030, Shape{})
	m.Reanchor(60600, 3030)
	if math.Abs(m.Params.P0-1.01) > 1e-9 {
		t.Fatalf("P0 %v", m.Params.P0)
	}
	if p, _ := m.Step(t0.Add(2*time.Second), 60600, 3030, Shape{}); math.Abs(p-1.01) > 1e-9 {
		t.Fatalf("after reanchoring: %v", p)
	}
}

func TestLadderPricesSitOnTheMakersGrid(t *testing.T) {
	p := DefaultParams() // 8 levels, 0.2%, every 5 ticks
	bids, asks := LadderPrices(1.0, 0, p, astra)
	if len(bids) != 8 || len(asks) != 8 {
		t.Fatalf("%d bids, %d asks", len(bids), len(asks))
	}
	// 0.1% below and above 1.0000, on the grid of 5 ticks.
	if bids[0].String() != "0.999" || asks[0].String() != "1.001" || bids[1].String() != "0.9985" || asks[7].String() != "1.0045" {
		t.Fatalf("bids %v asks %v", bids, asks)
	}
	// Another phase quotes between them.
	b2, a2 := LadderPrices(1.0, 2, p, astra)
	if b2[0].String() != "0.9987" || a2[0].String() != "1.0012" {
		t.Fatalf("phase 2: bids %v asks %v", b2, a2)
	}
	// A move of less than a grid step keeps the levels.
	b3, a3 := LadderPrices(1.0002, 0, p, astra)
	if !b3[0].Equal(bids[0]) || !a3[0].Equal(d("1.0015")) {
		t.Fatalf("a small move: bids %v asks %v", b3, a3)
	}
}

func TestDiffKeepsWhatIsWanted(t *testing.T) {
	open := []Order{
		{ID: "b1", Side: Buy, Price: d("0.999")},
		{ID: "b2", Side: Buy, Price: d("0.998")}, // no longer wanted
		{ID: "b3", Side: Buy, Price: d("0.999")}, // a second at the same price
		{ID: "a1", Side: Sell, Price: d("1.001"), Canceling: true},
	}
	cancel, bids, asks := Diff(open, []decimal.Decimal{d("0.999"), d("0.9985")}, []decimal.Decimal{d("1.001")})
	if len(cancel) != 2 || cancel[0].ID != "b2" || cancel[1].ID != "b3" {
		t.Fatalf("cancel %+v", cancel)
	}
	if len(bids) != 1 || bids[0].String() != "0.9985" || len(asks) != 1 || asks[0].String() != "1.001" {
		t.Fatalf("place bids %v asks %v", bids, asks)
	}
}

func TestQuantityMeetsThePairsMinimums(t *testing.T) {
	if q := Quantity(800, d("0.9993"), astra); q.String() != "800" {
		t.Fatalf("800 USDT: %s", q)
	}
	if q := Quantity(2, d("1.25"), astra); q.String() != "4" {
		t.Fatalf("below the minimum notional: %s", q)
	}
	if q := Quantity(10, decimal.Zero, astra); !q.IsZero() {
		t.Fatalf("no price: %s", q)
	}
}

// A day of taker orders is worth the turnover asked for: 1,600,000 (the
// takers' share of 2,000,000) in orders of a median 400, a mean of 551.
func TestArrivalsMakeTheDaysTurnover(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // a repeatable test
	n, worth, now := 0, 0.0, t0.Truncate(24*time.Hour)
	for i := 0; i < 86400*4; i++ {
		now = now.Add(250 * time.Millisecond)
		for range Arrivals(rng, 1_600_000, 400, 250*time.Millisecond, now) {
			n++
			worth += Worth(rng, 400, OrderSpread)
		}
	}
	if n < 2700 || n > 3100 || worth < 1_500_000 || worth > 1_700_000 {
		t.Fatalf("%d orders worth %.0f in a day, want about 2,900 worth 1,600,000", n, worth)
	}
	if Arrivals(rng, 0, 400, time.Second, now) != 0 {
		t.Fatal("orders without a turnover")
	}
	// The trend followers' share: 4 of them deciding every 30 seconds with
	// a chance of 0.3 make 400,000 a day.
	p := DefaultParams()
	median := TrendWorth(p, 4, 30*time.Second)
	if day := MeanWorth(median, OrderSpread) * 2880 * 4 * 0.3; math.Abs(day-400_000) > 1 {
		t.Fatalf("the trend followers' day: %.0f", day)
	}
}

// A quiet market's order traded ahead of the takers' budget comes off
// their next orders (coordinator 2026-10-04: counted in the budget).
func TestRepay(t *testing.T) {
	for _, c := range []struct{ worth, debt, left, owed float64 }{
		{400, 0, 400, 0},   // nothing owed
		{400, 5, 395, 0},   // a quiet order of 5 comes off
		{400, 900, 0, 500}, // the order goes to the debt
		{400, 397, 0, 0},   // 3 left: not worth an order
	} {
		if left, owed := Repay(c.worth, c.debt, 5); left != c.left || owed != c.owed {
			t.Fatalf("Repay(%v, %v) = %v, %v", c.worth, c.debt, left, owed)
		}
	}
}

func TestTakerSideAndLean(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // a repeatable test
	count := func(lean float64) int {
		buys := 0
		for range 10000 {
			if TakerSide(rng, DefaultParams(), lean) == Buy {
				buys++
			}
		}
		return buys
	}
	if b := count(0); b < 4800 || b > 5200 {
		t.Fatalf("even odds: %d buys", b)
	}
	if b := count(-1); b > 3200 {
		t.Fatalf("leaning to sell: %d buys", b)
	}
	if Lean(50_000, 100_000) != -0.5 || Lean(300_000, 100_000) != 1 || Lean(0, 100_000) != -1 {
		t.Fatal("lean")
	}
}

func TestTrendSide(t *testing.T) {
	var h []Mark
	for i := range 20 {
		h = append(h, Mark{At: t0.Add(time.Duration(i) * time.Minute), P: 1 + float64(i)*0.001})
	}
	now := t0.Add(19 * time.Minute)
	if s, ok := TrendSide(h, now, 15*time.Minute); !ok || s != Buy {
		t.Fatalf("rising: %q %v", s, ok)
	}
	if _, ok := TrendSide(h[10:], now, 15*time.Minute); ok {
		t.Fatal("a history shorter than the window")
	}
	two := []Mark{{At: t0, P: 1.1}, {At: t0.Add(20 * time.Minute), P: 1.0}}
	if s, ok := TrendSide(two, t0.Add(20*time.Minute), 15*time.Minute); ok {
		t.Fatalf("no mark inside the window but the last: %q", s)
	}
}

func TestBucket(t *testing.T) {
	b := &Bucket{Rate: 2, Burst: 2}
	first, second, third := b.Take(t0), b.Take(t0), b.Take(t0)
	if !first || !second || third {
		t.Fatal("the burst")
	}
	if !b.Take(t0.Add(500*time.Millisecond)) || b.Take(t0.Add(500*time.Millisecond)) {
		t.Fatal("refilled at the rate")
	}
}

func TestParamsValidate(t *testing.T) {
	if err := DefaultParams().Validate(); err != nil {
		t.Fatal(err)
	}
	p := DefaultParams()
	p.Spread, p.Levels, p.Floor = 0, 0, -1
	if err := p.Validate(); err == nil {
		t.Fatal("bad settings passed")
	}
	p = DefaultParams()
	p.Sigma = math.NaN()
	if err := p.Validate(); err == nil {
		t.Fatal("NaN passed")
	}
	// The hard limits hold whoever signs the change (review M4).
	for name, change := range map[string]func(*Params){
		"a lower floor":       func(p *Params) { p.Floor = 0.00001 },
		"a higher ceiling":    func(p *Params) { p.Ceiling = 2_000_000 },
		"more orders":         func(p *Params) { p.OrdersPerSecond = 101 },
		"more cancels":        func(p *Params) { p.CancelsPerSecond = 101 },
		"more turnover":       func(p *Params) { p.DailyVolume = 100_000_001 },
		"bigger orders":       func(p *Params) { p.OrderSize = 50_001 },
		"bigger levels":       func(p *Params) { p.LevelSize = 50_001 },
		"richer bots":         func(p *Params) { p.BotUSDT = 10_000_001 },
		"more perp turnover":  func(p *Params) { p.PerpDailyVolume = 100_000_001 },
		"bigger perp caps":    func(p *Params) { p.PerpBotCap = 10_000_001 },
		"bigger perp margins": func(p *Params) { p.PerpMargin = 10_000_001 },
	} {
		p := DefaultParams()
		change(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func started(t EventType, from string) *Event {
	return &Event{Type: t, Status: EventRunning, StartedAt: t0, FromP: d(from), CreatedBy: "ops", Reason: "test"}
}

func TestEventShapes(t *testing.T) {
	// An instant jump: the factor at once, and it ends.
	jump := started(EventJump, "1")
	jump.Size = 0.1
	sh, ended := ShapeOf([]*Event{jump}, t0)
	if sh.LogE == nil || math.Abs(*sh.LogE-math.Log(1.1)) > 1e-12 || !sh.Moving || len(ended) != 1 {
		t.Fatalf("instant jump: %+v %v", sh, ended)
	}
	// Over a minute: half of it at 30 seconds.
	jump.Duration = time.Minute
	sh, ended = ShapeOf([]*Event{jump}, t0.Add(30*time.Second))
	if math.Abs(*sh.LogE-0.5*math.Log(1.1)) > 1e-12 || len(ended) != 0 {
		t.Fatalf("half way: %v %v", *sh.LogE, ended)
	}
	// A target: an exponential path, then held, then done.
	target := started(EventTarget, "1")
	target.Price, target.Duration, target.Hold = d("1.21"), time.Minute, 30*time.Second
	if sh, _ = ShapeOf([]*Event{target}, t0.Add(30*time.Second)); math.Abs(sh.Pin-1.1) > 1e-12 {
		t.Fatalf("half way to 1.21: %v", sh.Pin)
	}
	if sh, ended = ShapeOf([]*Event{target}, t0.Add(80*time.Second)); sh.Pin != 1.21 || len(ended) != 0 {
		t.Fatalf("held: %v %v", sh.Pin, ended)
	}
	if _, ended = ShapeOf([]*Event{target}, t0.Add(90*time.Second)); len(ended) != 1 {
		t.Fatal("the hold did not end")
	}
	// A pause holds the price it started at; a trend and a volatility
	// change the model's own; a halt halts until ended.
	pause := started(EventPause, "0.97")
	trend := started(EventTrend, "1")
	trend.Mu = 0.2
	vol := started(EventVolatility, "1")
	vol.Factor, vol.Duration = 3, time.Minute
	halt := started(EventHalt, "1")
	sh, ended = ShapeOf([]*Event{pause, trend, vol, halt}, t0.Add(2*time.Minute))
	if sh.Pin != 0.97 || *sh.Mu != 0.2 || sh.Vol != 3 || !sh.Halted || len(ended) != 1 || ended[0] != vol {
		t.Fatalf("shape %+v ended %v", sh, ended)
	}
}

func TestOneOperatorsLimits(t *testing.T) {
	t0 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	at := func(minutes int, move float64) Spend {
		return Spend{At: t0.Add(time.Duration(minutes) * time.Minute), Move: move}
	}
	if NeedsApproval(at(0, 0.3), nil) || !NeedsApproval(at(0, -0.31), nil) {
		t.Fatal("a single move")
	}
	// The hours that hold a move, by when the moves take effect: 0.2 at
	// −50 minutes and 0.25 at +40 are never in one hour with each other,
	// but each is with a move at 0; the ones at −70 and +75 are not.
	others := []Spend{at(-50, 0.2), at(40, -0.25), at(-70, 0.3), at(75, 0.3)}
	if NeedsApproval(at(0, 0.25), others) || !NeedsApproval(at(0, 0.26), others) {
		t.Fatal("the busiest hour holds 0.25 besides")
	}
	if !NeedsApproval(at(-10, 0.1), others) {
		t.Fatal("at −10: 0.3 at −70 and 0.2 at −50 with it")
	}
	// The day's turnover: at most double or half within an hour.
	vol := func(minutes int, v float64) Spend {
		return Spend{At: t0.Add(time.Duration(minutes) * time.Minute), Volume: v}
	}
	if NeedsApproval(vol(0, math.Log(1.5)), []Spend{vol(-90, math.Log(2))}) || !NeedsApproval(vol(0, math.Log(1.5)), []Spend{vol(-30, math.Log(1.5))}) {
		t.Fatal("the turnover's budget")
	}
}

func TestMoves(t *testing.T) {
	for _, c := range []struct {
		e    Event
		want float64
	}{
		{Event{Type: EventJump, Size: -0.2}, -0.2},
		{Event{Type: EventTarget, Price: d("1.3")}, 0.3},
		{Event{Type: EventTarget, Price: d("1.3"), FromP: d("1.3")}, 0},
		{Event{Type: EventTrend, Mu: 0.1}, math.Expm1(0.1)},                            // a day without an end
		{Event{Type: EventTrend, Mu: 0.1, Duration: 12 * time.Hour}, math.Expm1(0.05)}, // half a day
		{Event{Type: EventVolatility, Factor: 5}, 0.02 * 4},
		{Event{Type: EventVolatility, Factor: 5, Duration: 6 * time.Hour}, 0.02 * 4 * 0.5},
		{Event{Type: EventPause}, 0},
	} {
		if got := c.e.Move(1, 0.02); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("%+v: %v, want %v", c.e, got, c.want)
		}
	}
	p := DefaultParams()
	q := p
	q.P0, q.Floor, q.MaxMinuteMove = 1.1, 1.05, 0.05
	if got := ParamsMove(p, q, 1); math.Abs(got-(0.02+0.1+0.05)) > 1e-9 {
		t.Fatalf("settings move %v", got)
	}
	q = p
	q.DailyVolume = 4_000_000
	if got := VolumeMove(p, q); math.Abs(got-math.Ln2) > 1e-9 {
		t.Fatalf("turnover %v", got)
	}
	q.DailyVolume = 0
	if got := VolumeMove(p, q); got <= SoloVolume {
		t.Fatalf("stopping the takers %v", got)
	}
}

func TestEventValidate(t *testing.T) {
	ok := Event{Type: EventJump, Size: 0.1, CreatedBy: "ops", Reason: "test"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{
		{Type: EventJump, Size: 0, CreatedBy: "ops", Reason: "test"},
		{Type: EventJump, Size: 1.01, CreatedBy: "ops", ApprovedBy: "ops2", Reason: "beyond the hard cap"},
		{Type: EventJump, Size: 0.1, Duration: 11 * time.Minute, CreatedBy: "ops", Reason: "test"},
		{Type: EventTarget, CreatedBy: "ops", Reason: "test"},
		{Type: EventVolatility, Factor: 0, CreatedBy: "ops", Reason: "test"},
		{Type: "BOOM", CreatedBy: "ops", Reason: "test"},
		{Type: EventPause, Reason: "test"},
	} {
		if err := e.Validate(); err == nil {
			t.Errorf("%+v passed", e)
		}
	}
}

// An event moves the price past the minute guard, which then starts again
// from the event's price instead of pulling it back.
func TestAnEventPassesTheMinuteGuard(t *testing.T) {
	m := NewModel(quiet(), State{}, 1)
	m.Step(t0, 60000, 3000, Shape{})
	logE := math.Log(1.2)
	if p, g := m.Step(t0.Add(time.Second), 60000, 3000, Shape{LogE: &logE, Moving: true}); math.Abs(p-1.2) > 1e-9 || g != GuardNone {
		t.Fatalf("the jump: %v %q", p, g)
	}
	if p, g := m.Step(t0.Add(2*time.Second), 60000, 3000, Shape{}); math.Abs(p-1.2) > 1e-9 || g != GuardNone {
		t.Fatalf("after the jump: %v %q", p, g)
	}
	// A pin, and the model goes on from it.
	if p, _ := m.Step(t0.Add(3*time.Second), 60000, 3000, Shape{Pin: 0.8, Moving: true}); p != 0.8 {
		t.Fatalf("pinned: %v", p)
	}
	m.Hold(0.8)
	if p, _ := m.Step(t0.Add(4*time.Second), 60000, 3000, Shape{}); math.Abs(p-0.8) > 1e-9 {
		t.Fatalf("after the pin: %v", p)
	}
}

func TestQuoteCenter(t *testing.T) {
	a := Anchors{Lo: 1, Hi: 1}
	for _, c := range []struct {
		target, band, center float64
		walking              bool
	}{
		{1.05, 0.1, 1.05, false}, // inside: the target
		{1.35, 0.1, 1.07, true},  // beyond: 70% into the band
		{0.5, 0.1, 0.93, true},
		{1.35, 0, 1.35, false}, // no band
	} {
		got, walking := QuoteCenter(c.target, a, c.band)
		if math.Abs(got-c.center) > 1e-9 || walking != c.walking {
			t.Fatalf("%+v: %v %v", c, got, walking)
		}
	}
	// Reads that moved: the band both of them allow.
	if got, _ := QuoteCenter(1.35, Anchors{Lo: 1, Hi: 1.05}, 0.1); math.Abs(got-1.07) > 1e-9 {
		t.Fatalf("up from the lower read: %v", got)
	}
	if got, _ := QuoteCenter(0.5, Anchors{Lo: 1, Hi: 1.05}, 0.1); math.Abs(got-1.05*0.93) > 1e-9 {
		t.Fatalf("down from the higher read: %v", got)
	}
	if got, walking := QuoteCenter(1, Anchors{}, 0.1); got != 1 || walking {
		t.Fatal("no anchor, no band")
	}
}

func TestInBand(t *testing.T) {
	prices := []decimal.Decimal{d("0.89"), d("0.95"), d("1.0999"), d("1.1"), d("1.11")}
	got := InBand(prices, Anchors{Lo: 1, Hi: 1}, 0.1)
	if len(got) != 2 || !got[0].Equal(d("0.95")) || !got[1].Equal(d("1.0999")) {
		t.Fatalf("in band: %v", got)
	}
	if got := InBand(prices, Anchors{Lo: 1, Hi: 1}, 0); len(got) != len(prices) {
		t.Fatalf("no band: %v", got)
	}
}

func TestTheBucketsShareAndReturns(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	b := Bucket{Rate: 4, Burst: 4}
	n := 0
	for b.TakeLeaving(now, 1) {
		n++
	}
	if n != 3 || !b.Take(now) || b.Take(now) {
		t.Fatalf("the makers took %d of 4, leaving one", n)
	}
	b.Return()
	if !b.Take(now) {
		t.Fatal("a returned token was not there")
	}
	if one := (Bucket{Rate: 1, Burst: 1}); !one.TakeLeaving(now, 5) {
		t.Fatal("a burst of one: the makers never take")
	}
}

func TestBackoff(t *testing.T) {
	var waits []time.Duration
	w := time.Duration(0)
	for range 8 {
		w = Backoff(w)
		waits = append(waits, w)
	}
	if waits[0] != time.Second || waits[3] != 8*time.Second || waits[7] != time.Minute {
		t.Fatalf("waits %v", waits)
	}
}

func TestRebase(t *testing.T) {
	m := NewModel(DefaultParams(), State{}, 7)
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	m.Step(now, 60000, 3000, Shape{})
	m.Rebase(1.5)
	p, guard := m.Step(now.Add(time.Second), 60000, 3000, Shape{})
	if math.Abs(p/1.5-1) > 0.01 || guard != GuardNone {
		t.Fatalf("rebased at 1.5: %v %q", p, guard)
	}
}

// Stored settings beyond the hard limits run at them; what was changed is
// named.
func TestClampBringsSettingsWithinTheHardLimits(t *testing.T) {
	p := DefaultParams()
	p.OrdersPerSecond, p.DailyVolume, p.Ceiling, p.MaxMinuteMove = 500, 1e9, 5e6, 0.2
	got, changed := p.Clamp()
	if err := got.Validate(); err != nil || len(changed) != 4 || got.OrdersPerSecond != MaxOrdersPerSecond ||
		got.DailyVolume != MaxDailyVolume || got.Ceiling != HardCeiling || got.MaxMinuteMove != MaxMinuteMove {
		t.Fatalf("%+v %v %v", got, changed, err)
	}
	if _, changed := DefaultParams().Clamp(); len(changed) != 0 {
		t.Fatalf("the defaults changed: %v", changed)
	}
}
