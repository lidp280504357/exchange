package domain

import (
	"sort"

	"github.com/shopspring/decimal"
)

// level is the resting orders at one price, oldest first.
type level struct {
	price  decimal.Decimal
	orders []*Order
}

// Book is the order book of one symbol. It is not safe for concurrent
// use: the engine applies one command at a time per symbol.
type Book struct {
	Symbol string
	// Seq is the sequence of the last event emitted.
	Seq int64
	// Trades counts the trades so far; each trade carries its number.
	Trades uint64
	bids   []*level // best (highest) first
	asks   []*level // best (lowest) first
	index  map[string]*Order
}

// NewBook returns an empty book.
func NewBook(symbol string) *Book {
	return &Book{Symbol: symbol, index: map[string]*Order{}}
}

// Resting reports whether the order rests on the book.
func (b *Book) Resting(orderID string) bool { _, ok := b.index[orderID]; return ok }

// Best returns the best bid and ask, zero when a side is empty.
func (b *Book) Best() (bid, ask decimal.Decimal) {
	if len(b.bids) > 0 {
		bid = b.bids[0].price
	}
	if len(b.asks) > 0 {
		ask = b.asks[0].price
	}
	return bid, ask
}

func (b *Book) next() int64 {
	b.Seq++
	return b.Seq
}

func (b *Book) opposite(s Side) *[]*level {
	if s == Buy {
		return &b.asks
	}
	return &b.bids
}

func (b *Book) own(s Side) *[]*level {
	if s == Buy {
		return &b.bids
	}
	return &b.asks
}

// Place matches an incoming order against the book (price-time priority,
// at the resting price) and rests what is left when its time in force
// allows. It returns the events in order: per fill a trade and the resting
// order's change, then the incoming order's outcome. A replayed order that
// already rests yields nothing.
func (b *Book) Place(in Order) []Event {
	if b.Resting(in.ID) {
		return nil
	}
	o := &in
	o.Filled, o.FilledQuote = decimal.Zero, decimal.Zero
	opp := b.opposite(o.Side)
	switch {
	case o.Type == Market && (len(*opp) == 0 || !o.crosses((*opp)[0].price)):
		return []Event{b.orderEvent(KindRejected, o, RejectNoLiquidity, "")}
	case o.TimeInForce == PostOnly && len(*opp) > 0 && o.crosses((*opp)[0].price):
		return []Event{b.orderEvent(KindRejected, o, RejectWouldTake, "")}
	case o.TimeInForce == FOK && !b.canFill(o):
		return []Event{b.orderEvent(KindCanceled, o, ReasonFOK, "")}
	}

	var out []Event
	var lastTrade string
	selfTrade, dust := false, false
match:
	for len(*opp) > 0 && !b.done(o) {
		lvl := (*opp)[0]
		if !o.crosses(lvl.price) {
			break
		}
		for len(lvl.orders) > 0 && !b.done(o) {
			maker := lvl.orders[0]
			if maker.UserID == o.UserID {
				switch o.STP {
				case CancelOldest:
					out = append(out, b.dropFront(lvl, ReasonSelfTrade))
					continue
				case CancelBoth:
					out = append(out, b.dropFront(lvl, ReasonSelfTrade))
				}
				selfTrade = true
				break match
			}
			qty := decimal.Min(maker.Remaining(), o.Remaining())
			if o.Type == Market && o.Side == Buy {
				qty = decimal.Min(maker.Remaining(), affordable(o.RemainingQuote(), lvl.price, o.lot()))
				if !qty.IsPositive() {
					dust = true // the rest cannot buy a lot here
					break match
				}
			}
			ev := b.fill(o, maker, lvl.price, qty)
			lastTrade = ev[0].Trade.ID
			out = append(out, ev...)
			if maker.Remaining().IsZero() {
				lvl.orders = lvl.orders[1:]
				delete(b.index, maker.ID)
			}
		}
		if len(lvl.orders) == 0 {
			*opp = (*opp)[1:]
		}
	}
	// CANCEL_BOTH may have emptied the level matching stopped at.
	for len(*opp) > 0 && len((*opp)[0].orders) == 0 {
		*opp = (*opp)[1:]
	}

	switch {
	case b.done(o) || (dust && o.Filled.IsPositive()):
		out = append(out, b.orderEvent(KindFilled, o, "", lastTrade))
	case selfTrade && o.Filled.IsZero():
		out = append(out, b.orderEvent(KindRejected, o, RejectSelfTrade, ""))
	case selfTrade:
		out = append(out, b.orderEvent(KindCanceled, o, ReasonSelfTrade, lastTrade))
	case o.Type == Market && o.Filled.IsZero():
		out = append(out, b.orderEvent(KindRejected, o, RejectNoLiquidity, ""))
	case o.Type == Market:
		out = append(out, b.orderEvent(KindCanceled, o, ReasonNoLiquidity, lastTrade))
	case o.TimeInForce == IOC || o.TimeInForce == FOK:
		out = append(out, b.orderEvent(KindCanceled, o, string(o.TimeInForce), lastTrade))
	default:
		b.rest(o)
		if o.Filled.IsZero() {
			out = append(out, b.orderEvent(KindOpened, o, "", ""))
		} else {
			out = append(out, b.orderEvent(KindPartiallyFilled, o, "", lastTrade))
		}
	}
	return out
}

