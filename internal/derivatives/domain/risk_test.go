package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestMarginStatesAndLiquidationOrders(t *testing.T) {
	// An isolated long of 1 at 60000 with 1200 of margin (50x): its
	// maintenance margin at m is m x 1% less tier 2's amount, 50000 x
	// (1% − 0.4%) = 300, so it reaches it near 59090.91, not at 59393.94 as
	// with the rate alone (which jumps from 0.4% at 50000).
	long := Position{
		UserID: "u1", Symbol: btcPerp.Symbol, Side: SideBoth, Qty: d("1"), EntryCost: d("60000"), Margin: d("1200"),
		MarginMode: Isolated, Leverage: 50,
	}
	for _, c := range []struct {
		mark string
		want MarginState
	}{
		{"60000", MarginHealthy},
		{"59400", MarginHealthy},   // balance 600 > 1.2 x 294
		{"59100", MarginWarning},   // 300 <= 1.2 x 291
		{"59090", MarginLiquidate}, // 290 <= 290.9
	} {
		mark := d(c.mark)
		if got := State(long.MarginBalance(mark), long.MaintenanceMargin(btcPerp, mark)); got != c.want {
			t.Errorf("at %s: %v, want %v", c.mark, got, c.want)
		}
	}
	// Bankruptcy at (60000 − 1200) / 1 = 58800; the liquidation sell goes
	// 0.5% below it.
	o := LiquidationOrder("l1", btcPerp, long, long.BankruptcyPrice(), time.Now())
	if o.Side != Sell || !o.Price.Equal(d("58506")) || o.TimeInForce != IOC || o.Kind != KindLiquidation || o.Reserving() || !o.ReduceOnly {
		t.Fatalf("liquidation order %+v", o)
	}
	short := Position{UserID: "u2", Side: SideShort, Qty: d("-1"), EntryCost: d("60000"), Margin: d("1200"), MarginMode: Isolated}
	if o := LiquidationOrder("l2", btcPerp, short, short.BankruptcyPrice(), time.Now()); o.Side != Buy || !o.Price.Equal(d("61506")) || o.ReduceOnly {
		t.Fatalf("liquidation buy %+v", o)
	}
	if got := ADLPrice(btcPerp, long, d("59000")); !got.Equal(d("58800")) {
		t.Fatalf("ADL price %s", got)
	}
}

func TestTheADLQueue(t *testing.T) {
	liquidated := Position{UserID: "u0", Qty: d("-2"), EntryCost: d("120000"), Margin: d("2400")}
	mark := d("61000")
	queue := ADLQueue(liquidated, []Position{
		{UserID: "a", Qty: d("1"), EntryCost: d("59000"), Margin: d("5900")},  // +2000, 10x
		{UserID: "b", Qty: d("1"), EntryCost: d("60000"), Margin: d("1200")},  // +1000, 50x
		{UserID: "c", Qty: d("1"), EntryCost: d("62000"), Margin: d("6200")},  // at a loss
		{UserID: "d", Qty: d("-1"), EntryCost: d("60000"), Margin: d("6000")}, // same side
		{UserID: "u0", Qty: d("1"), EntryCost: d("60000"), Margin: d("6000")}, // the same user
		{UserID: "e", Qty: d("1"), EntryCost: d("50000"), Margin: d("5000"), Liquidating: true},
	}, mark)
	var order []string
	for _, p := range queue {
		order = append(order, p.UserID)
	}
	// b: 1000/60000 x 61000/2200 = 0.462; a: 2000/59000 x 61000/7900 = 0.262; c negative.
	if len(order) != 3 || order[0] != "b" || order[1] != "a" || order[2] != "c" {
		t.Fatalf("queue %v", order)
	}
}

