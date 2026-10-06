package application

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

// markCall is one MarkStream connection of fakeMarkSource: the test sends
// marks through on and ends it with end.
type markCall struct {
	refs []ports.Reference
	coin bool
	on   func(domain.ReferenceMark)
	end  chan error
}

type fundingAsk struct {
	symbols  []string
	coin     bool
	from, to time.Time
}

type fakeMarkSource struct {
	calls chan markCall

	mu      sync.Mutex
	settled []domain.SettledFunding
	asks    []fundingAsk
	fail    bool
}

func (f *fakeMarkSource) MarkStream(ctx context.Context, refs []ports.Reference, coin bool, on func(domain.ReferenceMark)) error {
	call := markCall{refs: refs, coin: coin, on: on, end: make(chan error, 1)}
	select {
	case f.calls <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-call.end:
		return err
	}
}

func (f *fakeMarkSource) SettledFunding(_ context.Context, refs []ports.Reference, coin bool, from, to time.Time) ([]domain.SettledFunding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ask := fundingAsk{coin: coin, from: from, to: to}
	for _, r := range refs {
		ask.symbols = append(ask.symbols, r.Symbol)
	}
	f.asks = append(f.asks, ask)
	if f.fail {
		return nil, errors.New("binance funding rates: HTTP 503")
	}
	return f.settled, nil
}

type feedOn struct{ off atomic.Bool }

func (f *feedOn) Enabled(key string, _ flags.Subject) bool {
	return key == flags.KeyReferenceFeed && !f.off.Load()
}

func nextCall(t *testing.T, src *fakeMarkSource) markCall {
	t.Helper()
	select {
	case c := <-src.calls:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no mark stream connection")
		return markCall{}
	}
}

