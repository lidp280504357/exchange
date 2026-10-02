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
	if p, _ := m.Step(t0, 60000, 3000); math.Abs(p-1) > 1e-12 {
		t.Fatalf("anchored at %v", p)
	}
	// Both up 1%: ASTRA up about 1% (beta 1, half each).
	p, g := m.Step(t0.Add(250*time.Millisecond), 60600, 3030)
	if g != GuardNone || math.Abs(p-1.01) > 1e-9 {
		t.Fatalf("both up 1%%: %v %q", p, g)
	}
	// BTC up 2%, ETH flat: about +1%, as a weighted log return.
	p, _ = m.Step(t0.Add(500*time.Millisecond), 61200, 3000)
	if want := math.Exp(0.5 * math.Log(1.02)); math.Abs(p-want) > 1e-9 {
		t.Fatalf("BTC up 2%%: %v, want %v", p, want)
	}
	// Prices that are not fresh keep the factor.
	if q, _ := m.Step(t0.Add(750*time.Millisecond), 0, 0); q != p {
		t.Fatalf("stale prices moved the target: %v -> %v", p, q)
	}
	// Beta 0 does not follow.
	flat := quiet()
	flat.Beta = 0
	m = NewModel(flat, State{}, 1)
	m.Step(t0, 60000, 3000)
	if p, _ := m.Step(t0.Add(time.Second), 70000, 3500); p != 1 {
		t.Fatalf("beta 0: %v", p)
	}
}

func TestTheMinuteGuardBoundsTheSpeedNotTheLevel(t *testing.T) {
	m := NewModel(quiet(), State{}, 1)
	m.Step(t0, 60000, 3000)
	// BTC and ETH jump 10%: the target moves 3% in the minute, then
	// catches up a minute at a time.
	p, g := m.Step(t0.Add(time.Second), 66000, 3300)
	if g != GuardMinute || math.Abs(p-1.03) > 1e-9 {
		t.Fatalf("the jump: %v %q", p, g)
	}
	now := t0.Add(time.Second)
	for i := 0; i < 5; i++ {
		now = now.Add(61 * time.Second)
		p, _ = m.Step(now, 66000, 3300)
	}
	if math.Abs(p-1.1) > 1e-9 {
		t.Fatalf("five minutes on: %v, want 1.1", p)
	}
}

func TestTheFloorAndCeilingHold(t *testing.T) {
	p := quiet()
	p.P0, p.Floor, p.Ceiling, p.MaxMinuteMove = 1, 0.99, 1.005, 0.5
	m := NewModel(p, State{}, 1)
	m.Step(t0, 60000, 3000)
	if got, g := m.Step(t0.Add(time.Second), 66000, 3300); got != 1.005 || g != GuardCeil {
		t.Fatalf("ceiling: %v %q", got, g)
	}
	if got, g := m.Step(t0.Add(2*time.Second), 54000, 2700); got != 0.99 || g != GuardFloor {
		t.Fatalf("floor: %v %q", got, g)
	}
}

// The same seed, or the same saved state, gives the same path.
func TestThePathRepeatsFromItsSeedOrItsState(t *testing.T) {
	run := func(m *Model, from time.Time, n int) []float64 {
		var out []float64
		for i := range n {
			p, _ := m.Step(from.Add(time.Duration(i+1)*250*time.Millisecond), 60000, 3000)
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
	m.Step(t0, 60000, 3000)
	m.Step(t0.Add(time.Second), 60600, 3030)
	m.Reanchor(60600, 3030)
	if math.Abs(m.Params.P0-1.01) > 1e-9 {
		t.Fatalf("P0 %v", m.Params.P0)
	}
	if p, _ := m.Step(t0.Add(2*time.Second), 60600, 3030); math.Abs(p-1.01) > 1e-9 {
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

func TestArrivalsMakeTheDaysTurnover(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // a repeatable test
	p := DefaultParams()               // 2,000,000 a day in orders of 400: 5,000 orders
	n, now := 0, t0.Truncate(24*time.Hour)
	for i := 0; i < 86400*4; i++ {
		now = now.Add(250 * time.Millisecond)
		n += Arrivals(rng, p, 250*time.Millisecond, now)
	}
	if n < 4700 || n > 5300 {
		t.Fatalf("%d orders in a day, want about 5000", n)
	}
	p.DailyVolume = 0
	if Arrivals(rng, p, time.Second, now) != 0 {
		t.Fatal("orders without a turnover")
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
}