func TestConditionalOrders(t *testing.T) {
	long := Position{Side: SideBoth, Qty: d("0.5"), EntryCost: d("30000")}
	now := time.Now()
	tp, err := NewConditional("c1", ConditionalRequest{UserID: "u1", Kind: TakeProfit, TriggerPrice: d("62000")}, btcPerp, long, d("60000"), now)
	if err != nil || tp.Side != Sell || tp.TriggerBy != TriggerMark || tp.OrderType != Market {
		t.Fatalf("take-profit %+v %v", tp, err)
	}
	if tp.Triggered(d("61999.9")) || !tp.Triggered(d("62000")) {
		t.Fatal("a long's take-profit triggers at or above")
	}
	sl, err := NewConditional("c2", ConditionalRequest{UserID: "u1", Kind: StopLoss, TriggerPrice: d("58000"), Qty: d("0.2")}, btcPerp, long, d("60000"), now)
	if err != nil || sl.Triggered(d("58000.1")) || !sl.Triggered(d("58000")) {
		t.Fatalf("stop-loss %+v %v", sl, err)
	}
	if r := sl.OrderRequest(long); !r.Qty.Equal(d("0.2")) || !r.ReduceOnly || r.Side != Sell || r.Type != Market {
		t.Fatalf("its order %+v", r)
	}
	if _, err := NewConditional("c3", ConditionalRequest{UserID: "u1", Kind: StopLoss, TriggerPrice: d("61000")}, btcPerp, long, d("60000"), now); code(err) != "DERIV_TRIGGER_IMMEDIATE" {
		t.Fatalf("a stop above the price: %v", err)
	}
	short := Position{Side: SideShort, Qty: d("-0.5")}
	stp, err := NewConditional("c4", ConditionalRequest{UserID: "u1", Kind: TakeProfit, TriggerPrice: d("58000"), OrderType: Limit, Price: d("58010")}, btcPerp, short, d("60000"), now)
	if err != nil || stp.Side != Buy || stp.Triggered(d("58000.1")) || !stp.Triggered(d("57999")) {
		t.Fatalf("a short's take-profit %+v %v", stp, err)
	}
	if r := stp.OrderRequest(Position{Side: SideShort, Qty: d("-0.3")}); !r.Qty.Equal(d("0.3")) || r.ReduceOnly || !r.Price.Equal(d("58010")) {
		t.Fatalf("closes what is left %+v", r)
	}
	if _, err := NewConditional("c5", ConditionalRequest{UserID: "u1", Kind: TakeProfit, TriggerPrice: d("62000")}, btcPerp, Position{}, d("60000"), now); code(err) != "DERIV_NO_POSITION" {
		t.Fatalf("no position: %v", err)
	}
}

// The deployed ladder since 2026-10-01 (deploy/instruments/test.json):
// 125x up to 50,000 at 0.4% maintenance, down to 2x above 50,000,000.
func TestTheLadderAt125x(t *testing.T) {
	c := btcPerp
	c.Tiers = []RiskTier{
		{MaxNotional: d("50000"), MaxLeverage: 125, MMR: d("0.004")},
		{MaxNotional: d("250000"), MaxLeverage: 100, MMR: d("0.005")},
		{MaxNotional: d("1000000"), MaxLeverage: 50, MMR: d("0.01")},
		{MaxNotional: d("5000000"), MaxLeverage: 20, MMR: d("0.025")},
		{MaxNotional: d("20000000"), MaxLeverage: 10, MMR: d("0.05")},
		{MaxNotional: d("50000000"), MaxLeverage: 5, MMR: d("0.1")},
		{MaxNotional: d("100000000"), MaxLeverage: 2, MMR: d("0.125")},
	}
	if c.MaxLeverage() != 125 || !c.MaxNotional(125).Equal(d("50000")) || !c.MaxNotional(100).Equal(d("250000")) || !c.MaxNotional(2).Equal(d("100000000")) {
		t.Fatalf("leverage %d, caps %s %s %s", c.MaxLeverage(), c.MaxNotional(125), c.MaxNotional(100), c.MaxNotional(2))
	}
	if !c.MMR(d("60000")).Equal(d("0.005")) || !c.MMR(d("200000000")).Equal(d("0.125")) {
		t.Fatalf("mmr %s %s", c.MMR(d("60000")), c.MMR(d("200000000")))
	}
	// An isolated long of 0.5 at 84000 at 125x: 336 of margin against 0.4%
	// maintenance leaves 0.4% of room: liquidation near 83662.65.
	long := Position{
		UserID: "u1", Symbol: c.Symbol, Side: SideBoth, Qty: d("0.5"), EntryCost: d("42000"), Margin: d("336"),
		MarginMode: Isolated, Leverage: 125,
	}
	if got := long.LiquidationPrice(c); !got.Equal(d("83662.65060241")) {
		t.Fatalf("liquidation price %s", got)
	}
	for _, s := range []struct {
		mark string
		want MarginState
	}{
		{"84000", MarginHealthy},
		{"83700", MarginWarning},   // balance 186 <= 1.2 x 167.4
		{"83660", MarginLiquidate}, // 166 <= 167.32
	} {
		mark := d(s.mark)
		if got := State(long.MarginBalance(mark), long.MaintenanceMargin(c, mark)); got != s.want {
			t.Errorf("at %s: %v, want %v", s.mark, got, s.want)
		}
	}
	// Bankruptcy at (42000 − 336) / 0.5 = 83328, 0.4% below the entry:
	// the liquidation sell still goes 0.5% under it.
	if o := LiquidationOrder("l1", c, long, long.BankruptcyPrice(), time.Now()); o.Side != Sell || !o.Price.Equal(d("82911.3")) || !o.ReduceOnly {
		t.Fatalf("liquidation order %+v", o)
	}
}

