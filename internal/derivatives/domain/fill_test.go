package domain

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var btcPerp = Contract{
	Symbol: "BTC-USDT-PERP", Base: "BTC", Quote: "USDT", TickSize: d("0.1"), LotSize: d("0.001"),
	MinQuantity: d("0.001"), MaxQuantity: d("100"), MinNotional: d("5"), PriceBand: d("0.05"),
	Tiers: []RiskTier{
		{MaxNotional: d("50000"), MaxLeverage: 50, MMR: d("0.004")},
		{MaxNotional: d("250000"), MaxLeverage: 20, MMR: d("0.01")},
	},
	FundingIntervalHours: 8, MakerFeeRate: d("0.0002"), TakerFeeRate: d("0.0005"), Status: StatusTrading, QuoteDecimals: 6,
}

var t0 = time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)

func order(t *testing.T, side Side, ps PositionSide, price, qty string, reduceOnly bool, s Settings) Order {
	t.Helper()
	req := Request{UserID: "u1", Symbol: btcPerp.Symbol, Side: side, PositionSide: ps, Type: Limit, Qty: d(qty), ReduceOnly: reduceOnly}
	if price != "" {
		req.Price = d(price)
	}
	o, err := NewOrder(strings.ReplaceAll("o-"+string(side)+price+qty, ".", "_"), req, btcPerp, s, d("60000"), t0)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func settings(mode PositionMode, margin MarginMode, lev int32) Settings {
	return Settings{UserID: "u1", Symbol: btcPerp.Symbol, PositionMode: mode, MarginMode: margin, Leverage: lev}
}

// moves renders a plan's moves for comparison.
func moves(p FillPlan) []string {
	var out []string
	for _, m := range p.Moves {
		s := m.Type + " " + m.Amount.String()
		if m.Frozen {
			s += " frozen"
		}
		if m.Limit != nil {
			s += " limit " + m.Limit.String()
		}
		if m.Partial {
			s += " partial"
		}
		if m.EntryType != "" && m.EntryType != EntryRealizedPnL {
			s += " " + m.EntryType
		}
		out = append(out, s)
	}
	return out
}

func same(t *testing.T, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("moves\n got %q\nwant %q", got, want)
	}
}

func outcomesFor(p FillPlan) []Outcome {
	out := make([]Outcome, len(p.Moves))
	for i := range out {
		out[i] = Outcome{User: decimal.Zero, Insurance: decimal.Zero, Waived: decimal.Zero}
	}
	return out
}

