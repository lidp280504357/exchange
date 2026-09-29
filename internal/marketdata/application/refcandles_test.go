package application

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// history is a reference source's klines by interval.
type history struct {
	mu     sync.Mutex
	klines map[domain.Interval][]domain.Candle
	calls  map[domain.Interval]int
}

func (h *history) Klines(_ context.Context, symbol string, i domain.Interval, _ time.Time, limit int) ([]domain.Candle, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.calls == nil {
		h.calls = map[domain.Interval]int{}
	}
	h.calls[i]++
	list := h.klines[i]
	if len(list) > limit {
		list = list[len(list)-limit:]
	}
	out := slices.Clone(list)
	for j := range out {
		out[j].Symbol, out[j].Interval = symbol, i
	}
	return out, nil
}

func (h *history) count(i domain.Interval) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[i]
}

type listing struct {
	pairs     []string
	contracts []ports.Contract
}

func (l listing) Listed(context.Context, string) (bool, error)        { return true, nil }
func (l listing) Symbols(context.Context) ([]string, error)           { return l.pairs, nil }
func (l listing) Contracts(context.Context) ([]ports.Contract, error) { return l.contracts, nil }

// klineFlags has the reference feed on and reference K-lines on except for
// the denied symbols.
type klineFlags struct{ deny []string }

func (f klineFlags) Enabled(key string, s flags.Subject) bool {
	switch key {
	case flags.KeyReferenceFeed:
		return true
	case flags.KeyReferenceKline:
		return !slices.Contains(f.deny, s.Symbol)
	}
	return false
}

func minute(t, o, h, l, c, v string) domain.Candle {
	return domain.Candle{
		Symbol: "BTC-USDT", Interval: domain.Minute1, OpenTime: at(t), Open: d(o), High: d(h), Low: d(l), Close: d(c), Volume: d(v),
		QuoteVolume: d(v).Mul(d(c)), Trades: 1,
	}
}

func newReferenceRig(h *history) *ReferenceCandles {
	return NewReferenceCandles(h, klineFlags{deny: []string{"ETH-BTC"}}, listing{
		pairs:     []string{"BTC-USDT", "ETH-BTC"},
		contracts: []ports.Contract{{Symbol: "BTC-USDT-PERP", IndexSymbol: "BTC-USDT"}},
	}, []string{"BTC-USDT"}, slog.New(slog.DiscardHandler))
}

func TestReferenceCandlesAggregateTheLiveMinutes(t *testing.T) {
	rc := newReferenceRig(&history{})
	defer rc.wg.Wait()
	rc.Observe(minute("2026-09-30T10:00:00Z", "100", "101", "99", "100.5", "1"))
	rc.Observe(minute("2026-09-30T10:00:00Z", "100", "103", "99", "102", "2")) // the same minute, later
	rc.Observe(minute("2026-09-30T10:01:00Z", "102", "104", "101", "101", "3"))
	five := rc.open["BTC-USDT"][domain.Minute5].c
	if !five.OpenTime.Equal(at("2026-09-30T10:00:00Z")) || !five.Open.Equal(d("100")) || !five.High.Equal(d("104")) ||
		!five.Low.Equal(d("99")) || !five.Close.Equal(d("101")) || !five.Volume.Equal(d("5")) || five.Trades != 2 {
		t.Fatalf("5m candle %+v", five)
	}
	if hour := rc.open["BTC-USDT"][domain.Hour1].c; !hour.Volume.Equal(d("5")) || !hour.High.Equal(d("104")) {
		t.Fatalf("1h candle %+v", hour)
	}
	// 10:05 opens the next 5m candle and closes the one before.
	rc.Observe(minute("2026-09-30T10:05:00Z", "101", "101", "100", "100", "4"))
	var ended []string
	for _, c := range rc.closed["BTC-USDT"] {
		ended = append(ended, string(c.Interval)+"@"+c.OpenTime.Format("15:04"))
	}
	if !slices.Contains(ended, "5m@10:00") || !slices.Contains(ended, "1m@10:01") || slices.Contains(ended, "1h@10:00") {
		t.Fatalf("closed %v", ended)
	}
	if next := rc.open["BTC-USDT"][domain.Minute5].c; !next.OpenTime.Equal(at("2026-09-30T10:05:00Z")) || !next.Volume.Equal(d("4")) {
		t.Fatalf("next 5m candle %+v", next)
	}
	if hour := rc.open["BTC-USDT"][domain.Hour1].c; !hour.Volume.Equal(d("9")) || !hour.Low.Equal(d("99")) {
		t.Fatalf("1h candle across 5m candles %+v", hour)
	}
}

