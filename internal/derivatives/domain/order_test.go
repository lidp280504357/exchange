package domain

import (
	"testing"

	"github.com/skill/exchange/internal/platform/apperr"
)

func code(err error) string { return apperr.From(err).Code }

func TestOrderChecks(t *testing.T) {
	oneWay, hedge := settings(OneWay, Cross, 10), settings(Hedge, Isolated, 10)
	for _, c := range []struct {
		name string
		req  Request
		s    Settings
		want string
	}{
		{"a price off the tick", Request{Side: Buy, Type: Limit, Price: d("60000.05"), Qty: d("0.1")}, oneWay, "INSTRUMENT_PRECISION"},
		{"a quantity off the lot", Request{Side: Buy, Type: Limit, Price: d("60000"), Qty: d("0.0015")}, oneWay, "INSTRUMENT_PRECISION"},
		{"too far from the mark", Request{Side: Buy, Type: Limit, Price: d("63001"), Qty: d("0.1")}, oneWay, "ORDER_PRICE_OUT_OF_BAND"},
		{"too small", Request{Side: Buy, Type: Limit, Price: d("60000"), Qty: d("0")}, oneWay, "COMMON_INVALID_ARGUMENT"},
		{"below the minimum notional", Request{Side: Buy, Type: Limit, Price: d("3000"), Qty: d("0.001")}, settings(OneWay, Cross, 10), "ORDER_PRICE_OUT_OF_BAND"},
		{"a side for hedge mode in one-way", Request{Side: Buy, PositionSide: SideLong, Type: Limit, Price: d("60000"), Qty: d("0.1")}, oneWay, "COMMON_INVALID_ARGUMENT"},
		{"no side in hedge mode", Request{Side: Buy, Type: Limit, Price: d("60000"), Qty: d("0.1")}, hedge, "COMMON_INVALID_ARGUMENT"},
		{"reduce-only in hedge mode", Request{Side: Sell, PositionSide: SideLong, Type: Limit, Price: d("60000"), Qty: d("0.1"), ReduceOnly: true}, hedge, "COMMON_INVALID_ARGUMENT"},
		{"a market order with a price", Request{Side: Buy, Type: Market, Price: d("60000"), Qty: d("0.1")}, oneWay, "COMMON_INVALID_ARGUMENT"},
		{"a market GTC", Request{Side: Buy, Type: Market, TimeInForce: GTC, Qty: d("0.1")}, oneWay, "COMMON_INVALID_ARGUMENT"},
	} {
		c.req.UserID, c.req.Symbol = "u1", btcPerp.Symbol
		_, err := NewOrder("o1", c.req, btcPerp, c.s, d("60000"), t0)
		if code(err) != c.want {
			t.Errorf("%s: %v, want %s", c.name, err, c.want)
		}
	}
	halted := btcPerp
	halted.Status = "HALT"
	if _, err := NewOrder("o1", Request{UserID: "u1", Side: Buy, Type: Limit, Price: d("60000"), Qty: d("0.1")}, halted, oneWay, d("60000"), t0); code(err) != "INSTRUMENT_NOT_TRADING" {
		t.Fatalf("halted: %v", err)
	}
	if _, err := NewOrder("o1", Request{UserID: "u1", Side: Buy, Type: Limit, Price: d("60000"), Qty: d("0.1")}, btcPerp, oneWay, d("0"), t0); code(err) != "DERIV_MARK_PRICE_UNAVAILABLE" {
		t.Fatalf("no mark: %v", err)
	}
	small := btcPerp
	small.MinNotional = d("100")
	if _, err := NewOrder("o1", Request{UserID: "u1", Side: Buy, Type: Limit, Price: d("60000"), Qty: d("0.001")}, small, oneWay, d("60000"), t0); code(err) != "ORDER_MIN_NOTIONAL" {
		t.Fatalf("min notional: %v", err)
	}
}

