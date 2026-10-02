package application

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
)

// SourcePlatform names the platform's own market as an index source.
const SourcePlatform = "platform"

// PlatformIndexAge is how old the platform's last trade may be to price an
// index when the engine's book is one-sided or empty.
const PlatformIndexAge = 5 * time.Minute

// PlatformIndex gives the index sources of a contract: the reference
// market's for an index pair it follows, the platform's own market for
// one it does not (the platform coin's ASTRA-USDT, ASTRA design §5.2):
// the middle of the engine's book, or a trade of the last PlatformIndexAge.
// A followed pair never falls back to the platform's book, which is
// HOUSE's copy of the reference market's.
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

// PlatformPrice is symbol's price on the platform's own market: the middle
// of the engine's best bid and ask, else a trade of the last
// PlatformIndexAge.
func (s *Service) PlatformPrice(symbol string) (decimal.Decimal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.symbols[symbol]
	if !ok {
		return decimal.Zero, false
	}
	if d := st.depth; d != nil && len(d.GetBids()) > 0 && len(d.GetAsks()) > 0 {
		bid, err1 := decimal.NewFromString(d.GetBids()[0].GetPrice())
		ask, err2 := decimal.NewFromString(d.GetAsks()[0].GetPrice())
		if err1 == nil && err2 == nil && bid.IsPositive() && ask.GreaterThan(bid) {
			return bid.Add(ask).Div(decimal.NewFromInt(2)), true
		}
	}
	if st.last.IsPositive() && s.now().Sub(st.lastAt) <= PlatformIndexAge {
		return st.last, true
	}
	return decimal.Zero, false
}
