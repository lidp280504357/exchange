package application

import (
	"context"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
)

// SourcePlatform names the platform's own market as an index source.
const SourcePlatform = "platform"

// The platform's own market as an index source (ASTRA design §5.2, as
// clarified after the A4 review): not the bare middle of the book, which
// one order can move, but the time-weighted average of the last price over
// PlatformTWAP, with the book's middle averaged in only while it is within
// PlatformMidBand of the last trade and both sides of the top of the book
// hold PlatformTopMin worth. Without a trade in the window the qualified
// middle stands in while the last trade is no older than PlatformIndexAge;
// else there is no price, and the contract's mark degrades.
const (
	PlatformTWAP     = 60 * time.Second
	PlatformMidBand  = 0.01
	PlatformIndexAge = 5 * time.Minute
)

// PlatformTopMin is the least worth, in the quote asset, each side of the
// top of the book holds for its middle to count.
var PlatformTopMin = decimal.NewFromInt(100)

// PlatformIndex gives the index sources of a contract: the reference
// market's for an index pair it follows, the platform's own market for
// one it does not (the platform coin's ASTRA-USDT): PlatformPrice. A
// followed pair never falls back to the platform's book, which is HOUSE's
// copy of the reference market's.
type PlatformIndex struct {
	Feed IndexSources
	Svc  *Service
	Refs *ReferenceMap
}

// Prices returns the index sources of symbol.
func (p PlatformIndex) Prices(symbol string) []domain.SourcePrice {
	if _, followed := p.Refs.Get(context.Background())[symbol]; followed {
		return p.Feed.Prices(symbol)
	}
	if price, ok := p.Svc.PlatformPrice(symbol); ok {
		return []domain.SourcePrice{{Source: SourcePlatform, Price: price}}
	}
	return nil
}

// PlatformPrice is symbol's price on the platform's own market as an index
// source: the last PlatformTWAP's time-weighted last price, averaged with
// the book's middle while that qualifies (top of the book on both sides
// worth PlatformTopMin, within PlatformMidBand of the last trade); the
// qualified middle alone without a trade in the window, while the last
// trade is no older than PlatformIndexAge.
func (s *Service) PlatformPrice(symbol string) (decimal.Decimal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.symbols[symbol]
	if !ok {
		return decimal.Zero, false
	}
	now := s.now()
	avg, traded := twap(st.trades, now, PlatformTWAP)
	mid, midOK := topMiddle(st.depth)
	midOK = midOK && st.last.IsPositive() && now.Sub(st.lastAt) <= PlatformIndexAge &&
		mid.Div(st.last).Sub(decimal.NewFromInt(1)).Abs().LessThanOrEqual(decimal.NewFromFloat(PlatformMidBand))
	switch {
	case traded && midOK:
		return avg.Add(mid).Div(decimal.NewFromInt(2)).Round(8), true
	case traded:
		return avg.Round(8), true
	case midOK:
		return mid, true
	}
	return decimal.Zero, false
}

// PlatformMid is the middle of symbol's book on the platform while the
// top of the book on both sides is worth PlatformTopMin.
func (s *Service) PlatformMid(symbol string) (decimal.Decimal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.symbols[symbol]
	if !ok {
		return decimal.Zero, false
	}
	return topMiddle(st.depth)
}

// topMiddle is the middle of a book whose best bid and ask are each worth
// PlatformTopMin.
func topMiddle(d *marketv1.DepthSnapshot) (decimal.Decimal, bool) {
	if d == nil || len(d.GetBids()) == 0 || len(d.GetAsks()) == 0 {
		return decimal.Zero, false
	}
	level := func(l *marketv1.PriceLevel) (decimal.Decimal, bool) {
		p, err1 := decimal.NewFromString(l.GetPrice())
		q, err2 := decimal.NewFromString(l.GetQuantity())
		return p, err1 == nil && err2 == nil && p.IsPositive() && p.Mul(q).GreaterThanOrEqual(PlatformTopMin)
	}
	bid, okBid := level(d.GetBids()[0])
	ask, okAsk := level(d.GetAsks()[0])
	if !okBid || !okAsk || !ask.GreaterThan(bid) {
		return decimal.Zero, false
	}
	return bid.Add(ask).Div(decimal.NewFromInt(2)), true
}

// twap is the time-weighted average of the last price over the window
// before now, from trades (oldest first): each price counts for as long as
// it was the last, from the window's start (or the first trade kept); false
// when no trade fell in the window.
func twap(trades []domain.Trade, now time.Time, window time.Duration) (decimal.Decimal, bool) {
	start := now.Add(-window)
	i := sort.Search(len(trades), func(i int) bool { return !trades[i].At.Before(start) })
	if i == len(trades) {
		return decimal.Zero, false
	}
	from, price := start, trades[i].Price
	if i > 0 {
		price = trades[i-1].Price // the last price when the window opened
	} else {
		from = trades[0].At
	}
	var sum, total decimal.Decimal
	for j := i; j <= len(trades); j++ {
		to := now
		if j < len(trades) {
			to = trades[j].At
		}
		if d := to.Sub(from); d > 0 {
			w := decimal.NewFromInt(d.Nanoseconds())
			sum, total = sum.Add(price.Mul(w)), total.Add(w)
		}
		if j < len(trades) {
			if trades[j].At.After(from) {
				from = trades[j].At
			}
			price = trades[j].Price
		}
	}
	if !total.IsPositive() {
		return trades[len(trades)-1].Price, true
	}
	return sum.Div(total), true
}