func TestAnIntervalFirstSeenMidwayStartsFromTheSource(t *testing.T) {
	h := &history{klines: map[domain.Interval][]domain.Candle{
		domain.Minute5: {{
			OpenTime: at("2026-09-30T10:05:00Z"), Open: d("99"), High: d("105"), Low: d("98"), Close: d("104"), Volume: d("10"),
			QuoteVolume: d("1000"), Trades: 7,
		}},
	}}
	rc := newReferenceRig(h)
	rc.Observe(minute("2026-09-30T10:07:00Z", "104", "104", "103", "103", "1"))
	rc.wg.Wait()
	o := rc.open["BTC-USDT"][domain.Minute5]
	if o == nil || !o.c.Open.Equal(d("99")) || !o.c.Volume.Equal(d("10")) || !o.base.Volume.Equal(d("9")) || !o.c.Close.Equal(d("103")) {
		t.Fatalf("initialized %+v", o)
	}
	rc.Observe(minute("2026-09-30T10:07:00Z", "104", "106", "103", "106", "2"))
	if c := rc.open["BTC-USDT"][domain.Minute5].c; !c.Volume.Equal(d("11")) || !c.High.Equal(d("106")) {
		t.Fatalf("after the next update %+v", c)
	}
}

func TestReferenceCandlesReplaceThePlatformsInThePush(t *testing.T) {
	rc := newReferenceRig(&history{})
	rc.Observe(minute("2026-09-30T10:00:00Z", "100", "101", "99", "100.5", "1"))
	platform := []Update{
		{"BTC-USDT", &marketv1.CandleUpdated{Candle: &marketv1.Candle{Symbol: "BTC-USDT", Interval: "1m"}}},
		{"BTC-USDT", &marketv1.TickerUpdated{Ticker: &marketv1.Ticker{Symbol: "BTC-USDT"}}},
		{"ETH-BTC", &marketv1.CandleUpdated{Candle: &marketv1.Candle{Symbol: "ETH-BTC", Interval: "1m"}}},
	}
	rc.wg.Wait()
	out := rc.Push(context.Background(), platform)
	counts := map[string]int{}
	for _, u := range out {
		switch m := u.Message.(type) {
		case *marketv1.CandleUpdated:
			if m.GetCandle().GetSymbol() != u.Symbol {
				t.Fatalf("a candle of %s pushed as %s", m.GetCandle().GetSymbol(), u.Symbol)
			}
			if u.Symbol != "ETH-BTC" && m.GetCandle().GetClose() != "100.5" {
				t.Fatalf("a platform candle of %s kept: %v", u.Symbol, m)
			}
			counts[u.Symbol+" candle"]++
		case *marketv1.TickerUpdated:
			counts[u.Symbol+" ticker"]++
		}
	}
	// The intervals that open at 10:00 (the others would start from the
	// source, which has nothing here).
	n := 0
	for _, i := range domain.Intervals {
		if i.Start(at("2026-09-30T10:00:00Z")).Equal(at("2026-09-30T10:00:00Z")) {
			n++
		}
	}
	if counts["BTC-USDT candle"] != n || counts["BTC-USDT-PERP candle"] != n || counts["BTC-USDT ticker"] != 1 || counts["ETH-BTC candle"] != 1 {
		t.Fatalf("pushed %v", counts)
	}
	// Nothing new: no reference candles.
	if out := rc.Push(context.Background(), nil); len(out) != 0 {
		t.Fatalf("pushed again %d updates", len(out))
	}
	if ref, ok := rc.Serves(context.Background(), "BTC-USDT-PERP"); !ok || ref != "BTC-USDT" {
		t.Fatalf("serves %q %v", ref, ok)
	}
	if _, ok := rc.Serves(context.Background(), "ETH-BTC"); ok {
		t.Fatal("ETH-BTC has no followed reference")
	}
}

func TestReferenceCandlesServeTheSourcesHistory(t *testing.T) {
	h := &history{klines: map[domain.Interval][]domain.Candle{
		domain.Minute1: {
			{OpenTime: at("2026-09-30T09:58:00Z"), Close: d("98")},
			{OpenTime: at("2026-09-30T09:59:00Z"), Close: d("99")},
			{OpenTime: at("2026-09-30T10:00:00Z"), Close: d("100")},
		},
	}}
	rc := newReferenceRig(h)
	rc.now = func() time.Time { return at("2026-09-30T10:00:30Z") }
	rc.Observe(minute("2026-09-30T10:00:00Z", "100", "101", "99", "100.7", "1"))
	list, err := rc.Candles(context.Background(), "BTC-USDT-PERP", "BTC-USDT", "1m", time.Time{}, time.Time{}, 3)
	if err != nil || len(list) != 3 || list[0].Symbol != "BTC-USDT-PERP" || !list[2].Close.Equal(d("100.7")) || !list[1].Close.Equal(d("99")) {
		t.Fatalf("candles %+v %v", list, err)
	}
	if _, err := rc.Candles(context.Background(), "BTC-USDT", "BTC-USDT", "1m", time.Time{}, time.Time{}, 3); err != nil {
		t.Fatal(err)
	}
	rc.wg.Wait()
	if n := h.count(domain.Minute1); n != 1 {
		t.Fatalf("a second request within the cache time: %d calls", n)
	}
	if list, _ := rc.Candles(context.Background(), "BTC-USDT", "BTC-USDT", "1m", at("2026-09-30T09:59:10Z"), time.Time{}, 3); len(list) != 2 {
		t.Fatalf("from 09:59: %d candles", len(list))
	}
	if _, err := rc.Candles(context.Background(), "BTC-USDT", "BTC-USDT", "2d", time.Time{}, time.Time{}, 3); err == nil {
		t.Fatal("an unknown interval")
	}
}
