package application

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Tickers picks the ticker each symbol shows (ADR-0010): its reference
// market's while market.reference_ticker is on for it and one has been
// received, however old (a client tells a stalled feed by updated_at);
// the platform's otherwise. A contract shows its index pair's reference
// ticker until the perpetual streams arrive.
type Tickers struct {
	svc         *Service
	feed        *ReferenceFeed // nil without a feed
	refs        *ReferenceMap
	flags       Flags
	instruments ports.Instruments

	mu     sync.Mutex
	pushed map[string]time.Time // the reference ticker last pushed, by symbol
}

// Push notes: a symbol that stops showing its reference ticker gets the
// platform's pushed again at once (Service.RepushTicker), or clients
// would keep the last reference ticker until the platform's next changed.

// NewTickers combines the platform's tickers with feed's; feed may be nil.
func NewTickers(svc *Service, feed *ReferenceFeed, refs *ReferenceMap, fl Flags, instruments ports.Instruments) *Tickers {
	return &Tickers{svc: svc, feed: feed, refs: refs, flags: fl, instruments: instruments, pushed: map[string]time.Time{}}
}

func (t *Tickers) on(symbol string) bool {
	return t.feed != nil && t.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{}) &&
		t.flags.Enabled(flags.KeyReferenceTicker, flags.Subject{Symbol: symbol})
}

// reference returns symbol's reference ticker when it shows one.
func (t *Tickers) reference(mapping map[string]ports.Reference, symbol string) (domain.Ticker, bool) {
	ref, ok := mapping[symbol]
	if !ok || !t.on(symbol) {
		return domain.Ticker{}, false
	}
	tk, ok := t.feed.Ticker(ref.Symbol)
	if !ok {
		return domain.Ticker{}, false
	}
	tk.Symbol = symbol
	return tk, true
}

func (t *Tickers) mapping(ctx context.Context) map[string]ports.Reference {
	if t.feed == nil {
		return nil
	}
	return t.refs.Get(ctx)
}

// Ticker returns the ticker symbol shows.
func (t *Tickers) Ticker(ctx context.Context, symbol string) (domain.Ticker, error) {
	own, err := t.svc.Ticker(ctx, symbol)
	if err != nil {
		return domain.Ticker{}, err
	}
	if ref, ok := t.reference(t.mapping(ctx), symbol); ok {
		return ref, nil
	}
	return own, nil
}

// All returns the ticker of every listed pair and contract.
func (t *Tickers) All(ctx context.Context) ([]domain.Ticker, error) {
	list, err := t.svc.Tickers(ctx)
	if err != nil {
		return nil, err
	}
	mapping := t.mapping(ctx)
	for i := range list {
		if ref, ok := t.reference(mapping, list[i].Symbol); ok {
			list[i] = ref
		}
	}
	return list, nil
}

// Ranks returns the base asset's rank of each listed pair and contract.
func (t *Tickers) Ranks(ctx context.Context) (map[string]int32, error) {
	return t.instruments.Ranks(ctx)
}

// Summary is the market overview of the home page: the top movers and
// the most traded pairs.
type Summary struct {
	Gainers  []domain.Ticker
	Losers   []domain.Ticker
	Turnover []domain.Ticker
}

// Summary ranks the USDT pairs that trade and have a price: gainers by
// 24-hour change down, losers up, turnover by quote volume down; n each.
func (t *Tickers) Summary(ctx context.Context, n int) (Summary, error) {
	pairs, err := t.instruments.Pairs(ctx)
	if err != nil {
		return Summary{}, err
	}
	trading := map[string]bool{}
	for _, p := range pairs {
		if p.Quote == "USDT" && p.Status == "TRADING" {
			trading[p.Symbol] = true
		}
	}
	all, err := t.All(ctx)
	if err != nil {
		return Summary{}, err
	}
	var list []domain.Ticker
	for _, tk := range all {
		if trading[tk.Symbol] && tk.Last.IsPositive() {
			list = append(list, tk)
		}
	}
	top := func(less func(a, b domain.Ticker) int) []domain.Ticker {
		out := slices.Clone(list)
		slices.SortStableFunc(out, func(a, b domain.Ticker) int {
			if c := less(a, b); c != 0 {
				return c
			}
			return cmp.Compare(a.Symbol, b.Symbol)
		})
		return out[:min(n, len(out))]
	}
	return Summary{
		Gainers:  top(func(a, b domain.Ticker) int { return b.Change.Cmp(a.Change) }),
		Losers:   top(func(a, b domain.Ticker) int { return a.Change.Cmp(b.Change) }),
		Turnover: top(func(a, b domain.Ticker) int { return b.QuoteVolume.Cmp(a.QuoteVolume) }),
	}, nil
}

// Push swaps the platform's ticker updates of the symbols that show a
// reference ticker for that ticker, pushed whenever a newer one arrived.
func (t *Tickers) Push(ctx context.Context, updates []Update) []Update {
	mapping := t.mapping(ctx)
	served := map[string]domain.Ticker{}
	for symbol := range mapping {
		if tk, ok := t.reference(mapping, symbol); ok {
			served[symbol] = tk
		}
	}
	out := make([]Update, 0, len(updates)+len(served))
	for _, u := range updates {
		if _, ok := served[u.Symbol]; ok {
			if _, isTicker := u.Message.(*marketv1.TickerUpdated); isTicker {
				continue
			}
		}
		out = append(out, u)
	}
	symbols := make([]string, 0, len(served))
	for s := range served {
		symbols = append(symbols, s)
	}
	slices.Sort(symbols)
	t.mu.Lock()
	defer t.mu.Unlock()
	for symbol := range t.pushed {
		if _, ok := served[symbol]; !ok {
			delete(t.pushed, symbol)
			t.svc.RepushTicker(symbol)
		}
	}
	for _, symbol := range symbols {
		tk := served[symbol]
		if !tk.At.After(t.pushed[symbol]) {
			continue
		}
		t.pushed[symbol] = tk.At
		out = append(out, Update{symbol, &marketv1.TickerUpdated{Ticker: TickerProto(tk, tk.At)}})
	}
	return out
}
