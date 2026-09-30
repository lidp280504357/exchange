package application

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// tickerFlags has the feed on and reference tickers on but for the
// denied symbols.
type tickerFlags struct{ deny []string }

func (f tickerFlags) Enabled(key string, s flags.Subject) bool {
	switch key {
	case flags.KeyReferenceFeed:
		return true
	case flags.KeyReferenceTicker:
		return !slices.Contains(f.deny, s.Symbol)
	}
	return false
}

type tickerRig struct {
	svc   *Service
	feed  *ReferenceFeed
	list  listing
	ticks *Tickers
}

func newTickerRig(t *testing.T, fl Flags) tickerRig {
	t.Helper()
	list := testListing()
	list.pairs = append(list.pairs,
		ports.Pair{Symbol: "ETH-USDT", Base: "ETH", Quote: "USDT", Status: "TRADING", Rank: 2, Reference: ref("ETH-USDT", "ETHUSDT")},
		ports.Pair{Symbol: "SOL-USDT", Base: "SOL", Quote: "USDT", Status: "PREPARE", Rank: 5, Reference: ref("SOL-USDT", "SOLUSDT")},
	)
	svc := New(newMemStore(), list, slog.New(slog.DiscardHandler))
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	feed := NewReferenceFeed(&fakeSource{}, newMemStore(), fl, list, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	refs := NewReferenceMap(list, slog.New(slog.DiscardHandler))
	return tickerRig{svc: svc, feed: feed, list: list, ticks: NewTickers(svc, feed, refs, fl, list)}
}

func tick(symbol, last, open, quoteVolume string, at time.Time) domain.Ticker {
	t := domain.Ticker{Symbol: symbol, Last: d(last), Open: d(open), High: d(last), Low: d(open), Volume: d("10"), QuoteVolume: d(quoteVolume), At: at}
	t.Change = t.Last.Sub(t.Open).DivRound(t.Open, 8)
	return t
}

func TestTickersShowTheReferenceMarket(t *testing.T) {
	rig := newTickerRig(t, tickerFlags{deny: []string{"ETH-USDT"}})
	ctx := context.Background()
	at := time.Now().Add(-time.Minute) // old, and still shown
	rig.feed.setTicker(tick("BTC-USDT", "84000", "80000", "1000000", at))
	rig.feed.setTicker(tick("ETH-USDT", "3000", "2900", "500000", at))

	got, err := rig.ticks.Ticker(ctx, "BTC-USDT")
	if err != nil || !got.Last.Equal(d("84000")) || !got.At.Equal(at) {
		t.Fatalf("BTC-USDT %+v %v", got, err)
	}
	// A contract shows its index pair's ticker under its own symbol.
	if got, _ := rig.ticks.Ticker(ctx, "BTC-USDT-PERP"); got.Symbol != "BTC-USDT-PERP" || !got.Last.Equal(d("84000")) {
		t.Fatalf("BTC-USDT-PERP %+v", got)
	}
	// Denied by the flag, or no reference market: the platform's.
	if got, _ := rig.ticks.Ticker(ctx, "ETH-USDT"); !got.Last.IsZero() || !got.At.IsZero() {
		t.Fatalf("ETH-USDT %+v", got)
	}
	if got, _ := rig.ticks.Ticker(ctx, "ETH-BTC"); !got.At.IsZero() {
		t.Fatalf("ETH-BTC %+v", got)
	}
	all, err := rig.ticks.All(ctx)
	if err != nil || len(all) != 5 {
		t.Fatalf("all %d %v", len(all), err)
	}
}

func TestSummaryRanksTheTradingUSDTPairs(t *testing.T) {
	rig := newTickerRig(t, tickerFlags{})
	now := time.Now()
	rig.feed.setTicker(tick("BTC-USDT", "84000", "80000", "1000000", now)) // +5%
	rig.feed.setTicker(tick("ETH-USDT", "2900", "3000", "2000000", now))   // -3.3%
	rig.feed.setTicker(tick("SOL-USDT", "200", "100", "9000000", now))     // PREPARE: left out
	s, err := rig.ticks.Summary(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	names := func(list []domain.Ticker) (out []string) {
		for _, tk := range list {
			out = append(out, tk.Symbol)
		}
		return out
	}
	if g := names(s.Gainers); !slices.Equal(g, []string{"BTC-USDT", "ETH-USDT"}) {
		t.Fatalf("gainers %v", g)
	}
	if l := names(s.Losers); !slices.Equal(l, []string{"ETH-USDT", "BTC-USDT"}) {
		t.Fatalf("losers %v", l)
	}
	if v := names(s.Turnover); !slices.Equal(v, []string{"ETH-USDT", "BTC-USDT"}) {
		t.Fatalf("turnover %v", v)
	}
	if s, _ := rig.ticks.Summary(context.Background(), 1); len(s.Gainers) != 1 {
		t.Fatalf("limit 1: %v", names(s.Gainers))
	}
}

func TestTickerPushSwapsThePlatformsForTheReferences(t *testing.T) {
	fl := &toggleFlags{on: map[string]bool{flags.KeyReferenceFeed: true, flags.KeyReferenceTicker: true}}
	rig := newTickerRig(t, fl)
	ctx := context.Background()
	at := time.Now()
	rig.feed.setTicker(tick("BTC-USDT", "84000", "80000", "1000000", at))
	platform := []Update{
		{"BTC-USDT", &marketv1.TickerUpdated{Ticker: &marketv1.Ticker{Symbol: "BTC-USDT", Last: "1"}}},
		{"BTC-USDT", &marketv1.CandleUpdated{Candle: &marketv1.Candle{Symbol: "BTC-USDT"}}},
		{"ETH-BTC", &marketv1.TickerUpdated{Ticker: &marketv1.Ticker{Symbol: "ETH-BTC", Last: "0.05"}}},
	}
	out := rig.ticks.Push(ctx, platform)
	tickers := map[string]string{}
	candles := 0
	for _, u := range out {
		switch m := u.Message.(type) {
		case *marketv1.TickerUpdated:
			tickers[u.Symbol] = m.GetTicker().GetLast()
			if m.GetTicker().GetSymbol() != u.Symbol {
				t.Fatalf("a ticker of %s pushed as %s", m.GetTicker().GetSymbol(), u.Symbol)
			}
		case *marketv1.CandleUpdated:
			candles++
		}
	}
	if tickers["BTC-USDT"] != "84000" || tickers["BTC-USDT-PERP"] != "84000" || tickers["ETH-BTC"] != "0.05" || candles != 1 {
		t.Fatalf("pushed tickers %v, %d candles", tickers, candles)
	}
	// Nothing newer: nothing again, and the platform's stays swapped out.
	if out := rig.ticks.Push(ctx, platform[:1]); len(out) != 0 {
		t.Fatalf("pushed again: %v", out)
	}
	// The flag goes off: the platform's ticker is pushed again at once.
	rig.svc.mu.Lock()
	rig.svc.state("BTC-USDT").pushedTicker = &domain.Ticker{Symbol: "BTC-USDT"}
	rig.svc.mu.Unlock()
	fl.set(flags.KeyReferenceTicker, false)
	_ = rig.ticks.Push(ctx, nil)
	var repushed bool
	for _, u := range rig.svc.Updates(time.Now()) {
		if _, ok := u.Message.(*marketv1.TickerUpdated); ok && u.Symbol == "BTC-USDT" {
			repushed = true
		}
	}
	if !repushed {
		t.Fatal("the platform's ticker was not pushed after the reference one")
	}
}

// toggleFlags are switched by the test.
type toggleFlags struct {
	mu sync.Mutex
	on map[string]bool
}

func (f *toggleFlags) Enabled(key string, _ flags.Subject) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on[key]
}

func (f *toggleFlags) set(key string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.on[key] = on
}
