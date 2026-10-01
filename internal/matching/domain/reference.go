package domain

import (
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

// RefMaxAge is how much older than an order a reference book may be and
// still serve it (ADR-0015): both times travel in the commands, so a
// replay decides the same.
const RefMaxAge = 5 * time.Second

// SettleLag is how long a trade may take to reach HOUSE's balances: an
// update's rooms are taken to miss the fills from that long before its
// holdings were read on (it errs towards less room).
const SettleLag = 2 * time.Second

// RefLevel is what HOUSE offers at one price of a reference book.
type RefLevel struct {
	Price    decimal.Decimal `json:"price"`
	Quantity decimal.Decimal `json:"quantity"`
}

// Reference is HOUSE's virtual liquidity on a book (ADR-0015): the top of
// the reference market's book in the platform's units, capped per level,
// with how much HOUSE may still buy and sell. Fills use it up until the
// next update replaces it.
type Reference struct {
	// Bids are where HOUSE buys (best, highest, first); Asks where it sells
	// (best, lowest, first).
	Bids []RefLevel `json:"bids"`
	Asks []RefLevel `json:"asks"`
	// BuyRoom and SellRoom are the base quantities HOUSE may still buy and
	// sell.
	BuyRoom  decimal.Decimal `json:"buy_room"`
	SellRoom decimal.Decimal `json:"sell_room"`
	// HouseUser is HOUSE's user ID on the trades.
	HouseUser string `json:"house_user"`
	// At is when the update was published.
	At time.Time `json:"at"`
	// HoldingsAt is when HOUSE's holdings behind the rooms were read; zero
	// when the publisher does not say (the rooms are taken as they come).
	HoldingsAt time.Time `json:"holdings_at,omitzero"`
}

// HouseFill is a fill HOUSE made on the book: its side and base quantity,
// at the time of the command that made it.
type HouseFill struct {
	At   time.Time       `json:"at"`
	Side Side            `json:"side"`
	Qty  decimal.Decimal `json:"qty"`
}

// clone copies the levels, which fills use up.
func (r Reference) clone() *Reference {
	r.Bids, r.Asks = slices.Clone(r.Bids), slices.Clone(r.Asks)
	return &r
}

// takes returns the levels and the room an order of side s takes from:
// HOUSE's asks for a buy, its bids for a sell.
func (r *Reference) takes(s Side) (*[]RefLevel, *decimal.Decimal) {
	if s == Buy {
		return &r.Asks, &r.SellRoom
	}
	return &r.Bids, &r.BuyRoom
}

// best is the best price an order of side s can take at least one lot
// at, dropping the levels too small for a lot on the way.
func (r *Reference) best(s Side, lot decimal.Decimal) (decimal.Decimal, bool) {
	if r == nil {
		return decimal.Zero, false
	}
	levels, room := r.takes(s)
	if !floorLot(*room, lot).IsPositive() {
		return decimal.Zero, false
	}
	for len(*levels) > 0 && !floorLot((*levels)[0].Quantity, lot).IsPositive() {
		*levels = (*levels)[1:]
	}
	if len(*levels) == 0 {
		return decimal.Zero, false
	}
	return (*levels)[0].Price, true
}

// floorLot rounds q down to whole lots.
func floorLot(q, lot decimal.Decimal) decimal.Decimal {
	if !lot.IsPositive() {
		return q
	}
	return q.Div(lot).Floor().Mul(lot)
}

// better reports whether price a is better than b for an incoming order
// of side s: lower for a buy, higher for a sell.
func better(s Side, a, b decimal.Decimal) bool {
	if s == Buy {
		return a.LessThan(b)
	}
	return a.GreaterThan(b)
}

// refFor returns the reference liquidity an incoming order may take: the
// book's, while it is at most RefMaxAge older than the order and HOUSE is
// not the order's own user.
func (b *Book) refFor(o *Order) *Reference {
	r := b.ref
	if r == nil || o.At.IsZero() || o.UserID == r.HouseUser || o.At.Sub(r.At) > RefMaxAge {
		return nil
	}
	return r
}

// takeHouse fills an incoming order against HOUSE's best reference level:
// as much as the level, HOUSE's room and the order allow, in whole lots,
// at the level's price. done is false when nothing traded because the
// level or the room held less than a lot (they are dropped, so the caller
// looks again); dust means a market buy cannot afford a lot at this price.
func (b *Book) takeHouse(o *Order, r *Reference) (ev Event, done, dust bool) {
	levels, room := r.takes(o.Side)
	lvl := &(*levels)[0]
	lot := o.lot()
	want := o.Remaining()
	if o.Type == Market && o.Side == Buy {
		want = affordable(o.RemainingQuote(), lvl.Price, lot)
		if !want.IsPositive() {
			return Event{}, false, true
		}
	}
	qty := floorLot(decimal.Min(want, lvl.Quantity, *room), lot)
	if !qty.IsPositive() {
		if !floorLot(*room, lot).IsPositive() {
			*room = decimal.Zero
		} else {
			*levels = (*levels)[1:]
		}
		return Event{}, false, false
	}
	ev = b.houseFill(o, false, r.HouseUser, lvl.Price, qty)
	lvl.Quantity = lvl.Quantity.Sub(qty)
	if !lvl.Quantity.IsPositive() {
		*levels = (*levels)[1:]
	}
	*room = room.Sub(qty)
	return ev, true, false
}

// houseFill trades qty at price between a user's order and HOUSE. The
// user is the maker when a reference update reached its resting order,
// the taker when it came in; HOUSE pays no fee and has no order.
func (b *Book) houseFill(u *Order, userMaker bool, house string, price, qty decimal.Decimal) Event {
	quote := price.Mul(qty)
	u.Filled, u.FilledQuote = u.Filled.Add(qty), u.FilledQuote.Add(quote)
	// HOUSE takes the other side, at the time of the command that traded:
	// the user's order, or HOUSE's update reaching a resting order.
	at := u.At
	if userMaker {
		at = b.ref.At
	}
	b.houseFills = append(b.houseFills, HouseFill{At: at, Side: u.Side.Opposite(), Qty: qty})
	rate := u.TakerFeeRate
	if userMaker {
		rate = u.MakerFeeRate
	}
	seq := b.next()
	b.Trades++
	t := &Trade{
		ID: tradeID(b.Symbol, seq), Number: b.Trades, Symbol: b.Symbol, BaseAsset: u.BaseAsset, QuoteAsset: u.QuoteAsset, Seq: seq,
		Price: price, Quantity: qty, Quote: quote,
	}
	houseTakes := Buy // the side of whoever came in: the user, or HOUSE's update
	if u.Side == Buy {
		t.BuyOrderID, t.BuyUserID, t.SellUserID, t.HouseSide = u.ID, u.UserID, house, Sell
		t.BuyerIsMaker, houseTakes = userMaker, Sell
		// Fees round up: the platform-favorable direction (§10.3).
		t.BuyerFee, t.SellerFee = qty.Mul(rate).RoundCeil(u.BaseDecimals), decimal.Zero
		if u.Type == Limit {
			t.BuyerLimit = u.Price
		}
	} else {
		t.SellOrderID, t.SellUserID, t.BuyUserID, t.HouseSide = u.ID, u.UserID, house, Buy
		t.BuyerIsMaker = !userMaker
		t.BuyerFee, t.SellerFee = decimal.Zero, quote.Mul(rate).RoundCeil(u.QuoteDecimals)
	}
	t.TakerSide = u.Side
	if userMaker {
		t.TakerSide = houseTakes
	}
	return Event{Kind: KindTrade, Seq: seq, Symbol: b.Symbol, Trade: t}
}

// Reference replaces the book's reference liquidity (a ReferenceBookUpdate)
// and fills the resting orders its prices reach (ADR-0015 §4): buys at or
// above HOUSE's best ask, sells at or below its best bid, best price
// first and oldest first at a price, each at its own limit, as far as the
// reference levels that cross it and HOUSE's room allow. The resting
// order is the maker. It returns the trades and the orders' changes.
func (b *Book) Reference(r Reference) []Event {
	b.ref = r.clone()
	b.tightenRooms()
	var out []Event
	for _, side := range []Side{Buy, Sell} {
		out = append(out, b.trigger(side)...)
	}
	return out
}

// tightenRooms takes off the new rooms what HOUSE traded on the book since
// its holdings were read (allowing SettleLag for the settlement): the
// publisher refreshes the holdings every second but sends an update every
// 250 ms, and each update would otherwise give back room already used, so
// HOUSE could sell more than it holds (ADR-0013). Fills from before that
// are in the holdings; they are forgotten.
func (b *Book) tightenRooms() {
	if b.ref.HoldingsAt.IsZero() {
		b.houseFills = nil
		return
	}
	since := b.ref.HoldingsAt.Add(-SettleLag)
	kept := b.houseFills[:0]
	for _, f := range b.houseFills {
		if !f.At.After(since) {
			continue
		}
		kept = append(kept, f)
		if f.Side == Sell {
			b.ref.SellRoom = decimal.Max(b.ref.SellRoom.Sub(f.Qty), decimal.Zero)
		} else {
			b.ref.BuyRoom = decimal.Max(b.ref.BuyRoom.Sub(f.Qty), decimal.Zero)
		}
	}
	b.houseFills = kept
}

// trigger fills the resting orders of one side that HOUSE's reference
// prices cross.
func (b *Book) trigger(side Side) []Event {
	own := b.own(side)
	levels, room := b.ref.takes(side)
	var out []Event
	for len(*own) > 0 && len(*levels) > 0 && room.IsPositive() {
		lvl := (*own)[0]
		if !crossed(side, lvl.price, (*levels)[0].Price) {
			break
		}
		filled := false
		for i := 0; i < len(lvl.orders); i++ {
			maker := lvl.orders[i]
			if maker.UserID == b.ref.HouseUser {
				continue // never a trade with itself
			}
			// What HOUSE offers at prices the order accepts.
			avail := decimal.Zero
			for _, l := range *levels {
				if !crossed(side, maker.Price, l.Price) {
					break
				}
				avail = avail.Add(l.Quantity)
			}
			qty := floorLot(decimal.Min(maker.Remaining(), avail, *room), maker.lot())
			if !qty.IsPositive() {
				continue
			}
			out = append(out, b.houseFill(maker, true, b.ref.HouseUser, maker.Price, qty))
			tradeID := out[len(out)-1].Trade.ID
			consume(levels, qty)
			*room = room.Sub(qty)
			kind := KindPartiallyFilled
			if maker.Remaining().IsZero() {
				kind = KindFilled
				lvl.orders = append(lvl.orders[:i], lvl.orders[i+1:]...)
				delete(b.index, maker.ID)
				i--
			}
			out = append(out, b.orderEvent(kind, maker, "", tradeID))
			filled = true
			if len(*levels) == 0 || !room.IsPositive() {
				break
			}
		}
		if len(lvl.orders) == 0 {
			*own = (*own)[1:]
			continue
		}
		if !filled {
			break // what is left at this price cannot take a lot; worse prices cannot either
		}
	}
	return out
}

// crossed reports whether a resting order of side s at price p is reached
// by HOUSE's reference price ref: a buy at or above HOUSE's ask, a sell at
// or below its bid.
func crossed(s Side, p, ref decimal.Decimal) bool {
	if s == Buy {
		return p.GreaterThanOrEqual(ref)
	}
	return p.LessThanOrEqual(ref)
}

// consume takes qty off the best reference levels.
func consume(levels *[]RefLevel, qty decimal.Decimal) {
	for qty.IsPositive() && len(*levels) > 0 {
		l := &(*levels)[0]
		take := decimal.Min(l.Quantity, qty)
		l.Quantity, qty = l.Quantity.Sub(take), qty.Sub(take)
		if !l.Quantity.IsPositive() {
			*levels = (*levels)[1:]
		}
	}
}