// affordable is the most whole lots that spend buys at price.
func affordable(spend, price, lot decimal.Decimal) decimal.Decimal {
	q := spend.Div(price).Div(lot).Floor().Mul(lot)
	if q.Mul(price).GreaterThan(spend) { // the division rounded up to a lot boundary
		q = q.Sub(lot)
	}
	return q
}

// done reports whether an incoming order has nothing left to fill.
func (b *Book) done(o *Order) bool {
	if o.Type == Market && o.Side == Buy {
		return !o.RemainingQuote().IsPositive()
	}
	return !o.Remaining().IsPositive()
}

// canFill runs a FOK order against the book without changing it.
func (b *Book) canFill(o *Order) bool {
	need, spend := o.Remaining(), o.RemainingQuote()
	marketBuy := o.Type == Market && o.Side == Buy
	for _, lvl := range *b.opposite(o.Side) {
		if !o.crosses(lvl.price) {
			return false
		}
		for _, maker := range lvl.orders {
			if maker.UserID == o.UserID {
				if o.STP == CancelOldest {
					continue // it would be canceled, not filled
				}
				return false // matching would stop here
			}
			if marketBuy {
				qty := decimal.Min(maker.Remaining(), affordable(spend, lvl.price, o.lot()))
				if !qty.IsPositive() {
					return true // what is left is dust
				}
				spend = spend.Sub(qty.Mul(lvl.price))
				if !spend.IsPositive() {
					return true
				}
				continue
			}
			need = need.Sub(maker.Remaining())
			if !need.IsPositive() {
				return true
			}
		}
	}
	return false
}

// fill trades qty at price between the incoming order and a resting one,
// returning the trade and the resting order's change.
func (b *Book) fill(taker, maker *Order, price, qty decimal.Decimal) []Event {
	quote := price.Mul(qty)
	buyer, seller := taker, maker
	if taker.Side == Sell {
		buyer, seller = maker, taker
	}
	rate := func(o *Order) decimal.Decimal {
		if o == maker {
			return o.MakerFeeRate
		}
		return o.TakerFeeRate
	}
	for _, o := range []*Order{taker, maker} {
		o.Filled, o.FilledQuote = o.Filled.Add(qty), o.FilledQuote.Add(quote)
	}
	seq := b.next()
	b.Trades++
	t := &Trade{
		ID: tradeID(b.Symbol, seq), Number: b.Trades, Symbol: b.Symbol, BaseAsset: taker.BaseAsset, QuoteAsset: taker.QuoteAsset, Seq: seq,
		Price: price, Quantity: qty, Quote: quote, TakerSide: taker.Side,
		BuyOrderID: buyer.ID, BuyUserID: buyer.UserID, SellOrderID: seller.ID, SellUserID: seller.UserID,
		BuyerIsMaker: buyer == maker,
		// Fees round up: the platform-favorable direction (§10.3).
		BuyerFee:  qty.Mul(rate(buyer)).RoundCeil(buyer.BaseDecimals),
		SellerFee: quote.Mul(rate(seller)).RoundCeil(seller.QuoteDecimals),
	}
	if buyer.Type == Limit {
		t.BuyerLimit = buyer.Price
	}
	kind := KindPartiallyFilled
	if maker.Remaining().IsZero() {
		kind = KindFilled
	}
	return []Event{
		{Kind: KindTrade, Seq: seq, Symbol: b.Symbol, Trade: t},
		b.orderEvent(kind, maker, "", t.ID),
	}
}

