package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

type futuresFlag struct{ on atomic.Bool }

func (f *futuresFlag) Enabled(key string, _ flags.Subject) bool {
	return key == KeyFuturesData && f.on.Load()
}

// statsCall is one Stats request the fake source answered.
type statsCall struct {
	symbol, metric, period string
	after                  time.Time
	limit                  int
}

type fakeFutures struct {
	mu    sync.Mutex
	perps map[bool][]ports.Perpetual
	// answer gives a request's points.
	answer    func(c statsCall) ([]ports.FuturesStat, error)
	calls     []statsCall
	perpReads int
}

func (f *fakeFutures) Perpetuals(_ context.Context, cm bool) ([]ports.Perpetual, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perpReads++
	return f.perps[cm], nil
}

func (f *fakeFutures) Stats(_ context.Context, m ports.FuturesMarket, metric, period string, after time.Time, limit int) ([]ports.FuturesStat, error) {
	c := statsCall{m.Symbol, metric, period, after, limit}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.answer == nil {
		return nil, nil
	}
	return f.answer(c)
}

func (f *fakeFutures) OpenInterest(context.Context, ports.FuturesMarket) (decimal.Decimal, time.Time, error) {
	return decimal.RequireFromString("94791.893"), time.UnixMilli(1791284697161).UTC(), nil
}

func (f *fakeFutures) ForcedOrders(ctx context.Context, _ bool, _ func(ports.ForcedOrder)) error {
	<-ctx.Done()
	return ctx.Err()
}

type memStats struct {
	mu     sync.Mutex
	points map[string][]ports.FuturesStat // by symbol, metric and period
	purged map[string]time.Time
}

func newMemStats() *memStats {
	return &memStats{points: map[string][]ports.FuturesStat{}, purged: map[string]time.Time{}}
}

func statKey(symbol, metric, period string) string { return symbol + " " + metric + " " + period }

func (m *memStats) Upsert(_ context.Context, stats []ports.FuturesStat) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range stats {
		k := statKey(s.Symbol, s.Metric, s.Period)
		list := slices.DeleteFunc(m.points[k], func(x ports.FuturesStat) bool { return x.At.Equal(s.At) })
		list = append(list, s)
		slices.SortFunc(list, func(a, b ports.FuturesStat) int { return a.At.Compare(b.At) })
		m.points[k] = list
	}
	return nil
}

func (m *memStats) Last(_ context.Context, symbol, metric, period string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.points[statKey(symbol, metric, period)]
	if len(list) == 0 {
		return time.Time{}, nil
	}
	return list[len(list)-1].At, nil
}

func (m *memStats) Recent(_ context.Context, symbol, metric, period string, limit int) ([]ports.FuturesStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.points[statKey(symbol, metric, period)]
	return slices.Clone(list[max(0, len(list)-limit):]), nil
}

func (m *memStats) Purge(_ context.Context, period string, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purged[period] = before
	return 0, nil
}

var futuresT0 = time.Date(2026, 10, 6, 11, 10, 54, 0, time.UTC)