func TestMarketOrdersGoAsLimitOrdersAtTheirProtection(t *testing.T) {
	s := settings(OneWay, Cross, 20)
	buy, err := NewOrder("o1", Request{UserID: "u1", Side: Buy, Type: Market, Qty: d("0.1")}, btcPerp, s, d("60000.07"), t0)
	if err != nil {
		t.Fatal(err)
	}
	// 5% above the mark, down to the tick; IOC by default; its margin is
	// reserved at that price.
	if !buy.Price.Equal(d("63000")) || buy.TimeInForce != IOC || !buy.MarginPerLot.Equal(d("3.15")) {
		t.Fatalf("market buy %+v", buy)
	}
	sell, err := NewOrder("o2", Request{UserID: "u1", Side: Sell, Type: Market, TimeInForce: FOK, Qty: d("0.1")}, btcPerp, s, d("60000.07"), t0)
	if err != nil || !sell.Price.Equal(d("57000.1")) || sell.TimeInForce != FOK {
		t.Fatalf("market sell %+v %v", sell, err)
	}
	// A sell fills at its price or above: it reserves at the mark, not 5%
	// below it, so the position gets its whole initial margin.
	if !sell.MarginPerLot.Equal(d("3.000004")) || !sell.FeePerLot.Equal(d("0.030001")) {
		t.Fatalf("market sell reserves %s and %s per lot", sell.MarginPerLot, sell.FeePerLot)
	}
	limit, err := NewOrder("o3", Request{UserID: "u1", Side: Sell, Type: Limit, Price: d("59000"), Qty: d("0.1")}, btcPerp, s, d("60000.07"), t0)
	if err != nil || !limit.MarginPerLot.Equal(d("3.000004")) {
		t.Fatalf("a limit sell below the mark reserves at the mark: %s %v", limit.MarginPerLot, err)
	}
	limit, err = NewOrder("o4", Request{UserID: "u1", Side: Sell, Type: Limit, Price: d("61000"), Qty: d("0.1")}, btcPerp, s, d("60000.07"), t0)
	if err != nil || !limit.MarginPerLot.Equal(d("3.05")) {
		t.Fatalf("a limit sell above the mark reserves at its price: %s %v", limit.MarginPerLot, err)
	}
}

