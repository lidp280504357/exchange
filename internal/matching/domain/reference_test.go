package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

var t0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

// ref is a BTC-USDT reference book: HOUSE buys at 59990 and 59980, sells
// at 60010 and 60020, 0.5 BTC a level, with room for 0.8 each way.
func ref() Reference {
	return Reference{
		Bids:    []RefLevel{{d("59990"), d("0.5")}, {d("59980"), d("0.5")}},
		Asks:    []RefLevel{{d("60010"), d("0.5")}, {d("60020"), d("0.5")}},
		BuyRoom: d("0.8"), SellRoom: d("0.8"), HouseUser: "house", At: t0,
	}
}

// at stamps an order with its command time.
func at(o Order, after time.Duration) Order {
	o.At = t0.Add(after)
	return o
}

func houseOnly(o Order) Order {
	o.HouseOnly = true
	return o
}

func TestMarketOrdersWalkTheReferenceBook(t *testing.T) {
	b := NewBook("BTC-USDT")
	expect(t, b.Reference(ref()), "")
	sell := at(order("u", Sell, Market, IOC, "", "0.6"), time.Second)
	evs := b.Place(sell)
	// HOUSE's bids best first, at their prices.
	expect(t, evs, "T 0.5@59990; T 0.1@59980; "+sell.ID+" FILLED 0.6/35993")
	tr := evs[0].Trade
	if tr.HouseSide != Buy || tr.BuyUserID != "house" || tr.BuyOrderID != "" || tr.SellOrderID != sell.ID || tr.TakerSide != Sell ||
		!tr.BuyerIsMaker || !tr.BuyerFee.IsZero() || tr.SellerFee.String() != "59.99" {
		t.Fatalf("trade %+v", tr)
	}
	// 0.2 of HOUSE's buying room is left: a market sell of 0.3 gets 0.2.
	sell2 := at(order("u", Sell, Market, IOC, "", "0.3"), time.Second)
	expect(t, b.Place(sell2), "T 0.2@59980; "+sell2.ID+" CANCELED 0.2/11996 NO_LIQUIDITY")
}

func TestMarketBuysSpendQuoteOnTheReferenceBook(t *testing.T) {
	b := NewBook("BTC-USDT")
	b.Reference(ref())
	buy := at(order("u", Buy, Market, IOC, "", ""), 0)
	buy.QuoteAmount = d("36010") // 0.5 at 60010 (30005), then 0.1 at 60020 (6002), 3 USDT of dust
	expect(t, b.Place(buy), "T 0.5@60010; T 0.1@60020; "+buy.ID+" FILLED 0.6/36007")
}

func TestLimitOrdersTakeTheReferencePriceThenRest(t *testing.T) {
	b := NewBook("BTC-USDT")
	b.Reference(ref())
	buy := at(houseOnly(limit("u", Buy, "60015", "0.7")), 0)
	evs := b.Place(buy)
	// Not worse than the limit: 0.5 at HOUSE's 60010, the rest rests (60020 is above 60015).
	expect(t, evs, "T 0.5@60010; "+buy.ID+" PARTIALLY_FILLED 0.5/30005")
	if evs[0].Trade.BuyerLimit.String() != "60015" || evs[0].Trade.BuyerFee.String() != "0.001" {
		t.Fatalf("trade %+v", evs[0].Trade)
	}
	if !b.Resting(buy.ID) {
		t.Fatal("the rest of the order rests")
	}
}

func TestHouseOnlyOrdersSkipOtherUsers(t *testing.T) {
	b := NewBook("BTC-USDT")
	b.Reference(ref())
	resting := limit("a", Sell, "60000", "0.1") // better than HOUSE's ask
	b.Place(resting)
	buy := at(houseOnly(limit("u", Buy, "60010", "0.2")), 0)
	expect(t, b.Place(buy), "T 0.2@60010; "+buy.ID+" FILLED 0.2/12002")
	if !b.Resting(resting.ID) {
		t.Fatal("the other user's order is untouched")
	}
	// Without HouseOnly the best price wins, resting orders first at a tie.
	tie := limit("a", Sell, "60010", "0.1")
	b.Place(tie)
	buy2 := at(limit("u", Buy, "60010", "0.4"), 0)
	expect(t, b.Place(buy2), "T 0.1@60000; "+resting.ID+" FILLED 0.1/6000; T 0.1@60010; "+tie.ID+" FILLED 0.1/6001; T 0.2@60010; "+buy2.ID+" FILLED 0.4/24003")
}