// counted is a counter's value.
func counted(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func newTestFutures(t *testing.T) (*FuturesStats, *fakeFutures, *memStats, *futuresFlag) {
	t.Helper()
	src := &fakeFutures{perps: map[bool][]ports.Perpetual{
		false: {{Remote: "BTCUSDT", Pair: "BTCUSDT"}, {Remote: "1000PEPEUSDT", Pair: "1000PEPEUSDT"}},
		true:  {{Remote: "BTCUSD_PERP", Pair: "BTCUSD", ContractSize: decimal.NewFromInt(100)}, {Remote: "ETHUSD_PERP", Pair: "ETHUSD", ContractSize: decimal.NewFromInt(10)}},
	}}
	repo := newMemStats()
	fl := &futuresFlag{}
	fl.on.Store(true)
	contracts := contractList{{Symbol: "BTC-USDT-PERP"}, {Symbol: "1000PEPE-USDT-PERP"}, {Symbol: "ASTRA-USDT-PERP"}, {Symbol: "BTC-USD-PERP"}}
	s := NewFuturesStats(src, repo, contracts, fl, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	s.now = func() time.Time { return futuresT0 }
	return s, src, repo, fl
}

func TestFuturesStatsFollowsTheSourcesPerpetuals(t *testing.T) {
	s, _, _, _ := newTestFutures(t)
	if _, known, _ := s.Market("ASTRA-USDT-PERP"); known {
		t.Fatal("known before the listing was read")
	}
	// Until then, what is stored is served for any contract.
	if _, err := s.Series(context.Background(), "ASTRA-USDT-PERP", ports.MetricOpenInterest, "5m", 30); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	btc, known, followed := s.Market("BTC-USDT-PERP")
	if !known || !followed || btc.CoinMargined || btc.Remote != "BTCUSDT" || btc.Pair != "BTCUSDT" {
		t.Fatalf("BTC-USDT-PERP: %+v %v %v", btc, known, followed)
	}
	if pepe, _, ok := s.Market("1000PEPE-USDT-PERP"); !ok || pepe.Remote != "1000PEPEUSDT" {
		t.Fatalf("the platform's thousand-coin code is the source's: %+v", pepe)
	}
	coin, _, ok := s.Market("BTC-USD-PERP")
	if !ok || !coin.CoinMargined || coin.Remote != "BTCUSD_PERP" || coin.Pair != "BTCUSD" || coin.ContractSize.String() != "100" {
		t.Fatalf("BTC-USD-PERP: %+v", coin)
	}
	if _, known, followed := s.Market("ASTRA-USDT-PERP"); !known || followed {
		t.Fatal("the platform coin's perpetual is not the source's")
	}
	if _, err := s.Series(context.Background(), "ASTRA-USDT-PERP", ports.MetricOpenInterest, "5m", 30); !apperr.Is(err, "MARKET_NO_FUTURES_DATA") {
		t.Fatalf("the platform coin's perpetual: %v", err)
	}
	if got := s.marketsOf(true); len(got) != 1 || got[0].Symbol != "BTC-USD-PERP" {
		t.Fatalf("COIN-M markets: %+v (ETH's has no platform contract)", got)
	}

	// The contracts are read again after 10 minutes, the source's
	// perpetuals (a megabyte) after an hour.
	src := s.src.(*fakeFutures)
	s.instruments = contractList{{Symbol: "BTC-USDT-PERP"}}
	for _, tc := range []struct {
		after time.Duration
		reads int
		pepe  bool
	}{{5 * time.Minute, 2, true}, {11 * time.Minute, 2, false}, {61 * time.Minute, 4, false}} {
		s.now = func() time.Time { return futuresT0.Add(tc.after) }
		if err := s.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		_, _, pepe := s.Market("1000PEPE-USDT-PERP")
		if src.perpReads != tc.reads || pepe != tc.pepe {
			t.Fatalf("%s on: %d reads of the perpetuals, PEPE followed %v", tc.after, src.perpReads, pepe)
		}
	}
}

func TestFuturesStatsSeriesArguments(t *testing.T) {
	s, _, repo, _ := newTestFutures(t)
	ctx := context.Background()
	for _, tc := range []struct{ metric, period string }{{"volume", "5m"}, {ports.MetricBasis, "30m"}, {ports.MetricBasis, ""}} {
		if _, err := s.Series(ctx, "BTC-USDT-PERP", tc.metric, tc.period, 30); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("%s %s: %v", tc.metric, tc.period, err)
		}
	}
	var points []ports.FuturesStat
	for i := range 600 {
		points = append(points, ports.FuturesStat{
			Symbol: "BTC-USDT-PERP", Metric: ports.MetricFunding,
			At: futuresT0.Add(time.Duration(i-600) * 8 * time.Hour), Values: map[string]decimal.Decimal{"funding_rate": decimal.Zero},
		})
	}
	if err := repo.Upsert(ctx, points); err != nil {
		t.Fatal(err)
	}
	// Funding has no period, whatever is asked; at most 500 points.
	got, err := s.Series(ctx, "BTC-USDT-PERP", ports.MetricFunding, "1h", 1000)
	if err != nil || len(got) != FuturesPoints || !got[len(got)-1].At.Equal(points[599].At) {
		t.Fatalf("%d funding points, %v", len(got), err)
	}
}

func TestFuturesStatsReadsEachSeriesAsItsPeriodsEnd(t *testing.T) {
	s, src, repo, _ := newTestFutures(t)
	ctx := context.Background()
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	btc, _, _ := s.Market("BTC-USDT-PERP")
	latest := futuresT0.Truncate(5 * time.Minute) // 11:10
	src.answer = func(c statsCall) ([]ports.FuturesStat, error) {
		var out []ports.FuturesStat
		for at := latest.Add(-4 * time.Duration(5) * time.Minute); !at.After(latest); at = at.Add(5 * time.Minute) {
			if at.After(c.after) {
				out = append(out, ports.FuturesStat{
					Symbol: c.symbol, Metric: c.metric, Period: c.period, At: at,
					Values: map[string]decimal.Decimal{"open_interest": decimal.NewFromInt(1)},
				})
			}
		}
		return out, nil
	}

	// The first read asks for the latest points and stores them; the next
	// is due when the 11:15 snapshot is out.
	se := &series{market: btc, period: "5m", due: futuresT0}
	if err := s.read(ctx, ports.MetricOpenInterest, se); err != nil {
		t.Fatal(err)
	}
	if c := src.calls[0]; !c.after.IsZero() || c.limit != FuturesPoints || c.period != "5m" {
		t.Fatalf("first read: %+v", c)
	}
	if !se.cursor.Equal(latest) || !se.due.Equal(latest.Add(5*time.Minute+futuresPublished)) {
		t.Fatalf("cursor %v, due %v", se.cursor, se.due)
	}
	if got, _ := repo.Recent(ctx, "BTC-USDT-PERP", ports.MetricOpenInterest, "5m", 10); len(got) != 5 {
		t.Fatalf("stored %d points", len(got))
	}
	if v := counted(t, s.points.WithLabelValues(ports.MetricOpenInterest)); v != 5 {
		t.Fatalf("points counted: %v", v)
	}

	// A volume is the period's: the bucket starting at 11:10 is out after
	// 11:15.
	taker := &series{market: btc, period: "5m", due: futuresT0}
	if err := s.read(ctx, ports.MetricTakerRatio, taker); err != nil {
		t.Fatal(err)
	}
	if !taker.due.Equal(latest.Add(10*time.Minute + futuresPublished)) {
		t.Fatalf("taker due %v", taker.due)
	}
	// The basis is a snapshot like the open interest.
	basis := &series{market: btc, period: "5m", due: futuresT0}
	if err := s.read(ctx, ports.MetricBasis, basis); err != nil {
		t.Fatal(err)
	}
	if !basis.due.Equal(latest.Add(5*time.Minute + futuresPublished)) {
		t.Fatalf("basis due %v", basis.due)
	}

	// Read again before the point is out (a restart): from the stored
	// cursor, nothing new, the point not late yet.
	again := &series{market: btc, period: "5m", due: futuresT0}
	if err := s.read(ctx, ports.MetricOpenInterest, again); err != nil {
		t.Fatal(err)
	}
	if last := src.calls[len(src.calls)-1]; !last.after.Equal(latest) {
		t.Fatalf("a series read again starts after the stored point: %+v", last)
	}
	if !again.due.Equal(latest.Add(5*time.Minute+futuresPublished)) || again.retry != 0 {
		t.Fatalf("not late yet: due %v, retry %v", again.due, again.retry)
	}

	// Late: the 11:15 point is not out at 11:17. Wait longer each time,
	// up to a quarter of the period (a minute at least).
	s.now = func() time.Time { return latest.Add(7 * time.Minute) }
	for i, want := range []time.Duration{time.Minute, 75 * time.Second, 75 * time.Second} {
		if err := s.read(ctx, ports.MetricOpenInterest, again); err != nil {
			t.Fatal(err)
		}
		if again.retry != want || !again.due.Equal(s.now().Add(want)) {
			t.Fatalf("late read %d: retry %v, due %v", i, again.retry, again.due)
		}
	}
	hourly := &series{market: btc, period: "1h", loaded: true, cursor: latest.Truncate(time.Hour)}
	src.answer = func(statsCall) ([]ports.FuturesStat, error) { return nil, nil }
	s.now = func() time.Time { return latest.Truncate(time.Hour).Add(3 * time.Hour) }
	var waits []time.Duration
	for range 6 {
		if err := s.read(ctx, ports.MetricOpenInterest, hourly); err != nil {
			t.Fatal(err)
		}
		waits = append(waits, hourly.retry)
	}
	if !slices.Equal(waits, []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}) {
		t.Fatalf("an hourly series' waits: %v", waits)
	}

	// A failure waits too, and is counted.
	src.answer = func(statsCall) ([]ports.FuturesStat, error) { return nil, errors.New("HTTP 503") }
	failed := &series{market: btc, period: "5m", loaded: true, cursor: latest}
	if err := s.read(ctx, ports.MetricOpenInterest, failed); err == nil || failed.retry != time.Minute {
		t.Fatalf("a failed read: %v, retry %v", err, failed.retry)
	}
	if v := counted(t, s.requests.WithLabelValues("usdm", ports.MetricOpenInterest, "error")); v != 1 {
		t.Fatalf("failures counted: %v", v)
	}
}