func TestClosingOrdersFitTheirPosition(t *testing.T) {
	held := map[PositionSide]Position{SideBoth: {Qty: d("0.5")}, SideLong: {Qty: d("0.3")}}
	s := settings(OneWay, Cross, 10)
	sell := order(t, Sell, SideBoth, "60000", "0.3", true, s)
	other := order(t, Sell, SideBoth, "60100", "0.25", true, s)
	if err := CheckClosing(sell, held, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckClosing(sell, held, []Order{other}); code(err) != "DERIV_REDUCE_ONLY_REJECTED" {
		t.Fatalf("0.3 + 0.25 of 0.5: %v", err)
	}
	buy := order(t, Buy, SideBoth, "60000", "0.1", true, s)
	if err := CheckClosing(buy, held, nil); code(err) != "DERIV_REDUCE_ONLY_REJECTED" {
		t.Fatalf("a reduce-only buy of a long: %v", err)
	}
	hedge := order(t, Sell, SideLong, "60000", "0.3", false, settings(Hedge, Cross, 10))
	if err := CheckClosing(hedge, held, nil); err != nil {
		t.Fatal(err)
	}
}

// Closing orders a shrunken position has no room for any more (review
// C62): the older keep the room, the rest go; a hedge-mode close of the
// long side by the long, a one-way reduce-only order by the position the
// way it closes; the liquidation engine's and ADL's orders, and those
// canceled already, are not counted.
func TestClosingOrdersBeyondTheirPosition(t *testing.T) {
	oneWay, hedge := settings(OneWay, Cross, 10), settings(Hedge, Cross, 10)
	older := order(t, Sell, SideBoth, "61000", "0.3", true, oneWay)
	newer := order(t, Sell, SideBoth, "61100", "0.2", true, oneWay)
	small := order(t, Sell, SideBoth, "61200", "0.05", true, oneWay)
	opening := order(t, Sell, SideBoth, "61300", "0.4", false, oneWay)
	ids := func(list []Order) []string {
		var out []string
		for _, o := range list {
			out = append(out, o.ID)
		}
		return out
	}
	newer.ID, small.ID, opening.ID = "o2", "o3", "o4"
	active := []Order{older, newer, small, opening}
	// A long of 0.55 holds 0.3 + 0.2 + 0.05: nothing goes.
	if got := BeyondPosition(map[PositionSide]Position{SideBoth: {Qty: d("0.55")}}, active); len(got) != 0 {
		t.Fatalf("all fit: %v", ids(got))
	}
	// Deleveraged to 0.35: the older 0.3 keeps its room, the 0.2 goes, the
	// 0.05 still fits.
	if got := ids(BeyondPosition(map[PositionSide]Position{SideBoth: {Qty: d("0.35")}}, active)); len(got) != 1 || got[0] != "o2" {
		t.Fatalf("0.35 left: %v", got)
	}
	// Turned short: no reduce-only sell closes anything.
	if got := ids(BeyondPosition(map[PositionSide]Position{SideBoth: {Qty: d("-0.1")}}, active)); len(got) != 3 {
		t.Fatalf("a short: %v", got)
	}
	// Hedge mode: the sells of the long side, by the long.
	sellLong := order(t, Sell, SideLong, "61000", "0.3", false, hedge)
	buyShort := order(t, Buy, SideShort, "59000", "0.2", false, hedge)
	buyShort.ID = "s1"
	held := map[PositionSide]Position{SideLong: {Qty: d("0.1")}, SideShort: {Qty: d("-0.2")}}
	if got := ids(BeyondPosition(held, []Order{sellLong, buyShort})); len(got) != 1 || got[0] != sellLong.ID {
		t.Fatalf("hedge: %v", got)
	}
	// The liquidation engine's and ADL's, and one on its way out, stay.
	liq, adl, gone := older, newer, small
	liq.Kind, adl.Kind, gone.CancelRequested = KindLiquidation, KindADL, true
	if got := BeyondPosition(map[PositionSide]Position{}, []Order{liq, adl, gone}); len(got) != 0 {
		t.Fatalf("system orders and canceled ones: %v", ids(got))
	}
}

func TestRiskLimits(t *testing.T) {
	// At 20x the ladder allows 250000 of notional: 4.166 BTC at 60000.
	s := settings(OneWay, Cross, 20)
	held := map[PositionSide]Position{SideBoth: {Qty: d("3")}}
	ok := order(t, Buy, SideBoth, "60000", "1", false, s)
	if err := CheckRiskLimit(btcPerp, ok, held, nil, d("60000")); err != nil {
		t.Fatal(err)
	}
	pending := order(t, Buy, SideBoth, "59000", "0.5", false, s)
	if err := CheckRiskLimit(btcPerp, ok, held, []Order{pending}, d("60000")); code(err) != "DERIV_RISK_LIMIT_EXCEEDED" {
		t.Fatalf("3 + 0.5 + 1 BTC: %v", err)
	}
	// Selling adds to the short side, which is empty.
	sell := order(t, Sell, SideBoth, "60000", "4", false, s)
	if err := CheckRiskLimit(btcPerp, sell, held, []Order{pending}, d("60000")); err != nil {
		t.Fatal(err)
	}
	// At 50x only 50000; the error names the cap, the leverage and the
	// notional the order would make (at the mark, to the cent).
	fifty := order(t, Buy, SideBoth, "60000", "1", false, settings(OneWay, Cross, 50))
	err := CheckRiskLimit(btcPerp, fifty, nil, nil, d("60000.004"))
	if code(err) != "DERIV_RISK_LIMIT_EXCEEDED" {
		t.Fatalf("60000 at 50x: %v", err)
	}
	if det := apperr.From(err).Details; det["max_notional"] != "50000" || det["leverage"] != int32(50) || det["notional"] != "60000.01" {
		t.Fatalf("details %v", det)
	}
	if !btcPerp.MaxNotional(21).Equal(d("50000")) || !btcPerp.MaxNotional(60).IsZero() || !btcPerp.MMR(d("60000")).Equal(d("0.01")) {
		t.Fatal("ladder lookups")
	}
}

func TestLeverageAndMarginChanges(t *testing.T) {
	cross := Position{Qty: d("1"), EntryCost: d("60000"), Margin: d("6000"), MarginMode: Cross, Leverage: 10}
	isolated := Position{Side: SideShort, Qty: d("-0.5"), EntryCost: d("30000"), Margin: d("3000"), MarginMode: Isolated, Leverage: 10}
	changes, err := ChangeLeverage(btcPerp, 20, []Position{cross, isolated}, d("60000"))
	if err != nil {
		t.Fatal(err)
	}
	if !changes[0].Delta.Equal(d("-3000")) || !changes[0].Position.Margin.Equal(d("3000")) || !changes[1].Delta.IsZero() || changes[1].Position.Leverage != 20 {
		t.Fatalf("changes %+v", changes)
	}
	if _, err := ChangeLeverage(btcPerp, 5, []Position{isolated}, d("60000")); code(err) != "DERIV_INSUFFICIENT_MARGIN" {
		t.Fatalf("an isolated position below its new initial margin: %v", err)
	}
	if _, err := ChangeLeverage(btcPerp, 50, []Position{cross}, d("60000")); code(err) != "DERIV_RISK_LIMIT_EXCEEDED" {
		t.Fatalf("60000 at 50x: %v", err)
	}
	if _, err := ChangeLeverage(btcPerp, 51, nil, d("60000")); code(err) != "DERIV_LEVERAGE_EXCEEDED" {
		t.Fatalf("51x: %v", err)
	}

	// The short is 500 in profit at 59000: it may keep 3000 (initial
	// margin at entry) of its 3000; taking 1 is too much.
	if _, err := AdjustMargin(btcPerp, isolated, d("-1"), d("59000")); code(err) != "DERIV_MARGIN_REDUCE_TOO_LARGE" {
		t.Fatalf("below the initial margin: %v", err)
	}
	more, err := AdjustMargin(btcPerp, isolated, d("500"), d("59000"))
	if err != nil || !more.Margin.Equal(d("3500")) {
		t.Fatalf("add %+v %v", more, err)
	}
	if _, err := AdjustMargin(btcPerp, more, d("-500"), d("59000")); err != nil {
		t.Fatal(err)
	}
	if _, err := AdjustMargin(btcPerp, cross, d("1"), d("59000")); code(err) != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("cross: %v", err)
	}
}

