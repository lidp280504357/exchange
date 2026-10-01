package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var btc = Spec{Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", TickSize: d("0.1"), LotSize: d("0.0001")}

func levels(pq ...string) []Level {
	var out []Level
	for i := 0; i+1 < len(pq); i += 2 {
		out = append(out, Level{Price: d(pq[i]), Quantity: d(pq[i+1])})
	}
	return out
}

func show(ls []Level) string {
	s := ""
	for _, l := range ls {
		s += l.Price.String() + "x" + l.Quantity.String() + " "
	}
	return s
}

func TestLevelsSitOnTheGridAwayFromTheSpread(t *testing.T) {
	// Bids round down, asks up; 60000.05 and 60000.01 meet at 60000.
	bids := Levels(levels("60000.05", "0.1", "60000.01", "0.2", "59999.95", "0.3"), true, btc, d("20000"), 20)
	if got := show(bids); got != "60000x0.3 59999.9x0.3 " {
		t.Fatalf("bids %s", got)
	}
	asks := Levels(levels("60000.06", "0.1", "60000.12", "0.00005"), false, btc, d("20000"), 20)
	if got := show(asks); got != "60000.1x0.1 " { // 60000.2 holds less than a lot
		t.Fatalf("asks %s", got)
	}
	// A level worth more than the cap is cut to it, in whole lots; n caps
	// the count.
	big := Levels(levels("60000", "5", "59999", "5", "59998", "5"), true, btc, d("20000"), 2)
	if got := show(big); got != "60000x0.3333 59999x0.3333 " {
		t.Fatalf("capped %s", got)
	}
}

func TestSpotRoomsFollowInventoryAndExposure(t *testing.T) {
	prices := map[string]decimal.Decimal{"BTC": d("50000"), "PEPE": d("0.01")}
	backed := func(a string) bool { return a == "BTC" || a == "USDT" }
	caps := Caps{Level: d("20000"), Symbol: d("100000"), Total: d("1000000"), Safety: d("1000")}
	// 0.4 BTC (20,000) and 500,000 USDT: selling stops at the inventory
	// less the safety margin, buying at the pair's 100,000.
	h := Holdings{"BTC": d("0.4"), "USDT": d("500000")}
	buy, sell := SpotRooms(btc, h, prices, backed, caps)
	if sell.String() != "0.38" || buy.String() != "1.6" {
		t.Fatalf("buy %s sell %s", buy, sell)
	}
	// An internal asset HOUSE sold short: selling more is bounded by the
	// pair's cap only, buying back by the USDT it holds.
	pepe := Spec{Symbol: "1000PEPE-USDT", Base: "PEPE", Quote: "USDT", TickSize: d("0.000001"), LotSize: d("1")}
	h = Holdings{"PEPE": d("-9000000"), "USDT": d("5000")}
	buy, sell = SpotRooms(pepe, h, prices, backed, caps)
	if sell.String() != "1000000" || buy.String() != "400000" {
		t.Fatalf("pepe buy %s sell %s", buy, sell)
	}
	// Near the total cap only the directions that shrink positions stay.
	h = Holdings{"BTC": d("1.99"), "PEPE": d("-90000000"), "USDT": d("10000000")}
	caps.Total = d("1000000")
	buy, sell = SpotRooms(btc, h, prices, backed, caps)
	if buy.String() != "0.01" || sell.String() != "1.97" {
		t.Fatalf("near the total cap: buy %s (what is left of it), sell %s (what it holds, less the safety)", buy, sell)
	}
	if b, s := SpotRooms(btc, h, map[string]decimal.Decimal{}, backed, caps); !b.IsZero() || !s.IsZero() {
		t.Fatalf("no price, no room: %s %s", b, s)
	}
}

// On a pair quoted in BTC the limits are still in USDT: buying ETH spends
// BTC worth its USDT price, and BTC counts towards the total.
func TestSpotRoomsOnAPairQuotedInBTC(t *testing.T) {
	ethBTC := Spec{Symbol: "ETH-BTC", Base: "ETH", Quote: "BTC", TickSize: d("0.00001"), LotSize: d("0.001")}
	prices := map[string]decimal.Decimal{"USDT": d("1"), "ETH": d("2700"), "BTC": d("84000")}
	backed := func(a string) bool { return a == "BTC" || a == "ETH" || a == "USDT" }
	caps := Caps{Level: d("20000"), Symbol: d("100000"), Total: d("1000000"), Safety: d("1000")}
	h := Holdings{"ETH": d("7.4"), "BTC": d("0.24"), "USDT": d("500000")}
	// Buying: 0.24 BTC is 20,160 USDT, 19,160 above the safety: 7.096 ETH.
	// Selling: 7.4 ETH less the safety's 0.370 ETH.
	buy, sell := SpotRooms(ethBTC, h, prices, backed, caps)
	if buy.String() != "7.096" || sell.String() != "7.029" {
		t.Fatalf("buy %s sell %s", buy, sell)
	}
	delete(prices, "BTC")
	if b, s := SpotRooms(ethBTC, h, prices, backed, caps); !b.IsZero() || !s.IsZero() {
		t.Fatalf("no price for the quote, no room: %s %s", b, s)
	}
}

func TestLevelCapIsInTheQuoteAsset(t *testing.T) {
	caps := Caps{Level: d("20000")}
	prices := map[string]decimal.Decimal{"USDT": d("1"), "BTC": d("84000")}
	if c, ok := LevelCap(btc, caps, prices); !ok || !c.Equal(d("20000")) {
		t.Fatalf("a USDT pair: %s %v", c, ok)
	}
	ethBTC := Spec{Symbol: "ETH-BTC", Base: "ETH", Quote: "BTC"}
	if c, ok := LevelCap(ethBTC, caps, prices); !ok || !c.Equal(d("20000").Div(d("84000"))) {
		t.Fatalf("ETH-BTC: %s %v", c, ok)
	}
	if _, ok := LevelCap(ethBTC, caps, map[string]decimal.Decimal{"USDT": d("1")}); ok {
		t.Fatal("ETH-BTC without a BTC price has no cap")
	}
	perp := Spec{Symbol: "BTC-USDT-PERP", Base: "BTC", Quote: "USDT", Contract: true}
	if c, ok := LevelCap(perp, caps, nil); !ok || !c.Equal(d("20000")) {
		t.Fatalf("a contract: %s %v", c, ok)
	}
}

func TestContractRoomsCapTheNetPosition(t *testing.T) {
	perp := Spec{Symbol: "BTC-USDT-PERP", Base: "BTC", Quote: "USDT", TickSize: d("0.1"), LotSize: d("0.001"), Contract: true}
	caps := Caps{Contract: d("100000")}
	buy, sell := ContractRooms(perp, d("-0.5"), d("50000"), caps) // HOUSE short 0.5
	if buy.String() != "2.5" || sell.String() != "1.5" {
		t.Fatalf("buy %s sell %s", buy, sell)
	}
}
