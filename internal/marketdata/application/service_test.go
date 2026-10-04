package application

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

type memStore struct {
	symbols    map[string]ports.SymbolState
	candles    map[string]domain.Candle
	trades     []domain.Trade
	references map[string]domain.Candle
	funding    map[string]ports.FundingPeriod
	halts      map[string]ports.Halt
	simHalts   map[string]ports.Halt
	heartbeats map[string]time.Time
	mu         sync.Mutex // the reference feed writes from its own goroutine
	down       bool
	// outbox holds the emitted events; emitFails makes Emit fail.
	outbox    []*eventv1.Envelope
	emitFails bool
}

func newMemStore() *memStore {
	return &memStore{
		symbols: map[string]ports.SymbolState{}, candles: map[string]domain.Candle{}, references: map[string]domain.Candle{},
		funding: map[string]ports.FundingPeriod{}, halts: map[string]ports.Halt{}, simHalts: map[string]ports.Halt{},
		heartbeats: map[string]time.Time{},
	}
}

func (s *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error {
	if s.down {
		return errors.New("database down")
	}
	return fn(memRepos{s})
}

func (s *memStore) Read() ports.Repos { return memRepos{s} }

type memRepos struct{ s *memStore }

func (r memRepos) Symbols() ports.SymbolRepo { return memSymbols(r) }
func (r memRepos) Candles() ports.CandleRepo { return memCandles(r) }
func (r memRepos) Trades() ports.TradeRepo   { return memTrades(r) }

func (r memRepos) References() ports.ReferenceRepo { return memReferences(r) }

func (r memRepos) Funding() ports.FundingRepo { return memFunding(r) }

func (r memRepos) Halts() ports.HaltRepo    { return memHalts{s: r.s, m: r.s.halts} }
func (r memRepos) SimHalts() ports.HaltRepo { return memHalts{s: r.s, m: r.s.simHalts} }

func (r memRepos) SimHeartbeats() ports.HeartbeatRepo { return memHeartbeats(r) }

type memHeartbeats memRepos

func (r memHeartbeats) List(context.Context) (map[string]time.Time, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return maps.Clone(r.s.heartbeats), nil
}

func (r memHeartbeats) Save(_ context.Context, symbol string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if at.After(r.s.heartbeats[symbol]) {
		r.s.heartbeats[symbol] = at
	}
	return nil
}

type memHalts struct {
	s *memStore
	m map[string]ports.Halt
}

func (r memHalts) List(context.Context) ([]ports.Halt, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := make([]ports.Halt, 0, len(r.m))
	for _, h := range r.m {
		out = append(out, h)
	}
	slices.SortFunc(out, func(a, b ports.Halt) int { return strings.Compare(a.Symbol, b.Symbol) })
	return out, nil
}

func (r memHalts) Add(_ context.Context, symbol string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.m[symbol]; !ok {
		r.m[symbol] = ports.Halt{Symbol: symbol, HaltedAt: at}
	}
	return nil
}

func (r memHalts) Remove(_ context.Context, symbol string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	delete(r.m, symbol)
	return nil
}

func (r memRepos) Emit(_ context.Context, _ string, env *eventv1.Envelope) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if r.s.emitFails || r.s.down {
		return errors.New("database down")
	}
	r.s.outbox = append(r.s.outbox, env)
	return nil
}

// takeOutbox returns the payloads emitted since the last call.
func (s *memStore) takeOutbox(t *testing.T) []proto.Message {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []proto.Message
	for _, env := range s.outbox {
		m, err := env.GetPayload().UnmarshalNew()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	s.outbox = nil
	return out
}

type memFunding memRepos

func fundingKey(symbol string, t time.Time) string {
	return symbol + "|" + t.UTC().Format(time.RFC3339)
}

func (r memFunding) Save(_ context.Context, p ports.FundingPeriod) error {
	if r.s.down {
		return errors.New("database down")
	}
	if old, ok := r.s.funding[fundingKey(p.Symbol, p.FundingTime)]; ok && old.Settled {
		return nil
	}
	r.s.funding[fundingKey(p.Symbol, p.FundingTime)] = p
	return nil
}

func (r memFunding) sorted(keep func(ports.FundingPeriod) bool) []ports.FundingPeriod {
	var out []ports.FundingPeriod
	for _, p := range r.s.funding {
		if keep(p) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].FundingTime.Before(out[b].FundingTime) })
	return out
}

func (r memFunding) Unsettled(context.Context) ([]ports.FundingPeriod, error) {
	if r.s.down {
		return nil, errors.New("database down")
	}
	return r.sorted(func(p ports.FundingPeriod) bool { return !p.Settled }), nil
}