func TestFuturesStatsPagesThroughAGap(t *testing.T) {
	s, src, _, _ := newTestFutures(t)
	ctx := context.Background()
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	btc, _, _ := s.Market("BTC-USDT-PERP")
	start := futuresT0.Add(-3 * 24 * time.Hour).Truncate(5 * time.Minute)
	// A full page: the next follows at once.
	src.answer = func(c statsCall) ([]ports.FuturesStat, error) {
		var out []ports.FuturesStat
		for i := 1; i <= c.limit; i++ {
			out = append(out, ports.FuturesStat{
				Symbol: c.symbol, Metric: c.metric, Period: c.period, At: c.after.Add(time.Duration(i) * 5 * time.Minute),
				Values: map[string]decimal.Decimal{"basis": decimal.NewFromInt(1)},
			})
		}
		return out, nil
	}
	se := &series{market: btc, period: "5m", loaded: true, cursor: start}
	if err := s.read(ctx, ports.MetricBasis, se); err != nil {
		t.Fatal(err)
	}
	if !se.due.Equal(futuresT0) || !se.cursor.Equal(start.Add(500*5*time.Minute)) {
		t.Fatalf("after a full page: due %v, cursor %v", se.due, se.cursor)
	}
	// An empty page wholly in the past: the source's gap is passed over.
	src.answer = func(statsCall) ([]ports.FuturesStat, error) { return nil, nil }
	se.cursor = start
	if err := s.read(ctx, ports.MetricBasis, se); err != nil {
		t.Fatal(err)
	}
	if !se.due.Equal(futuresT0) || !se.cursor.Equal(start.Add(500*5*time.Minute)) {
		t.Fatalf("after an empty page in the past: due %v, cursor %v", se.due, se.cursor)
	}

	// Funding rates are read after each hour.
	funding := &series{market: btc, loaded: true}
	if err := s.read(ctx, ports.MetricFunding, funding); err != nil {
		t.Fatal(err)
	}
	if c := src.calls[len(src.calls)-1]; c.limit != 1000 || c.period != "" {
		t.Fatalf("funding read: %+v", c)
	}
	if want := futuresT0.Truncate(time.Hour).Add(time.Hour + futuresFundingAt); !funding.due.Equal(want) {
		t.Fatalf("funding due %v, want %v", funding.due, want)
	}
}

