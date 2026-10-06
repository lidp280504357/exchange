package domain

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// btcUSD is an inverse (coin-margined) perpetual as coin-M design §2.1
// sets it: contracts of 100 dollars, settled in BTC (8 decimals), its
// risk tiers in BTC.
var btcUSD = Contract{
	Symbol: "BTC-USD-PERP", Base: "BTC", Quote: "USD", TickSize: d("0.1"), LotSize: d("1"),
	MinQuantity: d("1"), MaxQuantity: d("1000000"), MinNotional: d("100"), PriceBand: d("0.05"),
	Tiers: []RiskTier{
		{MaxNotional: d("5"), MaxLeverage: 125, MMR: d("0.004")},
		{MaxNotional: d("10"), MaxLeverage: 100, MMR: d("0.005")},
		{MaxNotional: d("20"), MaxLeverage: 50, MMR: d("0.01")},
		{MaxNotional: d("50"), MaxLeverage: 20, MMR: d("0.025")},
	},
	FundingIntervalHours: 8, MakerFeeRate: d("0.0001"), TakerFeeRate: d("0.0005"), Status: StatusTrading, QuoteDecimals: 8,
	ContractSize: d("100"),
}

// TestInverseFormulas checks coin-M §2.2 on 1000 contracts (100,000
// dollars) entered at 50,000, worth 2 BTC: value, result, maintenance with
// the tiers' deduction, bankruptcy and liquidation prices of both sides,
// and funding.
func TestInverseFormulas(t *testing.T) {
	long := Position{Qty: d("1000"), EntryCost: d("2"), Margin: d("0.1"), MarginMode: Isolated, Leverage: 20}
	short := Position{Qty: d("-1000"), EntryCost: d("2"), Margin: d("0.1"), MarginMode: Isolated, Leverage: 20}
	for _, c := range []struct {
		name string
		got  decimal.Decimal
		want string
	}{
		{"value at 40000", btcUSD.Value(d("1000"), d("40000")), "2.5"},
		{"value at 60000, half up", btcUSD.Value(d("1000"), d("60000")), "1.66666667"},
		{"entry price, the harmonic mean", long.EntryPrice(btcUSD), "50000"},
		{"a long at 40000: 100000 x (1/50000 − 1/40000)", long.UnrealizedPnL(btcUSD, d("40000")), "-0.5"},
		{"a long at 60000", long.UnrealizedPnL(btcUSD, d("60000")), "0.33333333"},
		{"a short at 40000", short.UnrealizedPnL(btcUSD, d("40000")), "0.5"},
		{"a short at 60000", short.UnrealizedPnL(btcUSD, d("60000")), "-0.33333333"},
		{"initial margin: value at entry / 20", InitialMargin(long.EntryCost, 20, 8), "0.1"},
		{"maintenance at 50000: 2 BTC in tier 1", long.MaintenanceMargin(btcUSD, d("50000")), "0.008"},
		// 7.5 BTC is in tier 2: 7.5 x 0.5% − 5 x (0.5% − 0.4%).
		{"maintenance in tier 2, less its deduction", btcUSD.Maintenance(d("7.5")), "0.0325"},
		// Margin used up: 100000 / (2 + 0.1) and 100000 / (2 − 0.1).
		{"a long's bankruptcy price", long.BankruptcyPrice(btcUSD), "47619.04761905"},
		{"a short's bankruptcy price", short.BankruptcyPrice(btcUSD), "52631.57894737"},
		// §2.2: long 100000 x (1 + 0.4%) / (0.1 + 0 + 2); short 100000 x
		// (1 − 0.4%) / (2 − 0.1 − 0).
		{"a long's liquidation price", long.LiquidationPrice(btcUSD), "47809.52380952"},
		{"a short's liquidation price", short.LiquidationPrice(btcUSD), "52421.05263158"},
		{"a long pays funding: 2 BTC x 0.01%", FundingAmount(btcUSD, long.Qty, d("50000"), d("0.0001")), "-0.0002"},
		{"a payer rounds up", FundingAmount(btcUSD, long.Qty, d("60000"), d("0.0001")), "-0.00016667"},
		{"a receiver rounds down", FundingAmount(btcUSD, short.Qty, d("60000"), d("0.0001")), "0.00016666"},
	} {
		if !c.got.Equal(d(c.want)) {
			t.Errorf("%s: %s, want %s", c.name, c.got, c.want)
		}
	}

	// A short whose margin covers its whole cost loses at most that cost
	// in coin: neither a bankruptcy nor a liquidation price; its
	// liquidation and ADL go by the mark.
	safe := Position{Qty: d("-1000"), EntryCost: d("2"), Margin: d("2.5"), MarginMode: Isolated, Leverage: 1}
	if !safe.BankruptcyPrice(btcUSD).IsZero() || !safe.LiquidationPrice(btcUSD).IsZero() {
		t.Fatalf("a fully covered short: %s %s", safe.BankruptcyPrice(btcUSD), safe.LiquidationPrice(btcUSD))
	}
	if got := LiquidationAnchor(btcUSD, safe, d("70000")); !got.Equal(d("70000")) {
		t.Fatalf("anchor %s", got)
	}
	if got := ADLPrice(btcUSD, safe, d("70000.04")); !got.Equal(d("70000")) {
		t.Fatalf("ADL price %s", got)
	}
	if got := LiquidationAnchor(btcUSD, long, d("48000")); !got.Equal(d("47619.04761905")) {
		t.Fatalf("an isolated long's anchor %s", got)
	}

	// In tier 2: 3000 contracts at 40000 (7.5 BTC) with 0.75 of margin:
	// 300000 x 1.005 / (0.75 + 0.005 + 7.5); its value there, 8.21 BTC,
	// is still in tier 2.
	deep := Position{Qty: d("3000"), EntryCost: d("7.5"), Margin: d("0.75"), MarginMode: Isolated, Leverage: 10}
	if got := deep.LiquidationPrice(btcUSD); !got.Equal(d("36523.31920048")) {
		t.Fatalf("a long in tier 2: %s", got)
	}
	// Its margin balance meets the maintenance margin there (to the
	// rounding of the value).
	at := d("36523.31920048")
	balance, maintenance := deep.MarginBalance(btcUSD, at), deep.MaintenanceMargin(btcUSD, at)
	if diff := balance.Sub(maintenance).Abs(); diff.GreaterThan(d("0.00000002")) {
		t.Fatalf("at its liquidation price: balance %s, maintenance %s", balance, maintenance)
	}

	// Cross: the account's equity 0.5 BTC with the long at 50000, nothing
	// else held: 100000 x 1.004 / (0.5 + 2).
	cross := long
	cross.MarginMode = Cross
	if got := CrossLiquidationPrice(btcUSD, cross, d("50000"), d("0.5"), decimal.Zero); !got.Equal(d("40160")) {
		t.Fatalf("cross %s", got)
	}
}

