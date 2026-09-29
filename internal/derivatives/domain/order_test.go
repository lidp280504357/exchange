package domain

import (
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
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
	// At 50x only 50000.
	fifty := order(t, Buy, SideBoth, "60000", "1", false, settings(OneWay, Cross, 50))
	if err := CheckRiskLimit(btcPerp, fifty, nil, nil, d("60000")); code(err) != "DERIV_RISK_LIMIT_EXCEEDED" {
		t.Fatalf("60000 at 50x: %v", err)
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
	if !long.EntryPrice().Equal(d("60000")) || !long.UnrealizedPnL(d("61000")).Equal(d("2000")) || !long.BankruptcyPrice().Equal(d("57000")) {
		t.Fatalf("long %s %s %s", long.EntryPrice(), long.UnrealizedPnL(d("61000")), long.BankruptcyPrice())
	}
	// (120000 − 6000) / (2 x (1 − 0.01)) = 57575.75757576
	if got := long.LiquidationPrice(btcPerp); !got.Equal(d("57575.75757576")) {
		t.Fatalf("liquidation price %s", got)
	}
	short := Position{Qty: d("-2"), EntryCost: d("120000"), Margin: d("6000")}
	if !short.UnrealizedPnL(d("61000")).Equal(d("-2000")) || !short.BankruptcyPrice().Equal(d("63000")) {
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
