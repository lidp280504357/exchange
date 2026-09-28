package domain

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var n int

// order builds a BTC-USDT order: taker fee 0.2%, maker 0.1%.
func order(user string, side Side, typ Type, tif TimeInForce, price, qty string) Order {
	n++
	o := Order{
		ID: fmt.Sprintf("o%03d", n), UserID: user, Symbol: "BTC-USDT", Side: side, Type: typ, TimeInForce: tif,
		STP: CancelNewest, MakerFeeRate: d("0.001"), TakerFeeRate: d("0.002"), BaseDecimals: 8, QuoteDecimals: 6,
		LotSize: d("0.0001"), BaseAsset: "BTC", QuoteAsset: "USDT",
	}
	if price != "" {
		o.Price = d(price)
	}
	if qty != "" {
		o.Quantity = d(qty)
	}
	return o
}

func limit(user string, side Side, price, qty string) Order {
	return order(user, side, Limit, GTC, price, qty)
}

// summary writes events compactly: "T 100@60000", "o001 FILLED 0.1/6000".
func summary(evs []Event) string {
	var parts []string
	for _, e := range evs {
		if e.Kind == KindTrade {
			parts = append(parts, fmt.Sprintf("T %s@%s", e.Trade.Quantity, e.Trade.Price))
			continue
		}
		s := fmt.Sprintf("%s %s %s/%s", e.OrderID, e.Kind, e.Filled, e.FilledQuote)
		if e.Reason != "" {
			s += " " + e.Reason
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

func expect(t *testing.T, got []Event, want string) {
	t.Helper()
	if s := summary(got); s != want {
		t.Fatalf("events\n got: %s\nwant: %s", s, want)
	}
}

func TestLimitOrdersMatchAtTheRestingPriceInPriceTimeOrder(t *testing.T) {
	b := NewBook("BTC-USDT")
	s1, s2, s3 := limit("a", Sell, "60100", "0.1"), limit("b", Sell, "60000", "0.1"), limit("c", Sell, "60000", "0.2")
	for _, o := range []Order{s1, s2, s3} {
		expect(t, b.Place(o), o.ID+" OPENED 0/0")
	}
	buy := limit("d", Buy, "60100", "0.35")
	// Best price first, then the older order at that price; the maker's price.
	expect(t, b.Place(buy), fmt.Sprintf("T 0.1@60000; %s FILLED 0.1/6000; T 0.2@60000; %s FILLED 0.2/12000; T 0.05@60100; %s PARTIALLY_FILLED 0.05/3005; %s FILLED 0.35/21005",
		s2.ID, s3.ID, s1.ID, buy.ID))
	if bid, ask := b.Best(); !bid.IsZero() || !ask.Equal(d("60100")) {
		t.Fatalf("best %s/%s", bid, ask)
	}
}

func TestPartialFillsRestAsPartiallyFilled(t *testing.T) {
	b := NewBook("BTC-USDT")
	ask := limit("a", Sell, "60000", "0.1")
	b.Place(ask)
	buy := limit("b", Buy, "60000", "0.3")
	expect(t, b.Place(buy), fmt.Sprintf("T 0.1@60000; %s FILLED 0.1/6000; %s PARTIALLY_FILLED 0.1/6000", ask.ID, buy.ID))
	if !b.Resting(buy.ID) || b.Resting(ask.ID) {
		t.Fatal("the rest of the buy rests; the filled ask is gone")
	}
}

func TestTimesInForce(t *testing.T) {
	b := NewBook("BTC-USDT")
	b.Place(limit("a", Sell, "60000", "0.1"))
	ioc := order("b", Buy, Limit, IOC, "60000", "0.3")
	evs := b.Place(ioc)
	if last := evs[len(evs)-1]; last.Kind != KindCanceled || last.Reason != ReasonIOC || !last.Filled.Equal(d("0.1")) || b.Resting(ioc.ID) {
		t.Fatalf("IOC: %s", summary(evs))
	}
	b.Place(limit("a", Sell, "60000", "0.1"))
	fok := order("c", Buy, Limit, FOK, "60000", "0.2")
	expect(t, b.Place(fok), fok.ID+" CANCELED 0/0 FOK")
	fok2 := order("c", Buy, Limit, FOK, "60000", "0.1")
	if evs := b.Place(fok2); evs[len(evs)-1].Kind != KindFilled {
		t.Fatalf("FOK that fits: %s", summary(evs))
	}
	b.Place(limit("a", Sell, "60000", "0.1"))
	post := order("d", Buy, Limit, PostOnly, "60000", "0.1")
	expect(t, b.Place(post), post.ID+" REJECTED 0/0 ORDER_WOULD_TAKE")
	post2 := order("d", Buy, Limit, PostOnly, "59999", "0.1")
	expect(t, b.Place(post2), post2.ID+" OPENED 0/0")
}

func TestMarketOrders(t *testing.T) {
	b := NewBook("BTC-USDT")
	empty := order("x", Buy, Market, IOC, "", "")
	empty.QuoteAmount = d("100")
	expect(t, b.Place(empty), empty.ID+" REJECTED 0/0 ORDER_NO_LIQUIDITY")

	a1, a2 := limit("a", Sell, "60000", "0.001"), limit("a", Sell, "60010", "1")
	b.Place(a1)
	b.Place(a2)
	buy := order("b", Buy, Market, IOC, "", "")
	buy.QuoteAmount = d("100")
	// 0.001 at 60000 costs 60; the 40 left buys 0.0006 at 60010 (36.006),
	// and the 3.994 left is less than a lot: the order is filled.
	expect(t, b.Place(buy), fmt.Sprintf("T 0.001@60000; %s FILLED 0.001/60; T 0.0006@60010; %s PARTIALLY_FILLED 0.0006/36.006; %s FILLED 0.0016/96.006",
		a1.ID, a2.ID, buy.ID))

	protected := order("c", Buy, Market, IOC, "", "")
	protected.QuoteAmount, protected.Protection = d("100"), d("60005")
	expect(t, b.Place(protected), protected.ID+" REJECTED 0/0 ORDER_NO_LIQUIDITY")

	b.Place(limit("d", Buy, "59000", "0.01"))
	sell := order("e", Sell, Market, IOC, "", "0.02")
	evs := b.Place(sell)
	if last := evs[len(evs)-1]; last.Kind != KindCanceled || last.Reason != ReasonNoLiquidity || !last.Filled.Equal(d("0.01")) {
		t.Fatalf("market sell beyond the book: %s", summary(evs))
	}
}

func TestSelfTradePrevention(t *testing.T) {
	b := NewBook("BTC-USDT")
	mine := limit("u", Sell, "60000", "0.1")
	b.Place(mine)
	newest := limit("u", Buy, "60000", "0.1")
	expect(t, b.Place(newest), newest.ID+" REJECTED 0/0 ORDER_SELF_TRADE")
	if !b.Resting(mine.ID) {
		t.Fatal("CANCEL_NEWEST keeps the resting order")
	}

	oldest := limit("u", Buy, "60000", "0.1")
	oldest.STP = CancelOldest
	expect(t, b.Place(oldest), fmt.Sprintf("%s CANCELED 0/0 SELF_TRADE; %s OPENED 0/0", mine.ID, oldest.ID))

	other := limit("v", Sell, "60000", "0.05")
	b.Place(other) // v sells 0.05 into u's resting buy
	both := limit("u", Sell, "59000", "0.2")
	both.STP = CancelBoth
	expect(t, b.Place(both), fmt.Sprintf("%s CANCELED 0.05/3000 SELF_TRADE; %s REJECTED 0/0 ORDER_SELF_TRADE", oldest.ID, both.ID))
	if bid, _ := b.Best(); !bid.IsZero() {
		t.Fatalf("an emptied level stays: best bid %s", bid)
	}
}

func TestFeesAndTrades(t *testing.T) {
	b := NewBook("BTC-USDT")
	maker := limit("m", Sell, "60000.01", "0.0003")
	b.Place(maker)
	taker := limit("t", Buy, "60001", "0.0003")
	evs := b.Place(taker)
	tr := evs[0].Trade
	// Buyer (taker, 0.2%) pays 0.0003 x 0.002 = 0.0000006 BTC; seller
	// (maker, 0.1%) 18.000003 x 0.001 = 0.018000003 USDT, rounded up.
	if !tr.Quote.Equal(d("18.000003")) || !tr.BuyerFee.Equal(d("0.0000006")) || !tr.SellerFee.Equal(d("0.018001")) ||
		tr.BuyerIsMaker || tr.TakerSide != Buy || tr.BuyOrderID != taker.ID || tr.SellUserID != "m" ||
		!tr.BuyerLimit.Equal(d("60001")) || tr.BaseAsset != "BTC" || tr.QuoteAsset != "USDT" {
		t.Fatalf("trade: %+v", tr)
	}
	if tr.ID != tradeID("BTC-USDT", tr.Seq) || tr.ID == tradeID("BTC-USDT", tr.Seq+1) {
		t.Fatal("trade IDs derive from symbol and sequence")
	}
}

func TestCancel(t *testing.T) {
	b := NewBook("BTC-USDT")
	o := limit("a", Buy, "59000", "0.1")
	b.Place(o)
	if evs := b.Cancel(o.ID, "someone else"); evs != nil {
		t.Fatal("only the owner cancels")
	}
	expect(t, b.Cancel(o.ID, "a"), o.ID+" CANCELED 0/0 USER")
	if evs := b.Cancel(o.ID, "a"); evs != nil || b.Resting(o.ID) {
		t.Fatal("a canceled order is gone")
	}
	if bid, _ := b.Best(); !bid.IsZero() {
		t.Fatal("the empty level is removed")
	}
	if evs := b.Place(o); len(evs) != 1 || b.Place(o) != nil {
		t.Fatal("placing a resting order again is ignored")
	}
}

func TestSequencesRiseAndSnapshotsRestore(t *testing.T) {
	b := NewBook("BTC-USDT")
	var seqs []int64
	for _, o := range []Order{limit("a", Sell, "60000", "0.1"), limit("b", Sell, "60000", "0.1"), limit("c", Buy, "59000", "0.2"), limit("d", Buy, "60000", "0.15")} {
		for _, e := range b.Place(o) {
			seqs = append(seqs, e.Seq)
		}
	}
	for i, s := range seqs {
		if s != int64(i+1) {
			t.Fatalf("sequences %v", seqs)
		}
	}
	r := Restore(b.Snapshot())
	if !reflect.DeepEqual(r.Snapshot(), b.Snapshot()) {
		t.Fatal("snapshot round trip")
	}
	next := limit("e", Sell, "59000", "0.3")
	if a, c := summary(b.Place(next)), summary(r.Place(next)); a != c {
		t.Fatalf("a restored book behaves the same:\n%s\n%s", a, c)
	}
}

// randomCommands is a reproducible mix of orders and cancels by a few
// users around one price.
func randomCommands(seed uint64, count int) []func(*Book) []Event {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // replays need a seeded generator
	var placed []Order
	var cmds []func(*Book) []Event
	for i := range count {
		if len(placed) > 0 && rng.IntN(5) == 0 {
			o := placed[rng.IntN(len(placed))]
			cmds = append(cmds, func(b *Book) []Event { return b.Cancel(o.ID, o.UserID) })
			continue
		}
		side := Buy
		if rng.IntN(2) == 0 {
			side = Sell
		}
		user := fmt.Sprintf("u%d", rng.IntN(4))
		price := decimal.New(int64(59990+rng.IntN(21)), 0)
		qty := decimal.New(int64(1+rng.IntN(50)), -4)
		o := Order{
			ID: fmt.Sprintf("r%d-%d", seed, i), UserID: user, Symbol: "BTC-USDT", Side: side, Type: Limit, TimeInForce: GTC,
			STP: []STP{CancelNewest, CancelOldest, CancelBoth}[rng.IntN(3)], Price: price, Quantity: qty,
			MakerFeeRate: d("0.001"), TakerFeeRate: d("0.002"), BaseDecimals: 8, QuoteDecimals: 6, LotSize: d("0.0001"),
		}
		switch rng.IntN(10) {
		case 0:
			o.TimeInForce = IOC
		case 1:
			o.TimeInForce = FOK
		case 2:
			o.TimeInForce = PostOnly
		case 3:
			o.Type, o.Price, o.TimeInForce = Market, decimal.Zero, IOC
			if side == Buy {
				o.Quantity, o.QuoteAmount = decimal.Zero, decimal.New(int64(10+rng.IntN(300)), 0)
			}
		}
		placed = append(placed, o)
		cmds = append(cmds, func(b *Book) []Event { return b.Place(o) })
	}
	return cmds
}

func TestReplaysAreDeterministic(t *testing.T) {
	for seed := range uint64(20) {
		cmds := randomCommands(seed, 400)
		run := func(from *Book, part []func(*Book) []Event) (*Book, string) {
			var all []string
			for _, c := range part {
				for _, e := range c(from) {
					all = append(all, fmt.Sprintf("%d %s", e.Seq, summary([]Event{e})))
				}
				checkBook(t, from)
			}
			return from, strings.Join(all, "\n")
		}
		a, outA := run(NewBook("BTC-USDT"), cmds)
		b, outB := run(NewBook("BTC-USDT"), cmds)
		if outA != outB || !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) {
			t.Fatalf("seed %d: the same commands gave different results", seed)
		}
		// Snapshot half-way, restore, and finish: the same as running on.
		half := len(cmds) / 2
		c, outFirst := run(NewBook("BTC-USDT"), cmds[:half])
		restored, outSecond := run(Restore(c.Snapshot()), cmds[half:])
		if outFirst+"\n"+outSecond != outA || !reflect.DeepEqual(restored.Snapshot(), a.Snapshot()) {
			t.Fatalf("seed %d: a restored book diverged", seed)
		}
	}
}

// checkBook asserts the book's invariants: never crossed, levels in
// order, no empty levels, positive rests, and an index that matches.
func checkBook(t *testing.T, b *Book) {
	t.Helper()
	bid, ask := b.Best()
	if !bid.IsZero() && !ask.IsZero() && !bid.LessThan(ask) {
		t.Fatalf("crossed book: bid %s ask %s", bid, ask)
	}
	count := 0
	for side, levels := range map[string][]*level{"bids": b.bids, "asks": b.asks} {
		for i, lvl := range levels {
			if len(lvl.orders) == 0 {
				t.Fatalf("empty level in %s", side)
			}
			if i > 0 && (side == "bids") != levels[i-1].price.GreaterThan(lvl.price) {
				t.Fatalf("%s out of order", side)
			}
			for _, o := range lvl.orders {
				if !o.Remaining().IsPositive() || b.index[o.ID] != o || !o.Price.Equal(lvl.price) {
					t.Fatalf("bad resting order %+v", o)
				}
				count++
			}
		}
	}
	if count != len(b.index) {
		t.Fatalf("index has %d orders, levels %d", len(b.index), count)
	}
}