// inverseOrder is user's order on btcUSD at 20x cross, the mark at mark.
func inverseOrder(t *testing.T, user string, side Side, typ Type, price, qty, mark string) Order {
	t.Helper()
	req := Request{UserID: user, Symbol: btcUSD.Symbol, Side: side, PositionSide: SideBoth, Type: typ, Qty: d(qty)}
	if price != "" {
		req.Price = d(price)
	}
	s := Settings{UserID: user, Symbol: btcUSD.Symbol, PositionMode: OneWay, MarginMode: Cross, Leverage: 20}
	o, err := NewOrder(strings.ToLower(user+string(side)+price+qty), req, btcUSD, s, d(mark), t0)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// TestInverseReservations checks that orders reserve the coin their fills
// may need (coin-M §2.2): a buy at the lower of its price and the mark (a
// coin's worth of dollars grows as the price falls), a sell at its price.
func TestInverseReservations(t *testing.T) {
	for _, c := range []struct {
		side        Side
		typ         Type
		price       string
		margin, fee string // per contract, rounded up
	}{
		// 100 / 50000 / 20 = 0.0001; the fee 0.002 x 0.05%.
		{Buy, Limit, "50000", "0.0001", "0.000001"},
		// Above the mark: it fills near the mark, where a contract needs
		// more coin than at its price.
		{Buy, Limit, "51000", "0.0001", "0.000001"},
		{Buy, Limit, "49000", "0.00010205", "0.00000103"},
		// A market buy's protection price is the band above the mark.
		{Buy, Market, "", "0.0001", "0.000001"},
		// A sell fills at its price or above: its price covers it.
		{Sell, Limit, "49000", "0.00010205", "0.00000103"},
		{Sell, Limit, "51000", "0.00009804", "0.00000099"},
	} {
		o := inverseOrder(t, "u1", c.side, c.typ, c.price, "10", "50000")
		if !o.MarginPerLot.Equal(d(c.margin)) || !o.FeePerLot.Equal(d(c.fee)) {
			t.Errorf("%s %s %s: margin %s fee %s a contract, want %s %s", c.side, c.typ, c.price, o.MarginPerLot, o.FeePerLot, c.margin, c.fee)
		}
	}
	// One contract is its face value, the least an order may be.
	if _, err := NewOrder("one", Request{
		UserID: "u1", Symbol: btcUSD.Symbol, Side: Buy, PositionSide: SideBoth, Type: Limit,
		Price: d("50000"), Qty: d("1"),
	}, btcUSD, Settings{
		UserID: "u1", Symbol: btcUSD.Symbol, PositionMode: OneWay, MarginMode: Cross,
		Leverage: 20,
	}, d("50000"), t0); err != nil {
		t.Fatalf("one contract: %v", err)
	}
}

// TestInverseFillsKeepTheBooksExact trades 1000 contracts between two
// users, closes 600 at a higher price, then turns the long around: each
// fill books one coin value on both of its sides however they split it,
// so PNL_CLEARING − Σ long cost + Σ short cost stays 0 (invariant 6 of an
// inverse contract, whose costs move the other way to the linear one's).
func TestInverseFillsKeepTheBooksExact(t *testing.T) {
	held := map[string]map[PositionSide]Position{"a": {}, "b": {}, "c": {}}
	clearing := decimal.Zero
	fill := func(user string, o Order, price, qty string) FillPlan {
		t.Helper()
		plan, err := PlanFill(btcUSD, o, held[user], FillInput{TradeID: "t" + price + qty, Price: d(price), Qty: d(qty), ExecutedAt: t0})
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range plan.Positions {
			held[user][ch.Position.Side] = ch.Position
		}
		clearing = clearing.Sub(plan.Fill.RealizedPnL)
		return plan
	}
	exact := func(step string) {
		t.Helper()
		sum := clearing
		for _, sides := range held {
			for _, p := range sides {
				switch {
				case p.Qty.IsPositive():
					sum = sum.Sub(p.EntryCost)
				case p.Qty.IsNegative():
					sum = sum.Add(p.EntryCost)
				}
			}
		}
		if !sum.IsZero() {
			t.Fatalf("%s: PNL_CLEARING − long cost + short cost = %s", step, sum)
		}
	}

	open := fill("a", inverseOrder(t, "a", Buy, Limit, "50000", "1000", "50000"), "50000", "1000")
	fill("b", inverseOrder(t, "b", Sell, Limit, "50000", "1000", "50000"), "50000", "1000")
	// 100000 / 50000 = 2 BTC; the taker fee 2 x 0.05%.
	if a := held["a"][SideBoth]; !a.EntryCost.Equal(d("2")) || !open.Fill.Fee.Equal(d("0.001")) {
		t.Fatalf("opened %+v, fee %s", a, open.Fill.Fee)
	}
	exact("opened")

	// 600 closed at 60000: proceeds 60000 / 60000 = 1 BTC against a cost
	// of 1.2: the long gains 0.2, the short loses it.
	closed := fill("a", inverseOrder(t, "a", Sell, Limit, "60000", "600", "60000"), "60000", "600")
	fill("b", inverseOrder(t, "b", Buy, Limit, "60000", "600", "60000"), "60000", "600")
	if !closed.Fill.RealizedPnL.Equal(d("0.2")) || !held["b"][SideBoth].RealizedPnL.Equal(d("-0.2")) {
		t.Fatalf("closed: a %s, b %s", closed.Fill.RealizedPnL, held["b"][SideBoth].RealizedPnL)
	}
	exact("closed 600")

	// a sells 1000 at 45000 to a new user c: it closes its 400 (proceeds
	// 40000 / 45000 = 0.88888889) and opens a short with what is left of
	// the fill's 2.22222222; c's long costs the whole 2.22222222.
	flip := fill("a", inverseOrder(t, "a", Sell, Limit, "45000", "1000", "45000"), "45000", "1000")
	fill("c", inverseOrder(t, "c", Buy, Limit, "45000", "1000", "45000"), "45000", "1000")
	if a := held["a"][SideBoth]; !a.Qty.Equal(d("-600")) || !a.EntryCost.Equal(d("1.33333333")) ||
		!flip.Fill.RealizedPnL.Equal(d("-0.08888889")) || !held["c"][SideBoth].EntryCost.Equal(d("2.22222222")) {
		t.Fatalf("turned around: a %+v (pnl %s), c %+v", a, flip.Fill.RealizedPnL, held["c"][SideBoth])
	}
	exact("turned around")
}