func TestFuturesStatsRelaysLiquidations(t *testing.T) {
	s, _, _, fl := newTestFutures(t)
	ctx := context.Background()
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	var got []Liquidation
	s.OnLiquidation(func(_ context.Context, l Liquidation) { got = append(got, l) })
	d := decimal.RequireFromString
	at := time.UnixMilli(1568014460893).UTC()
	s.relay(ctx, false, ports.ForcedOrder{Remote: "BTCUSDT", Side: "SELL", Price: d("9910"), AvgPrice: d("9912"), Filled: d("0.014"), At: at})
	s.relay(ctx, true, ports.ForcedOrder{Remote: "BTCUSD_PERP", Side: "BUY", Price: d("9425.5"), AvgPrice: d("9496.5"), Filled: d("3"), At: at})
	// Not ours: ETH's COIN-M contract, a quarterly one, a USDⓈ-M symbol on
	// the COIN-M stream.
	s.relay(ctx, true, ports.ForcedOrder{Remote: "ETHUSD_PERP", Side: "SELL", Filled: d("1")})
	s.relay(ctx, true, ports.ForcedOrder{Remote: "BTCUSD_261225", Side: "SELL", Filled: d("1")})
	s.relay(ctx, true, ports.ForcedOrder{Remote: "BTCUSDT", Side: "SELL", Filled: d("1")})
	if len(got) != 2 {
		t.Fatalf("relayed %+v", got)
	}
	if l := got[0]; l.Symbol != "BTC-USDT-PERP" || l.PositionSide != "LONG" || l.Quantity.String() != "0.014" || l.ValueUSD.String() != "138.768" || !l.At.Equal(at) {
		t.Fatalf("a USDⓈ-M long liquidated: %+v", l)
	}
	if l := got[1]; l.Symbol != "BTC-USD-PERP" || l.PositionSide != "SHORT" || l.Quantity.String() != "3" || l.ValueUSD.String() != "300" {
		t.Fatalf("a COIN-M short liquidated, 3 contracts of 100 USD: %+v", l)
	}
	fl.on.Store(false)
	s.relay(ctx, false, ports.ForcedOrder{Remote: "BTCUSDT", Side: "SELL", Filled: d("1")})
	if len(got) != 2 {
		t.Fatal("relayed while off")
	}
	if v := counted(t, s.liquidations.WithLabelValues("coinm")); v != 1 {
		t.Fatalf("COIN-M liquidations counted: %v", v)
	}
}