func TestPositionFigures(t *testing.T) {
	long := Position{Qty: d("2"), EntryCost: d("120000"), Margin: d("6000"), MarginMode: Isolated, Leverage: 20}
	if !long.EntryPrice(btcPerp).Equal(d("60000")) || !long.UnrealizedPnL(btcPerp, d("61000")).Equal(d("2000")) ||
		!long.BankruptcyPrice(btcPerp).Equal(d("57000")) {
		t.Fatalf("long %s %s %s", long.EntryPrice(btcPerp), long.UnrealizedPnL(btcPerp, d("61000")), long.BankruptcyPrice(btcPerp))
	}
	// Tier 2 (1%) takes 50000 x (1% − 0.4%) = 300 off its maintenance:
	// (120000 − 6000 − 300) / (2 x (1 − 0.01)) = 57424.24242424.
	if got := long.LiquidationPrice(btcPerp); !got.Equal(d("57424.24242424")) {
		t.Fatalf("liquidation price %s", got)
	}
	short := Position{Qty: d("-2"), EntryCost: d("120000"), Margin: d("6000")}
	if !short.UnrealizedPnL(btcPerp, d("61000")).Equal(d("-2000")) || !short.BankruptcyPrice(btcPerp).Equal(d("63000")) {
		t.Fatal("short")
	}
	if got := Transferable(d("1000"), d("-300")); !got.Equal(d("700")) {
		t.Fatalf("transferable %s", got)
	}
	if got := Transferable(d("1000"), d("300")); !got.Equal(d("1000")) {
		t.Fatalf("profit does not leave: %s", got)
	}
	if got := Transferable(d("100"), d("-300")); !got.IsZero() {
		t.Fatalf("a loss beyond the balance: %s", got)
	}
}