func (r memFunding) Settle(_ context.Context, p ports.FundingPeriod) (bool, error) {
	if r.s.down {
		return false, errors.New("database down")
	}
	if old, ok := r.s.funding[fundingKey(p.Symbol, p.FundingTime)]; ok && old.Settled {
		return false, nil
	}
	p.Settled, p.SettledAt = true, time.Now()
	r.s.funding[fundingKey(p.Symbol, p.FundingTime)] = p
	return true, nil
}

func (r memFunding) Settled(_ context.Context, symbol string, from, to time.Time, limit int) ([]ports.FundingPeriod, error) {
	out := r.sorted(func(p ports.FundingPeriod) bool {
		return p.Settled && p.Symbol == symbol && !p.FundingTime.Before(from) && p.FundingTime.Before(to)
	})
	slices.Reverse(out)
	return out[:min(limit, len(out))], nil
}

type memReferences memRepos

func (r memReferences) Upsert(_ context.Context, source string, list []domain.Candle) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, c := range list {
		r.s.references[source+"|"+candleKey(c)] = c
	}
	return nil
}

func (r memReferences) Latest(_ context.Context, source, symbol string) (*domain.Candle, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var last *domain.Candle
	for k, c := range r.s.references {
		if strings.HasPrefix(k, source+"|") && c.Symbol == symbol && (last == nil || c.OpenTime.After(last.OpenTime)) {
			last = &c
		}
	}
	return last, nil
}

func (r memReferences) Purge(context.Context, time.Time) (int64, error) { return 0, nil }

type (
	memSymbols memRepos
	memCandles memRepos
	memTrades  memRepos
)

func (r memSymbols) All(context.Context) ([]ports.SymbolState, error) {
	var out []ports.SymbolState
	for _, st := range r.s.symbols {
		out = append(out, st)
	}
	return out, nil
}

func (r memSymbols) Save(_ context.Context, st ports.SymbolState) error {
	r.s.symbols[st.Symbol] = st
	return nil
}

func candleKey(c domain.Candle) string {
	return c.Symbol + "|" + string(c.Interval) + "|" + c.OpenTime.Format(time.RFC3339)
}

func (r memCandles) Upsert(_ context.Context, list []domain.Candle) error {
	for _, c := range list {
		r.s.candles[candleKey(c)] = c
	}
	return nil
}

func (r memCandles) sorted(symbol string, i domain.Interval) []domain.Candle {
	var out []domain.Candle
	for _, c := range r.s.candles {
		if c.Symbol == symbol && c.Interval == i {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].OpenTime.Before(out[b].OpenTime) })
	return out
}

func (r memCandles) Latest(_ context.Context, symbol string) ([]domain.Candle, error) {
	var out []domain.Candle
	for _, i := range domain.Intervals {
		if list := r.sorted(symbol, i); len(list) > 0 {
			out = append(out, list[len(list)-1])
		}
	}
	return out, nil
}