// The cross estimate puts the account's cross equity at the maintenance
// margin, the other positions at their marks (§11.7).
func TestCrossLiquidationPrice(t *testing.T) {
	c := btcPerp
	c.Tiers = []RiskTier{
		{MaxNotional: d("50000"), MaxLeverage: 125, MMR: d("0.004")},
		{MaxNotional: d("250000"), MaxLeverage: 100, MMR: d("0.005")},
		{MaxNotional: d("1000000"), MaxLeverage: 50, MMR: d("0.01")},
	}
	contracts := map[string]Contract{c.Symbol: c}
	mark := d("84000")
	// A cross long of 2 at 84101.3 (20x, 8412.06 of margin) with 1587.94
	// available: 10,000 in all. At x the equity 10000 + 2x − 168202.6
	// meets 2x × 0.5% − 50: x = 158152.6 / 1.99.
	long := Position{
		UserID: "u1", Symbol: c.Symbol, Side: SideBoth, Qty: d("2"), EntryCost: d("168202.6"), Margin: d("8412.06"),
		MarginMode: Cross, Leverage: 20,
	}
	marks := map[string]decimal.Decimal{c.Symbol: mark}
	equity, maintenance := CrossEquity(d("1587.94"), nil, []Position{long}, contracts, marks)
	// 168000 x 0.5% less tier 2's 50000 x (0.5% − 0.4%) = 50.
	if !equity.Equal(d("9797.4")) || !maintenance.Equal(d("790")) {
		t.Fatalf("equity %s, maintenance %s", equity, maintenance)
	}
	if got := CrossLiquidationPrice(c, long, mark, equity, decimal.Zero); !got.Equal(d("79473.66834171")) {
		t.Fatalf("long %s", got)
	}
	// A cross short of 1 at 60000 with 1000 of equity: 61050 / 1.005.
	short := Position{UserID: "u2", Symbol: c.Symbol, Side: SideBoth, Qty: d("-1"), EntryCost: d("60000"), Margin: d("600"), MarginMode: Cross}
	if got := CrossLiquidationPrice(c, short, d("60000"), d("1000"), decimal.Zero); !got.Equal(d("60746.26865672")) {
		t.Fatalf("short %s", got)
	}
	// With 12,000 of equity the long of 1 at 60000 falls below 50,000 of
	// notional before liquidation: tier 2 gives 48190.95, in tier 1, whose
	// rate decides it.
	one := Position{Symbol: c.Symbol, Side: SideBoth, Qty: d("1"), EntryCost: d("60000"), Margin: d("3000"), MarginMode: Cross}
	if got := CrossLiquidationPrice(c, one, d("60000"), d("12000"), decimal.Zero); !got.Equal(d("48192.77108434")) {
		t.Fatalf("across a tier %s", got)
	}
	// Another position's maintenance margin brings it closer; equity that
	// covers a fall to zero leaves none.
	if got := CrossLiquidationPrice(c, one, d("60000"), d("12000"), d("1000")); !got.GreaterThan(d("48192.77108434")) {
		t.Fatalf("with others %s", got)
	}
	if got := CrossLiquidationPrice(c, one, d("60000"), d("70000"), decimal.Zero); !got.IsZero() {
		t.Fatalf("covered %s", got)
	}
}

// The maintenance margin has no step at a tier boundary (Binance's
// cumulative maintenance amount): a cent past it costs about a cent's
// worth of the new rate, not the whole notional's.
func TestMaintenanceIsContinuousAcrossTiers(t *testing.T) {
	c := btcPerp
	c.Tiers = []RiskTier{
		{MaxNotional: d("50000"), MaxLeverage: 125, MMR: d("0.004")},
		{MaxNotional: d("250000"), MaxLeverage: 100, MMR: d("0.005")},
		{MaxNotional: d("1000000"), MaxLeverage: 50, MMR: d("0.01")},
		{MaxNotional: d("5000000"), MaxLeverage: 20, MMR: d("0.025")},
		{MaxNotional: d("20000000"), MaxLeverage: 10, MMR: d("0.05")},
		{MaxNotional: d("50000000"), MaxLeverage: 5, MMR: d("0.1")},
		{MaxNotional: d("100000000"), MaxLeverage: 2, MMR: d("0.125")},
	}
	cent := d("0.01")
	for _, tier := range c.Tiers[:len(c.Tiers)-1] {
		at, past := c.Maintenance(tier.MaxNotional), c.Maintenance(tier.MaxNotional.Add(cent))
		if jump := past.Sub(at); jump.IsNegative() || jump.GreaterThan(cent) {
			t.Errorf("at %s: %s, a cent past it %s", tier.MaxNotional, at, past)
		}
	}
	// 1,000,000 of notional: 1% less 50000 x 0.1% + 250000 x 0.5% = 1300.
	if got := c.Maintenance(d("1000000")); !got.Equal(d("8700")) {
		t.Fatalf("maintenance %s", got)
	}
}
