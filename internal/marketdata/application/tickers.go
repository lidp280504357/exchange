package application

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

// Tickers picks the ticker each symbol shows (ADR-0010): its reference
// market's while market.reference_ticker is on for it and one has been
// received, however old (a client tells a stalled feed by updated_at);
// the platform's otherwise. A contract shows its own perpetual's (coin-M
// design §3.2), whose futures ticker has no best bid and ask: those come
// from the contract's reference book (UseBooks).
type Tickers struct {
	svc         *Service
	feed        *ReferenceFeed // nil without a feed
	refs        *ReferenceMap
	flags       Flags
	instruments ports.Instruments
	book        func(symbol string, limit int) (bids, asks []domain.Level, ok bool)
	// overlay is the price events' factors (nil: none; WithOverlay).
	overlay *Overlay

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

// WithOverlay has the reference tickers carry the price events' factors
// (design 2026-10-07, general price control): a pair's own, a perpetual's
// from its index pair while the overlay reaches risk.
func (t *Tickers) WithOverlay(o *Overlay) { t.overlay = o }

// UseBooks has the contracts' reference tickers take their best bid and
// ask from the reference books (Books.Levels). Call it before serving.
func (t *Tickers) UseBooks(levels func(symbol string, limit int) (bids, asks []domain.Level, ok bool)) {
	t.book = levels
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
	if ref.Market != ports.MarketSpot {
		bids, asks, ok := []domain.Level(nil), []domain.Level(nil), false
		if t.book != nil {
			bids, asks, ok = t.book(symbol, 1)
		}
		if !ok { // the reference book unusable: the engine's (review EL C37)
			bids, asks = t.svc.Book(symbol)
		}
		if len(bids) > 0 {
			tk.Bid = bids[0].Price
		}
		if len(asks) > 0 {
			tk.Ask = asks[0].Price
		}
	}
	if t.overlay != nil {
		// A perpetual's best bid and ask come from its book, already
		// scaled; a pair's are the reference market's.
		f, spot := t.overlay.RiskFactor(domain.IndexPairOf(symbol)), ref.Market == ports.MarketSpot
		if spot {
			f, _ = t.overlay.Factor(symbol)
		}
		tk = t.overlay.Ticker(symbol, tk, f, spot)
	}
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

// All returns the ticker of every listed pair and contract but the
// contracts still in PREPARE (review EL C37: the coin-margined ones before
// they open).
func (t *Tickers) All(ctx context.Context) ([]domain.Ticker, error) {
	list, err := t.svc.Tickers(ctx)
	if err != nil {
		return nil, err
	}
	hidden := t.preparing(ctx)
	mapping := t.mapping(ctx)
	out := list[:0]
	for _, tk := range list {
		if hidden[tk.Symbol] {
			continue
		}
		if ref, ok := t.reference(mapping, tk.Symbol); ok {
			tk = ref
		}
		out = append(out, tk)
	}
	return out, nil
}

// preparing lists the contracts in PREPARE; none while the contracts
// cannot be read.
func (t *Tickers) preparing(ctx context.Context) map[string]bool {
	contracts, err := t.instruments.Contracts(ctx)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, c := range contracts {
		if c.Status == "PREPARE" {
			out[c.Symbol] = true
		}
	}
	return out
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
	hidden := t.preparing(ctx)
	served := map[string]domain.Ticker{}
	for symbol := range mapping {
		if tk, ok := t.reference(mapping, symbol); ok && !hidden[symbol] {
			served[symbol] = tk
		}
	}
	out := make([]Update, 0, len(updates)+len(served))
	for _, u := range updates {
		if _, isTicker := u.Message.(*marketv1.TickerUpdated); isTicker {
			if _, ok := served[u.Symbol]; ok || hidden[u.Symbol] {
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