// dropFront cancels the oldest order of a level.
func (b *Book) dropFront(lvl *level, reason string) Event {
	o := lvl.orders[0]
	lvl.orders = lvl.orders[1:]
	delete(b.index, o.ID)
	return b.orderEvent(KindCanceled, o, reason, "")
}

// rest puts an order on its side of the book, behind the orders at its
// price.
func (b *Book) rest(o *Order) {
	side := b.own(o.Side)
	levels := *side
	better := func(p decimal.Decimal) bool { // p comes before o.Price
		if o.Side == Buy {
			return p.GreaterThan(o.Price)
		}
		return p.LessThan(o.Price)
	}
	i := sort.Search(len(levels), func(i int) bool { return !better(levels[i].price) })
	if i < len(levels) && levels[i].price.Equal(o.Price) {
		levels[i].orders = append(levels[i].orders, o)
	} else {
		levels = append(levels, nil)
		copy(levels[i+1:], levels[i:])
		levels[i] = &level{price: o.Price, orders: []*Order{o}}
		*side = levels
	}
	b.index[o.ID] = o
}

// Cancel removes a resting order of the user. Orders that do not rest
// (already finished, or someone else's) yield nothing.
func (b *Book) Cancel(orderID, userID string) []Event {
	o, ok := b.index[orderID]
	if !ok || o.UserID != userID {
		return nil
	}
	side := b.own(o.Side)
	for li, lvl := range *side {
		if !lvl.price.Equal(o.Price) {
			continue
		}
		for oi, r := range lvl.orders {
			if r.ID == orderID {
				lvl.orders = append(lvl.orders[:oi], lvl.orders[oi+1:]...)
				break
			}
		}
		if len(lvl.orders) == 0 {
			*side = append((*side)[:li], (*side)[li+1:]...)
		}
		break
	}
	delete(b.index, orderID)
	return []Event{b.orderEvent(KindCanceled, o, ReasonUser, "")}
}

func (b *Book) orderEvent(kind Kind, o *Order, reason, trade string) Event {
	return Event{
		Kind: kind, Seq: b.next(), Symbol: b.Symbol, OrderID: o.ID, ClientOrderID: o.ClientOrderID, UserID: o.UserID,
		Filled: o.Filled, FilledQuote: o.FilledQuote, TradeID: trade, Reason: reason,
	}
}

// Snapshot is a book's state: its sequence, trade count and resting
// orders, bids best first, then asks best first, each level oldest first.
type Snapshot struct {
	Symbol string  `json:"symbol"`
	Seq    int64   `json:"seq"`
	Trades uint64  `json:"trades"`
	Orders []Order `json:"orders"`
}

// Snapshot returns the book's state.
func (b *Book) Snapshot() Snapshot {
	s := Snapshot{Symbol: b.Symbol, Seq: b.Seq, Trades: b.Trades}
	for _, side := range [][]*level{b.bids, b.asks} {
		for _, lvl := range side {
			for _, o := range lvl.orders {
				s.Orders = append(s.Orders, *o)
			}
		}
	}
	return s
}

// Restore rebuilds a book from a snapshot.
func Restore(s Snapshot) *Book {
	b := NewBook(s.Symbol)
	b.Seq, b.Trades = s.Seq, s.Trades
	for i := range s.Orders {
		o := s.Orders[i]
		b.rest(&o)
	}
	return b
}
