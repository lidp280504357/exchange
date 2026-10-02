package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// alone: every book spends its assets alone.
func alone(string) int { return 1 }

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
	buy, sell := SpotRooms(btc, h, prices, backed, alone, caps)
	if sell.String() != "0.38" || buy.String() != "1.6" {
		t.Fatalf("buy %s sell %s", buy, sell)
	}
	// An internal asset HOUSE sold short: selling more is bounded by the
	// pair's cap only, buying back by the USDT it holds.
	pepe := Spec{Symbol: "1000PEPE-USDT", Base: "PEPE", Quote: "USDT", TickSize: d("0.000001"), LotSize: d("1")}
	h = Holdings{"PEPE": d("-9000000"), "USDT": d("5000")}
	buy, sell = SpotRooms(pepe, h, prices, backed, alone, caps)
	if sell.String() != "1000000" || buy.String() != "400000" {
		t.Fatalf("pepe buy %s sell %s", buy, sell)
	}
	// Near the total cap only the directions that shrink positions stay.
	h = Holdings{"BTC": d("1.99"), "PEPE": d("-90000000"), "USDT": d("10000000")}
	caps.Total = d("1000000")
	buy, sell = SpotRooms(btc, h, prices, backed, alone, caps)
	if buy.String() != "0.01" || sell.String() != "1.97" {
		t.Fatalf("near the total cap: buy %s (what is left of it), sell %s (what it holds, less the safety)", buy, sell)
	}
	if b, s := SpotRooms(btc, h, map[string]decimal.Decimal{}, backed, alone, caps); !b.IsZero() || !s.IsZero() {
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
	buy, sell := SpotRooms(ethBTC, h, prices, backed, alone, caps)
	if buy.String() != "7.096" || sell.String() != "7.029" {
		t.Fatalf("buy %s sell %s", buy, sell)
	}
	delete(prices, "BTC")
	if b, s := SpotRooms(ethBTC, h, prices, backed, alone, caps); !b.IsZero() || !s.IsZero() {
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
	buy, sell := ContractRooms(perp, d("-0.5"), d("50000"), d("1000000"), caps) // HOUSE short 0.5
	if buy.String() != "2.5" || sell.String() != "1.5" {
		t.Fatalf("buy %s sell %s", buy, sell)
	}
}

// HOUSE is never liquidated, so its equity bounds its contract positions
// (review A4): together they may be worth ContractLeverage times it, and
// past that, or with no equity, HOUSE only reduces them.
func TestContractRoomsKeepWithinHouseEquity(t *testing.T) {
	perp := Spec{Symbol: "BTC-USDT-PERP", Base: "BTC", Quote: "USDT", TickSize: d("0.1"), LotSize: d("0.001"), Contract: true}
	caps := Caps{Contract: d("1000000"), ContractLeverage: d("10")}
	acct := ContractAccount{Equity: d("20000"), Exposure: d("175000")} // 25,000 of room
	room := ContractRoom(acct, caps)
	if !room.Equal(d("25000")) {
		t.Fatalf("room %s", room)
	}
	// Short 0.5 at 50,000: buying closes it, then grows a long by 0.5 more.
	buy, sell := ContractRooms(perp, d("-0.5"), d("50000"), room, caps)
	if buy.String() != "1" || sell.String() != "0.5" {
		t.Fatalf("buy %s sell %s", buy, sell)
	}
	// Over its leverage after a loss: it only reduces.
	acct.Equity = d("15000")
	room = ContractRoom(acct, caps)
	buy, sell = ContractRooms(perp, d("-0.5"), d("50000"), room, caps)
	if !room.IsZero() || buy.String() != "0.5" || !sell.IsZero() {
		t.Fatalf("over its leverage: room %s buy %s sell %s", room, buy, sell)
	}
	// No equity at all: the same.
	acct.Equity = d("-100")
	if room = ContractRoom(acct, caps); !room.IsZero() {
		t.Fatalf("no equity: room %s", room)
	}
	// A zero ContractLeverage leaves no room.
	if room = ContractRoom(ContractAccount{Equity: d("1000000")}, Caps{}); !room.IsZero() {
		t.Fatalf("no leverage: room %s", room)
	}
}

// The inventory a backed asset holds above the safety is shared by the
// books that spend it: two books on BTC's 0.4 less 1,000 USDT's worth
// each sell half.
func TestSpotRoomsShareABackedAssetsInventory(t *testing.T) {
	caps := Caps{Level: d("20000"), Symbol: d("100000"), Total: d("1000000"), Safety: d("1000")}
	prices := map[string]decimal.Decimal{"BTC": d("50000"), "USDT": d("1")}
	h := Holdings{"BTC": d("0.4"), "USDT": d("500000")}
	backed := func(a string) bool { return a == "BTC" || a == "USDT" }
	_, sell := SpotRooms(btc, h, prices, backed, alone, caps)
	_, half := SpotRooms(btc, h, prices, backed, func(a string) int { return map[string]int{"BTC": 2}[a] }, caps)
	if !sell.Equal(d("0.38")) || !half.Equal(d("0.19")) {
		t.Fatalf("alone %s, shared by two %s", sell, half)
	}
	// A thin share is still a lot: 1,015 USDT among 87 books buys a lot
	// of BTC each, not nothing; below a lot above the safety, nothing.
	many := func(string) int { return 87 }
	h = Holdings{"BTC": d("0"), "USDT": d("1015")}
	if buy, _ := SpotRooms(btc, h, prices, backed, many, caps); !buy.Equal(d("0.0001")) {
		t.Fatalf("a thin share: buy %s", buy)
	}
	h["USDT"] = d("1004")
	if buy, _ := SpotRooms(btc, h, prices, backed, many, caps); !buy.IsZero() {
		t.Fatalf("less than a lot above the safety: buy %s", buy)
	}
}