func (r memCandles) Range(_ context.Context, symbol string, i domain.Interval, from, to time.Time) ([]domain.Candle, error) {
	var out []domain.Candle
	for _, c := range r.sorted(symbol, i) {
		if !c.OpenTime.Before(from) && c.OpenTime.Before(to) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r memCandles) Before(_ context.Context, symbol string, i domain.Interval, t time.Time) (*domain.Candle, error) {
	var last *domain.Candle
	for _, c := range r.sorted(symbol, i) {
		if c.OpenTime.Before(t) {
			last = &c
		}
	}
	return last, nil
}

func (r memTrades) Insert(_ context.Context, list []domain.Trade) error {
	r.s.trades = append(r.s.trades, list...)
	return nil
}

func (r memTrades) Recent(_ context.Context, symbol string, limit int) ([]domain.Trade, error) {
	var out []domain.Trade
	for i := len(r.s.trades) - 1; i >= 0 && len(out) < limit; i-- {
		if r.s.trades[i].Symbol == symbol {
			out = append(out, r.s.trades[i])
		}
	}
	return out, nil
}

func (r memTrades) Purge(context.Context, time.Time) (int64, error) { return 0, nil }

type pairs []string

func (p pairs) Listed(_ context.Context, symbol string) (bool, error) {
	return slices.Contains(p, symbol), nil
}
func (p pairs) Symbols(context.Context) ([]string, error) { return p, nil }

func (p pairs) Contracts(context.Context) ([]ports.Contract, error) { return nil, nil }

func (p pairs) Pairs(context.Context) ([]ports.Pair, error) {
	out := make([]ports.Pair, 0, len(p))
	for _, s := range p {
		out = append(out, ports.Pair{Symbol: s, Status: "TRADING"})
	}
	return out, nil
}

func (p pairs) Ranks(context.Context) (map[string]int32, error) { return map[string]int32{}, nil }

func (p pairs) SetPairStatus(context.Context, string, string, string) (string, error) {
	return "", ErrUnknownSymbol
}

func (p pairs) SetContractStatus(context.Context, string, string, string) (string, error) {
	return "", ErrUnknownSymbol
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func trade(seq uint64, price, qty, when string) domain.Trade {
	p, q := d(price), d(qty)
	return domain.Trade{
		Symbol: "BTC-USDT", Sequence: int64(seq), ID: "t", Number: seq, Price: p, Quantity: q, Quote: p.Mul(q), //nolint:gosec // small test numbers
		TakerSide: "BUY", At: at(when),
	}
}

func newService(t *testing.T, store *memStore, now *time.Time) *Service {
	t.Helper()
	s := New(store, pairs{"BTC-USDT", "ETH-USDT"}, slog.New(slog.DiscardHandler))
	s.now = func() time.Time { return *now }
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTradesBuildCandlesTickerAndTradeList(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	now := at("2026-09-30T10:03:30Z")
	s := newService(t, store, &now)
	batch := []domain.Trade{
		trade(3, "70000", "0.1", "2026-09-30T10:01:10Z"),
		trade(5, "70500", "0.2", "2026-09-30T10:01:50Z"),
		trade(8, "69800", "0.1", "2026-09-30T10:03:05Z"),
	}
	if fresh, err := s.OnTrades(ctx, batch); err != nil || len(fresh) != 3 {
		t.Fatalf("%d new trades, %v", len(fresh), err)
	}
	// A redelivery changes nothing, and nothing in it is new (to relay).
	if fresh, err := s.OnTrades(ctx, batch); err != nil || len(fresh) != 0 {
		t.Fatalf("redelivered: %+v %v", fresh, err)
	}
	if len(store.trades) != 3 || store.symbols["BTC-USDT"].Sequence != 8 {
		t.Fatalf("stored %d trades, sequence %d", len(store.trades), store.symbols["BTC-USDT"].Sequence)
	}
	hour := store.candles["BTC-USDT|1h|2026-09-30T10:00:00Z"]
	if hour.Trades != 3 || !hour.Open.Equal(d("70000")) || !hour.High.Equal(d("70500")) || !hour.Low.Equal(d("69800")) ||
		!hour.Close.Equal(d("69800")) || !hour.Volume.Equal(d("0.4")) {
		t.Fatalf("1h candle %+v", hour)
	}

	candles, err := s.Candles(ctx, "BTC-USDT", "1m", time.Time{}, time.Time{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	// 10:00 is before the first trade; 10:02 is flat.
	if len(candles) != 3 || candles[0].Trades != 2 || candles[1].Trades != 0 || !candles[1].Close.Equal(d("70500")) || candles[2].Trades != 1 {
		t.Fatalf("1m candles %+v", candles)
	}

	s.OnDepth(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 9, Bids: []*marketv1.PriceLevel{{Price: "69700", Quantity: "1"}}})
	tk, err := s.Ticker(ctx, "BTC-USDT")
	if err != nil {
		t.Fatal(err)
	}
	if !tk.Last.Equal(d("69800")) || !tk.Open.Equal(d("70000")) || tk.Trades != 3 || !tk.Bid.Equal(d("69700")) || !tk.Ask.IsZero() {
		t.Fatalf("ticker %+v", tk)
	}
	list, err := s.Trades(ctx, "BTC-USDT", 2)
	if err != nil || len(list) != 2 || list[0].Sequence != 8 || list[1].Sequence != 5 {
		t.Fatalf("trades %+v, %v", list, err)
	}
	if _, err := s.Ticker(ctx, "DOGE-USDT"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("an unknown pair: %v", err)
	}
	if tickers, err := s.Tickers(ctx); err != nil || len(tickers) != 2 || !tickers[1].Last.IsZero() {
		t.Fatalf("tickers %+v, %v", tickers, err)
	}
	if _, err := s.Candles(ctx, "BTC-USDT", "2d", time.Time{}, time.Time{}, 5); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a bad interval: %v", err)
	}
	// Paging: to at the oldest candle loaded gives exactly limit older ones.
	page, err := s.Candles(ctx, "BTC-USDT", "1m", time.Time{}, at("2026-09-30T10:03:00Z"), 2)
	if err != nil || len(page) != 2 || !page[0].OpenTime.Equal(at("2026-09-30T10:01:00Z")) || !page[1].OpenTime.Equal(at("2026-09-30T10:02:00Z")) {
		t.Fatalf("page before 10:03: %+v %v", page, err)
	}

	// After a restart the state comes back from the store.
	again := newService(t, store, &now)
	if tk, _ := again.Ticker(ctx, "BTC-USDT"); !tk.Last.Equal(d("69800")) || tk.Trades != 3 {
		t.Fatalf("reloaded ticker %+v", tk)
	}
	if _, err := again.OnTrades(ctx, batch[1:]); err != nil || len(store.trades) != 3 {
		t.Fatalf("a reloaded service applied old trades again: %v, %d stored", err, len(store.trades))
	}
}

func TestAFailedWriteReloadsBeforeTheRetry(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	now := at("2026-09-30T10:03:30Z")
	s := newService(t, store, &now)
	store.down = true
	batch := []domain.Trade{trade(1, "70000", "0.1", "2026-09-30T10:01:10Z")}
	if _, err := s.OnTrades(ctx, batch); err == nil {
		t.Fatal("the write failed")
	}
	store.down = false
	if _, err := s.OnTrades(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if len(store.trades) != 1 || store.symbols["BTC-USDT"].Sequence != 1 {
		t.Fatalf("the redelivered trade was not stored: %d trades", len(store.trades))
	}
}

func kinds(updates []Update) map[string]int {
	out := map[string]int{}
	for _, u := range updates {
		switch m := u.Message.(type) {
		case *marketv1.CandleUpdated:
			out["updated:"+m.GetCandle().GetInterval()]++
		case *marketv1.CandleClosed:
			out["closed:"+m.GetCandle().GetInterval()]++
		case *marketv1.TickerUpdated:
			out["ticker"]++
		}
	}
	return out
}

func TestUpdatesPushWhatChanged(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	now := at("2026-09-30T10:01:30Z")
	s := newService(t, store, &now)
	if got := s.Updates(now); len(got) != 0 {
		t.Fatalf("nothing traded yet: %+v", kinds(got))
	}
	if _, err := s.OnTrades(ctx, []domain.Trade{trade(1, "70000", "0.1", "2026-09-30T10:01:10Z")}); err != nil {
		t.Fatal(err)
	}
	got := kinds(s.Updates(now))
	if len(got) != len(domain.Intervals)+1 || got["updated:1m"] != 1 || got["updated:1M"] != 1 || got["ticker"] != 1 {
		t.Fatalf("after a trade: %v", got)
	}
	if got := s.Updates(now); len(got) != 0 {
		t.Fatalf("nothing changed: %+v", kinds(got))
	}
	// The minute ends: its candle closes, the next opens flat, once; the
	// unchanged ticker comes again, 30 seconds on.
	now = at("2026-09-30T10:02:00Z")
	got = kinds(s.Updates(now))
	if got["closed:1m"] != 1 || got["updated:1m"] != 1 || got["closed:3m"] != 0 || got["ticker"] != 1 || len(got) != 3 {
		t.Fatalf("at the minute: %v", got)
	}
	now = at("2026-09-30T10:02:10Z")
	if got := s.Updates(now); len(got) != 0 {
		t.Fatalf("the flat candle is pushed once: %+v", kinds(got))
	}
	// A quiet ticker is pushed again every TickerHeartbeat, alone.
	now = now.Add(TickerHeartbeat)
	if got := kinds(s.Updates(now)); got["ticker"] != 1 || len(got) != 1 {
		t.Fatalf("the heartbeat: %v", got)
	}
}

// Of a batch redelivered with a trade more, only that one is new: the
// public feed relays it alone.
func TestOnTradesReturnsWhatWasNew(t *testing.T) {
	ctx := context.Background()
	s := newService(t, newMemStore(), new(at("2026-09-30T10:03:30Z")))
	batch := []domain.Trade{trade(3, "70000", "0.1", "2026-09-30T10:01:10Z"), trade(5, "70500", "0.2", "2026-09-30T10:01:50Z")}
	if _, err := s.OnTrades(ctx, batch); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.OnTrades(ctx, append(batch, trade(8, "69800", "0.1", "2026-09-30T10:03:05Z")))
	if err != nil || len(fresh) != 1 || fresh[0].Sequence != 8 {
		t.Fatalf("new: %+v %v", fresh, err)
	}
}