func TestFuturesStatsPurgesByPeriod(t *testing.T) {
	s, _, repo, _ := newTestFutures(t)
	s.Purge(context.Background())
	want := map[string]time.Duration{
		"5m": 501 * 5 * time.Minute, "15m": 501 * 15 * time.Minute, "1h": 501 * time.Hour, "4h": 30 * 24 * time.Hour,
		"1d": 30 * 24 * time.Hour, "": 30 * 24 * time.Hour,
	}
	for period, kept := range want {
		if got := repo.purged[period]; !got.Equal(futuresT0.Add(-kept)) {
			t.Errorf("period %q purged before %v, want %v", period, got, futuresT0.Add(-kept))
		}
	}
}

func TestFuturesStatsRunsWhileOn(t *testing.T) {
	s, src, repo, fl := newTestFutures(t)
	fl.on.Store(false)
	clock := futuresT0
	var mu sync.Mutex
	s.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	s.sleep = func(ctx context.Context, _ time.Duration) {
		mu.Lock()
		clock = clock.Add(time.Second)
		mu.Unlock()
		select {
		case <-ctx.Done():
		case <-time.After(time.Millisecond):
		}
	}
	src.answer = func(c statsCall) ([]ports.FuturesStat, error) {
		at := futuresT0.Add(-time.Hour)
		if !at.After(c.after) {
			return nil, nil
		}
		return []ports.FuturesStat{{
			Symbol: c.symbol, Metric: c.metric, Period: c.period, At: at,
			Values: map[string]decimal.Decimal{"x": decimal.NewFromInt(1)},
		}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	src.mu.Lock()
	n := len(src.calls)
	src.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d reads while off", n)
	}
	fl.on.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for {
		// Every series of the followed contracts: 2 USDⓈ-M and 1 COIN-M
		// contract, 6 metrics at 5 periods and the funding rates.
		repo.mu.Lock()
		series := len(repo.points)
		repo.mu.Unlock()
		if series == 3*(6*5+1) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d series stored", series)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if oi, ok := s.OpenInterestNow("BTC-USD-PERP"); !ok || oi.Quantity.String() != "94791.893" || !oi.Market.CoinMargined {
		t.Fatalf("open interest now: %+v %v", oi, ok)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run ended with %v", err)
	}
}