func TestOpeningReservesAndFillsIntoMargin(t *testing.T) {
	s := settings(OneWay, Cross, 10)
	o := order(t, Buy, SideBoth, "60000", "0.5", false, s)
	// 60000 x 0.001 / 10 = 6 margin and 60000 x 0.001 x 0.0005 = 0.03 fee per lot.
	if !o.MarginPerLot.Equal(d("6")) || !o.FeePerLot.Equal(d("0.03")) || o.FreezeState != FreezePending {
		t.Fatalf("reservation %s %s %s", o.MarginPerLot, o.FeePerLot, o.FreezeState)
	}
	if !o.Unreleased().Equal(d("3015")) {
		t.Fatalf("reserved %s", o.Unreleased())
	}
	// A maker fill of 0.2 at 59990: fee 0.2 x 59990 x 0.0002 = 2.3996 of
	// the 6 reserved for 200 lots.
	plan, err := PlanFill(btcPerp, o, nil, FillInput{TradeID: "t1", Price: d("59990"), Qty: d("0.2"), Maker: true, ExecutedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	same(t, moves(plan), []string{"FEE 2.3996 frozen", "UNFREEZE 3.6004"})
	if err := plan.Apply(outcomesFor(plan)); err != nil {
		t.Fatal(err)
	}
	pos := plan.Positions[0]
	if pos.Reason != ChangeOpen || !pos.Position.Qty.Equal(d("0.2")) || !pos.Position.EntryCost.Equal(d("11998")) ||
		!pos.Position.Margin.Equal(d("1200")) || pos.Position.MarginMode != Cross || !pos.Position.Fees.Equal(d("2.3996")) {
		t.Fatalf("position %+v", pos)
	}
	if !plan.Order.Consumed.Equal(d("0.2")) || !plan.Order.Fee.Equal(d("2.3996")) {
		t.Fatalf("order %+v", plan.Order)
	}
}

func TestClosingACrossLongRealizesItsShare(t *testing.T) {
	long := Position{
		ID: "p1", UserID: "u1", Symbol: btcPerp.Symbol, Side: SideBoth, Qty: d("0.3"), EntryCost: d("18000.3"),
		Margin: d("1800"), MarginMode: Cross, Leverage: 10,
	}
	o := order(t, Sell, SideBoth, "61000", "0.1", true, settings(OneWay, Cross, 10))
	if o.Reserving() || o.FreezeState != FreezeDone {
		t.Fatal("a reduce-only order reserves nothing")
	}
	plan, err := PlanFill(btcPerp, o, map[PositionSide]Position{SideBoth: long}, FillInput{TradeID: "t2", Price: d("61000"), Qty: d("0.1")})
	if err != nil {
		t.Fatal(err)
	}
	// A third of the cost (6000.1) and margin (600); proceeds 6100: profit
	// 99.9; taker fee 3.05 from available.
	same(t, moves(plan), []string{"UNFREEZE 600", "PROFIT 99.9", "FEE 3.05"})
	if err := plan.Apply(outcomesFor(plan)); err != nil {
		t.Fatal(err)
	}
	pos := plan.Positions[0].Position
	if plan.Positions[0].Reason != ChangeReduce || !pos.Qty.Equal(d("0.2")) || !pos.EntryCost.Equal(d("12000.2")) || !pos.Margin.Equal(d("1200")) ||
		!pos.RealizedPnL.Equal(d("99.9")) || !plan.Fill.ClosedQty.Equal(d("0.1")) || !plan.Fill.RealizedPnL.Equal(d("99.9")) {
		t.Fatalf("after closing a third: %+v fill %+v", pos, plan.Fill)
	}
	if !pos.EntryPrice().Equal(d("60001")) {
		t.Fatalf("entry price %s", pos.EntryPrice())
	}
}

func TestAnIsolatedShortLosesAtMostItsMargin(t *testing.T) {
	short := Position{
		ID: "p2", UserID: "u1", Symbol: btcPerp.Symbol, Side: SideShort, Qty: d("-0.1"), EntryCost: d("6000"),
		Margin: d("120"), MarginMode: Isolated, Leverage: 50,
	}
	// Buying back at 61500 loses 150 on a margin of 120: the insurance fund
	// pays 30 and the fee is waived.
	o := order(t, Buy, SideShort, "61500", "0.1", false, settings(Hedge, Isolated, 50))
	if !o.Closing() || o.Reserving() {
		t.Fatal("BUY SHORT only closes")
	}
	plan, err := PlanFill(btcPerp, o, map[PositionSide]Position{SideShort: short}, FillInput{TradeID: "t3", Price: d("61500"), Qty: d("0.1")})
	if err != nil {
		t.Fatal(err)
	}
	same(t, moves(plan), []string{"LOSS 150 frozen limit 120", "FEE 3.075 frozen limit 0"})
	out := outcomesFor(plan)
	out[0] = Outcome{User: d("-120"), Insurance: d("30"), Waived: decimal.Zero}
	out[1] = Outcome{User: decimal.Zero, Insurance: decimal.Zero, Waived: d("3.075")}
	if err := plan.Apply(out); err != nil {
		t.Fatal(err)
	}
	pos := plan.Positions[0].Position
	if plan.Positions[0].Reason != ChangeClose || !pos.Qty.IsZero() || !pos.EntryCost.IsZero() || !pos.Margin.IsZero() ||
		!plan.Fill.Insurance.Equal(d("30")) || !plan.Fill.Fee.IsZero() || !plan.Fill.FeeWaived.Equal(d("3.075")) || !plan.Fill.RealizedPnL.Equal(d("-150")) {
		t.Fatalf("closed: %+v fill %+v", pos, plan.Fill)
	}

	// Liquidated instead at 60600: loss 60, fee 3.03, the other 56.97 of
	// the margin goes to the insurance fund.
	o.Kind = KindLiquidation
	plan, err = PlanFill(btcPerp, o, map[PositionSide]Position{SideShort: short}, FillInput{
		TradeID: "t4", Price: d("60600"), Qty: d("0.1"), Liquidation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	same(t, moves(plan), []string{"LOSS 60 frozen limit 120 LIQUIDATION_SETTLE", "FEE 3.03 frozen limit 60", "INSURANCE 56.97 frozen"})
}

func TestAOneWayOrderTurnsThePositionAround(t *testing.T) {
	long := Position{ID: "p3", Qty: d("0.1"), EntryCost: d("6000"), Margin: d("600"), MarginMode: Cross, Leverage: 10}
	o := order(t, Sell, SideBoth, "60500", "0.3", false, settings(OneWay, Cross, 10))
	// Reserved per lot: 6.05 margin, 0.03025 fee.
	plan, err := PlanFill(btcPerp, o, map[PositionSide]Position{SideBoth: long}, FillInput{TradeID: "t5", Price: d("60500"), Qty: d("0.3")})
	if err != nil {
		t.Fatal(err)
	}
	// Fee 9.075 from its reservation of 9.075; the margin reserved for the
	// closed 0.1 (605) is freed with the position's 600; profit 50.
	same(t, moves(plan), []string{"FEE 9.075 frozen", "UNFREEZE 1205", "PROFIT 50"})
	if err := plan.Apply(outcomesFor(plan)); err != nil {
		t.Fatal(err)
	}
	pos := plan.Positions[0]
	if len(plan.Positions) != 1 || pos.Reason != ChangeFlip || !pos.Position.Qty.Equal(d("-0.2")) || !pos.Position.EntryCost.Equal(d("12100")) ||
		!pos.Position.Margin.Equal(d("1210")) {
		t.Fatalf("flipped %+v", pos)
	}
}

func TestAClosingOrderBeyondItsPositionOpensTheOtherSide(t *testing.T) {
	// Hedge mode: SELL LONG 0.2 while the long is down to 0.1 (liquidated
	// meanwhile): 0.1 closes, 0.1 opens a short with margin from the
	// available balance.
	long := Position{ID: "p4", Side: SideLong, Qty: d("0.1"), EntryCost: d("6000"), Margin: d("600"), MarginMode: Cross, Leverage: 10}
	o := order(t, Sell, SideLong, "60000", "0.2", false, settings(Hedge, Cross, 10))
	plan, err := PlanFill(btcPerp, o, map[PositionSide]Position{SideLong: long}, FillInput{TradeID: "t6", Price: d("60000"), Qty: d("0.2"), Maker: true})
	if err != nil {
		t.Fatal(err)
	}
	same(t, moves(plan), []string{"UNFREEZE 600", "FEE 2.4", "FREEZE 600 partial"})
	out := outcomesFor(plan)
	out[2].Waived = d("100") // only 500 was available
	if err := plan.Apply(out); err != nil {
		t.Fatal(err)
	}
	if len(plan.Positions) != 2 || plan.Positions[0].Position.Side != SideLong || plan.Positions[0].Reason != ChangeClose ||
		plan.Positions[1].Position.Side != SideShort || plan.Positions[1].Reason != ChangeOpen ||
		!plan.Positions[1].Position.Margin.Equal(d("500")) || !plan.Positions[1].Position.Qty.Equal(d("-0.1")) {
		t.Fatalf("positions %+v", plan.Positions)
	}
	// The maker fee of 2.4 comes out of the available balance.
	if !plan.Fill.Fee.Equal(d("2.4")) {
		t.Fatalf("fee %s", plan.Fill.Fee)
	}
}

// ledger is a toy FUTURES ledger with the ledger's move semantics.
type ledger struct {
	available, frozen map[string]decimal.Decimal
	pnlClearing       decimal.Decimal
	feeRevenue        decimal.Decimal
	insurance         decimal.Decimal
}

func (l *ledger) apply(t *testing.T, user string, ms []Move) []Outcome {
	t.Helper()
	out := make([]Outcome, len(ms))
	for i, m := range ms {
		out[i] = Outcome{User: decimal.Zero, Insurance: decimal.Zero, Waived: decimal.Zero}
		bal := l.available
		if m.Frozen {
			bal = l.frozen
		}
		take := func() decimal.Decimal {
			v := decimal.Min(m.Amount, decimal.Max(bal[user], decimal.Zero))
			if m.Limit != nil {
				v = decimal.Min(v, *m.Limit)
			}
			return v
		}
		switch m.Type {
		case MoveUnfreeze:
			l.frozen[user], l.available[user] = l.frozen[user].Sub(m.Amount), l.available[user].Add(m.Amount)
		case MoveFreeze:
			v := decimal.Min(m.Amount, l.available[user])
			out[i].Waived = m.Amount.Sub(v)
			l.available[user], l.frozen[user] = l.available[user].Sub(v), l.frozen[user].Add(v)
		case MoveFee:
			v := take()
			out[i].User, out[i].Waived = v.Neg(), m.Amount.Sub(v)
			bal[user] = bal[user].Sub(v)
			l.feeRevenue = l.feeRevenue.Add(v)
		case MoveProfit:
			bal[user] = bal[user].Add(m.Amount)
			l.pnlClearing = l.pnlClearing.Sub(m.Amount)
		case MoveLoss:
			v := take()
			out[i].User, out[i].Insurance = v.Neg(), m.Amount.Sub(v)
			bal[user] = bal[user].Sub(v)
			l.insurance = l.insurance.Sub(out[i].Insurance)
			l.pnlClearing = l.pnlClearing.Add(m.Amount)
		case MoveInsurance:
			bal[user] = bal[user].Sub(m.Amount)
			l.insurance = l.insurance.Add(m.Amount)
		default:
			t.Fatalf("move %s", m.Type)
		}
		if l.available[user].IsNegative() || l.frozen[user].IsNegative() {
			t.Fatalf("%s overdrawn by %+v: %s/%s", user, m, l.available[user], l.frozen[user])
		}
	}
	return out
}

// TestTradingKeepsTheBooksExact trades at random between a few users in
// both modes and checks, after every fill, invariant 6 (PNL_CLEARING +
// long cost − short cost = 0, long quantity = short quantity) and that
// each user's frozen balance is exactly the reservations of their orders
// plus the margin of their positions.
func TestTradingKeepsTheBooksExact(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11)) //nolint:gosec // a reproducible test sequence
	users := []string{"a", "b", "c", "d"}
	modes := map[string]Settings{
		"a": settings(OneWay, Cross, 10), "b": settings(OneWay, Isolated, 20),
		"c": settings(Hedge, Cross, 5), "d": settings(Hedge, Isolated, 50),
	}
	l := &ledger{available: map[string]decimal.Decimal{}, frozen: map[string]decimal.Decimal{}, insurance: d("1000000")}
	positions := map[string]map[PositionSide]Position{}
	for _, u := range users {
		l.available[u] = d("100000")
		positions[u] = map[PositionSide]Position{}
	}
	var orders []Order
	price := d("60000")
	fills, flips, closes := 0, 0, 0
	defer func() {
		if fills < 100 || flips == 0 || closes < 20 {
			t.Errorf("only %d fills, %d flips, %d closes", fills, flips, closes)
		}
	}()
	for step := range 400 {
		// Place an order for a random user.
		u := users[rng.IntN(len(users))]
		s := modes[u]
		s.UserID = u
		side := Buy
		if rng.IntN(2) == 0 {
			side = Sell
		}
		ps := SideBoth
		if s.PositionMode == Hedge {
			ps = SideLong
			if rng.IntN(2) == 0 {
				ps = SideShort
			}
		}
		qty := decimal.NewFromInt(int64(rng.IntN(300) + 1)).Mul(btcPerp.LotSize)
		limit := price.Add(decimal.NewFromInt(int64(rng.IntN(200) - 100)).Mul(btcPerp.TickSize))
		req := Request{UserID: u, Symbol: btcPerp.Symbol, Side: side, PositionSide: ps, Type: Limit, Price: limit, Qty: qty}
		o, err := NewOrder(fmt.Sprintf("o%d", step), req, btcPerp, s, price, t0)
		if err != nil {
			t.Fatal(err)
		}
		if res := o.Unreleased(); res.IsPositive() {
			if l.available[u].LessThan(res) {
				continue
			}
			l.available[u], l.frozen[u] = l.available[u].Sub(res), l.frozen[u].Add(res)
		}
		orders = append(orders, o)
		n := len(orders) - 1

		// Match it against the resting orders of other users on the other
		// side, at the resting price when the limits cross. The engine's
		// part happens at once (fills counted, statuses); the fills reach
		// the positions after the orders' releases, as trade events may
		// come after order events.
		type queued struct {
			order    int
			maker    bool
			trade    string
			price, q decimal.Decimal
		}
		var queue []queued
		for i := n - 1; i >= 0 && !orders[n].Qty.Sub(orders[n].Filled).IsZero(); i-- {
			m, o := orders[i], orders[n]
			if m.UserID == o.UserID || m.Side == o.Side || !m.Status.Active() {
				continue
			}
			if (o.Side == Buy && o.Price.LessThan(m.Price)) || (o.Side == Sell && o.Price.GreaterThan(m.Price)) {
				continue
			}
			q := decimal.Min(o.Qty.Sub(o.Filled), m.Qty.Sub(m.Filled))
			trade := fmt.Sprintf("t%d-%d", step, i)
			for _, x := range []struct {
				idx   int
				maker bool
			}{{i, true}, {n, false}} {
				ord := &orders[x.idx]
				ord.Filled = ord.Filled.Add(q)
				ord.Status = StatusPartiallyFilled
				if ord.Filled.Equal(ord.Qty) {
					ord.Status = StatusFilled
				}
				queue = append(queue, queued{order: x.idx, maker: x.maker, trade: trade, price: m.Price, q: q})
			}
			price = m.Price
		}
		// Now and then an order finishes: its unfilled reservation is
		// released.
		for i := range orders {
			if orders[i].Status.Active() && rng.IntN(8) == 0 {
				orders[i].Status = StatusCanceled
			}
			if orders[i].Status.Terminal() && !orders[i].Released {
				if r := orders[i].ToRelease(); r.IsPositive() {
					l.apply(t, orders[i].UserID, []Move{{Type: MoveUnfreeze, Amount: r}})
				}
				orders[i].Released = true
			}
		}
		checkBooks(t, step, l, positions, orders)
		for _, f := range queue {
			ord := orders[f.order]
			plan, err := PlanFill(btcPerp, ord, positions[ord.UserID], FillInput{TradeID: f.trade, Price: f.price, Qty: f.q, Maker: f.maker, ExecutedAt: t0})
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.Apply(l.apply(t, ord.UserID, plan.Moves)); err != nil {
				t.Fatal(err)
			}
			for _, c := range plan.Positions {
				positions[ord.UserID][c.Position.Side] = c.Position
				switch c.Reason {
				case ChangeFlip:
					flips++
				case ChangeClose, ChangeReduce:
					closes++
				}
			}
			fills++
			orders[f.order] = plan.Order
		}
		checkBooks(t, step, l, positions, orders)
	}
}

func checkBooks(t *testing.T, step int, l *ledger, positions map[string]map[PositionSide]Position, orders []Order) {
	t.Helper()
	long, short, cost := decimal.Zero, decimal.Zero, l.pnlClearing
	for u, held := range positions {
		expected := decimal.Zero
		for _, p := range held {
			if p.Qty.IsPositive() {
				long, cost = long.Add(p.Qty), cost.Add(p.EntryCost)
			} else {
				short, cost = short.Add(p.Qty.Neg()), cost.Sub(p.EntryCost)
			}
			if p.Margin.IsNegative() || (p.Qty.IsZero() && (!p.EntryCost.IsZero() || !p.Margin.IsZero())) {
				t.Fatalf("step %d: position %+v", step, p)
			}
			expected = expected.Add(p.Margin)
		}
		for _, o := range orders {
			if o.UserID == u {
				expected = expected.Add(o.Unreleased())
			}
		}
		if !expected.Equal(l.frozen[u]) {
			t.Fatalf("step %d: %s frozen %s, positions and orders account for %s", step, u, l.frozen[u], expected)
		}
	}
	if !long.Equal(short) || !cost.IsZero() {
		t.Fatalf("step %d: invariant 6: long %s short %s, PNL_CLEARING + long cost − short cost = %s", step, long, short, cost)
	}
}