func TestStaleReferencesServeNoOrder(t *testing.T) {
	b := NewBook("BTC-USDT")
	b.Reference(ref())
	late := at(order("u", Buy, Market, IOC, "", "0.1"), RefMaxAge+time.Millisecond)
	late.QuoteAmount = d("7000")
	expect(t, b.Place(late), late.ID+" REJECTED 0/0 ORDER_NO_LIQUIDITY")
	undated := order("u", Sell, Market, IOC, "", "0.1") // a command from before references: no HOUSE
	expect(t, b.Place(undated), undated.ID+" REJECTED 0/0 ORDER_NO_LIQUIDITY")
	own := at(order("house", Sell, Market, IOC, "", "0.1"), 0) // HOUSE never trades with itself
	expect(t, b.Place(own), own.ID+" REJECTED 0/0 ORDER_NO_LIQUIDITY")
}

func TestReferenceUpdatesFillTheOrdersTheyReach(t *testing.T) {
	b := NewBook("BTC-USDT")
	b.Reference(ref())
	buy1 := at(houseOnly(limit("u", Buy, "60000", "0.3")), 0)
	buy2 := at(houseOnly(limit("v", Buy, "59995", "0.4")), 0)
	sell := at(houseOnly(limit("w", Sell, "60100", "0.2")), 0)
	for _, o := range []Order{buy1, buy2, sell} {
		expect(t, b.Place(o), o.ID+" OPENED 0/0")
	}
	// HOUSE's ask falls to 59995 with 0.5 there: the best buy fills first,
	// at its own limit, then the next one as far as the level lasts.
	r := ref()
	r.Asks = []RefLevel{{d("59995"), d("0.5")}, {d("60030"), d("1")}}
	r.At = t0.Add(time.Second)
	evs := b.Reference(r)
	expect(t, evs, "T 0.3@60000; "+buy1.ID+" FILLED 0.3/18000; T 0.2@59995; "+buy2.ID+" PARTIALLY_FILLED 0.2/11999")
	tr := evs[0].Trade
	if tr.HouseSide != Sell || tr.SellUserID != "house" || tr.TakerSide != Sell || !tr.BuyerIsMaker || tr.BuyerFee.String() != "0.0003" {
		t.Fatalf("the resting order is the maker: %+v", tr)
	}
	if !b.Resting(buy2.ID) || !b.Resting(sell.ID) {
		t.Fatal("the rest keeps resting")
	}
	// The sell is reached when HOUSE's bid climbs to it; the room caps it.
	r2 := ref()
	r2.Bids, r2.BuyRoom = []RefLevel{{d("60100"), d("5")}}, d("0.15")
	expect(t, b.Reference(r2), "T 0.15@60100; "+sell.ID+" PARTIALLY_FILLED 0.15/9015")
}

func TestReferenceLevelsTradeInWholeLots(t *testing.T) {
	b := NewBook("BTC-USDT")
	r := ref()
	r.Asks = []RefLevel{{d("60010"), d("0.00005")}, {d("60020"), d("0.00025")}} // lot 0.0001
	b.Reference(r)
	buy := at(limit("u", Buy, "60020", "0.0005"), 0)
	expect(t, b.Place(buy), "T 0.0002@60020; "+buy.ID+" PARTIALLY_FILLED 0.0002/12.004")
}

func TestFillOrKillAndPostOnlyCountTheReference(t *testing.T) {
	b := NewBook("BTC-USDT")
	b.Reference(ref())
	b.Place(limit("a", Sell, "60015", "0.2"))
	fok := at(order("u", Buy, Limit, FOK, "60020", "0.9"), 0) // 0.5 HOUSE + 0.2 user + 0.2 HOUSE
	evs := b.Place(fok)
	if last := evs[len(evs)-1]; last.Kind != KindFilled {
		t.Fatalf("FOK: %s", summary(evs))
	}
	tooBig := at(order("u", Buy, Limit, FOK, "60020", "0.5"), 0) // HOUSE has 0.1 of its room left
	expect(t, b.Place(tooBig), tooBig.ID+" CANCELED 0/0 FOK")
	post := at(order("u", Sell, Limit, PostOnly, "59990", "0.1"), 0)
	expect(t, b.Place(post), post.ID+" REJECTED 0/0 ORDER_WOULD_TAKE")
}

func TestSnapshotsKeepTheReference(t *testing.T) {
	b := NewBook("BTC-USDT")
	b.Reference(ref())
	b.Place(at(order("u", Sell, Market, IOC, "", "0.2"), 0))
	c := Restore(b.Snapshot())
	next := at(order("u", Sell, Market, IOC, "", "0.4"), 0)
	got, want := summary(c.Place(next)), summary(b.Place(next))
	if got != want || got != "T 0.3@59990; T 0.1@59980; "+next.ID+" FILLED 0.4/23995" {
		t.Fatalf("restored %s, original %s", got, want)
	}
	if !c.ref.BuyRoom.Equal(decimal.RequireFromString("0.2")) {
		t.Fatalf("room %s", c.ref.BuyRoom)
	}
}