// The feed follows the mark price of every contract whose index pair has
// a reference market, at the source's perpetual of the same code: USDⓈ-M
// and COIN-M on connections of their own. It reconnects after a failure
// and forgets the marks when the reference feed goes off.
func TestMarkFeedFollowsTheContracts(t *testing.T) {
	list := testListing()
	list.pairs = append(list.pairs, ports.Pair{Symbol: "ETH-USDT", Base: "ETH", Quote: "USDT", Status: "TRADING", Reference: ref("ETH-USDT", "ETHUSDT")})
	list.contracts = append(list.contracts,
		ports.Contract{Symbol: "ETH-USDT-PERP", IndexSymbol: "ETH-USDT"}, ports.Contract{Symbol: "ASTRA-USDT-PERP", IndexSymbol: "ASTRA-USDT"},
		ports.Contract{Symbol: "BTC-USD-PERP", IndexSymbol: "BTC-USDT"})
	refs := NewReferenceMap(list, slog.New(slog.DiscardHandler))
	src := &fakeMarkSource{calls: make(chan markCall)}
	fl := &feedOn{}
	feed := NewMarkFeed(src, refs, fl, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	feed.recheck = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = feed.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	c, coin := nextCall(t, src), nextCall(t, src)
	if c.coin {
		c, coin = coin, c
	}
	if c.coin || len(c.refs) != 2 || c.refs[0].Symbol != "BTC-USDT-PERP" || c.refs[0].Remote != "BTCUSDT" ||
		c.refs[1].Symbol != "ETH-USDT-PERP" || c.refs[1].Remote != "ETHUSDT" {
		t.Fatalf("USDⓈ-M followed %+v (coin %v)", c.refs, c.coin)
	}
	if !coin.coin || len(coin.refs) != 1 || coin.refs[0].Symbol != "BTC-USD-PERP" || coin.refs[0].Remote != "BTCUSD_PERP" {
		t.Fatalf("COIN-M followed %+v", coin.refs)
	}
	c.on(domain.ReferenceMark{Symbol: "BTC-USDT-PERP", Mark: d("60120"), Index: d("60060")})
	c.on(domain.ReferenceMark{Symbol: "SOL-USDT-PERP", Mark: d("1")}) // not followed
	m, received, ok := feed.Latest("BTC-USDT-PERP")
	if !ok || !m.Mark.Equal(d("60120")) || received.IsZero() {
		t.Fatalf("latest %+v %v", m, ok)
	}
	if _, _, ok := feed.Latest("SOL-USDT-PERP"); ok {
		t.Fatal("an unfollowed contract kept")
	}

	// The connection fails: it comes back after a second.
	c.end <- errors.New("binance mark stream: nothing received for 30s")
	c = nextCall(t, src)
	if len(c.refs) != 2 {
		t.Fatalf("reconnected with %+v", c.refs)
	}

	// The reference feed goes off: the marks are dropped.
	fl.off.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, _, ok := feed.Latest("BTC-USDT-PERP"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("marks kept with the feed off")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMarkGroupsSplitTheMarkets(t *testing.T) {
	m := map[string]ports.Reference{
		"BTC-USDT-PERP": {Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT"},
		"BTC-USD-PERP":  {Symbol: "BTC-USD-PERP", Remote: "BTCUSD_PERP"},
		"ETH-USDT-PERP": {Symbol: "ETH-USDT-PERP", Remote: "ETHUSDT"},
	}
	for i := range 450 {
		s := "X" + decimal.NewFromInt(int64(i)).String() + "-USDT-PERP"
		m[s] = ports.Reference{Symbol: s, Remote: "X" + decimal.NewFromInt(int64(i)).String() + "USDT"}
	}
	g := markGroups(m)
	if len(g) != 4 || g[0].coin || len(g[0].refs) != markStreamsPerConn || len(g[2].refs) != 452-2*markStreamsPerConn ||
		!g[3].coin || len(g[3].refs) != 1 || g[3].refs[0].Symbol != "BTC-USD-PERP" {
		t.Fatalf("groups %d", len(g))
	}
}

// The settled rates Marks asks for are fetched one request per market and
// funding time, kept once found, asked again while not.
func TestMarkFeedFetchesSettledRates(t *testing.T) {
	refs := NewReferenceMap(testListing(), slog.New(slog.DiscardHandler))
	src := &fakeMarkSource{calls: make(chan markCall)}
	feed := NewMarkFeed(src, refs, &feedOn{}, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	end := at("2026-10-06T08:00:00Z")
	now := end.Add(5 * time.Second)
	feed.now = func() time.Time { return now }
	ctx := context.Background()
	feed.follow(ctx)

	if _, ok := feed.Settled("BTC-USDT-PERP", end); ok {
		t.Fatal("known before any fetch")
	}
	feed.FetchFunding(ctx)
	if len(src.asks) != 1 || src.asks[0].coin || len(src.asks[0].symbols) != 1 || !src.asks[0].from.Equal(end) ||
		!src.asks[0].to.Equal(end.Add(fundingWindow)) {
		t.Fatalf("asked %+v", src.asks)
	}
	// Not settled yet on the market: asked again on the next fetch.
	src.settled = []domain.SettledFunding{
		{Symbol: "BTC-USDT-PERP", FundingTime: end.Add(-8 * time.Hour), Rate: d("0.0003")}, // an older period's
		{Symbol: "BTC-USDT-PERP", FundingTime: end.Add(time.Millisecond), Rate: d("0.00021"), Mark: d("60095.5")},
	}
	feed.FetchFunding(ctx)
	s, ok := feed.Settled("BTC-USDT-PERP", end)
	if !ok || !s.Rate.Equal(d("0.00021")) || !s.Mark.Equal(d("60095.5")) || len(src.asks) != 2 {
		t.Fatalf("settled %+v %v after %d asks", s, ok, len(src.asks))
	}
	feed.FetchFunding(ctx)
	if len(src.asks) != 2 {
		t.Fatal("asked again once found")
	}
	// A failed request is retried; a day later the wanted rate is dropped.
	src.fail = true
	feed.Settled("BTC-USDT-PERP", end.Add(8*time.Hour))
	now = end.Add(8*time.Hour + time.Second)
	feed.FetchFunding(ctx)
	feed.FetchFunding(ctx)
	if len(src.asks) != 4 {
		t.Fatalf("%d asks", len(src.asks))
	}
	now = now.Add(25 * time.Hour)
	feed.FetchFunding(ctx)
	if len(src.asks) != 4 || len(feed.wanted) != 0 || len(feed.settled) != 0 {
		t.Fatalf("kept a day on: %d asks, %d wanted, %d settled", len(src.asks), len(feed.wanted), len(feed.settled))
	}
}
