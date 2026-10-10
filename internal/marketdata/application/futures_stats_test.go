package application

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/kafka"
)

type futuresFlag struct{ on atomic.Bool }

func (f *futuresFlag) Enabled(key string, _ flags.Subject) bool {
	return key == flags.KeyFuturesData && f.on.Load()
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
	// perpErr fails a margin's perpetuals.
	perpErr map[bool]error
}

func (f *fakeFutures) Perpetuals(_ context.Context, cm bool) ([]ports.Perpetual, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perpReads++
	if err := f.perpErr[cm]; err != nil {
		return nil, err
	}
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
	liqs   []ports.Liquidation
	// liqsPurged is the last cutoff of the liquidations.
	liqsPurged time.Time
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

func (m *memStats) AddLiquidations(_ context.Context, list []ports.Liquidation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range list {
		if !slices.ContainsFunc(m.liqs, func(x ports.Liquidation) bool {
			return x.Symbol == l.Symbol && x.At.Equal(l.At) && x.PositionSide == l.PositionSide
		}) {
			m.liqs = append(m.liqs, l)
		}
	}
	return nil
}

func (m *memStats) RecentLiquidations(_ context.Context, symbol string, limit int) ([]ports.Liquidation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ports.Liquidation
	for _, l := range m.liqs {
		if l.Symbol == symbol {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b ports.Liquidation) int { return b.At.Compare(a.At) })
	return out[:min(limit, len(out))], nil
}

func (m *memStats) PurgeLiquidations(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.liqsPurged = before
	return 0, nil
}

// futuresList is the listing of both margin types.
type futuresList []ports.FuturesContract

func (l futuresList) FuturesContracts(context.Context) ([]ports.FuturesContract, error) {
	return l, nil
}

// recordPub keeps what was published, and what a failed publish was
// given; err fails the next publish.
type recordPub struct {
	mu     sync.Mutex
	recs   []kafka.Record
	failed []kafka.Record
	err    error
	// fails is how many publishes fail before they go through.
	fails int
}

func (p *recordPub) Publish(_ context.Context, recs ...kafka.Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		p.failed = append(p.failed, recs...)
		return p.err
	}
	if p.fails > 0 {
		p.fails--
		p.failed = append(p.failed, recs...)
		return errors.New("broker unreachable")
	}
	p.recs = append(p.recs, recs...)
	return nil
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
	contracts := futuresList{
		{Symbol: "BTC-USDT-PERP", ReferenceSymbol: "BTCUSDT"},
		{Symbol: "1000PEPE-USDT-PERP", ReferenceSymbol: "1000PEPEUSDT"},
		{Symbol: "ASTRA-USDT-PERP"},
		{Symbol: "OLD-USDT-PERP", ReferenceSymbol: "OLDUSDT"},
		{Symbol: "BTC-USD-PERP", CoinMargined: true, ReferenceSymbol: "BTCUSD_PERP", ContractSize: decimal.NewFromInt(100)},
	}
	s := NewFuturesStats(src, repo, contracts, fl, &recordPub{}, event.NewFactory("market-data-service", "test"),
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
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
	if _, _, followed := s.Market("OLD-USDT-PERP"); followed {
		t.Fatal("a contract whose Binance contract is no longer traded is followed")
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
	s.contracts = futuresList{{Symbol: "BTC-USDT-PERP", ReferenceSymbol: "BTCUSDT"}}
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
	s, _, repo, fl := newTestFutures(t)
	ctx := context.Background()
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	pub := s.pub.(*recordPub)
	d := decimal.RequireFromString
	at := time.UnixMilli(1568014460893).UTC()
	s.relay(false, ports.ForcedOrder{Remote: "BTCUSDT", Side: "SELL", Price: d("9910"), AvgPrice: d("9912"), Filled: d("0.014"), At: at})
	s.relay(true, ports.ForcedOrder{Remote: "BTCUSD_PERP", Side: "BUY", Price: d("9425.5"), AvgPrice: d("9496.5"), Filled: d("3"), At: at})
	// Not ours: ETH's COIN-M contract, a quarterly one, a USDⓈ-M symbol on
	// the COIN-M stream.
	s.relay(true, ports.ForcedOrder{Remote: "ETHUSD_PERP", Side: "SELL", Filled: d("1")})
	s.relay(true, ports.ForcedOrder{Remote: "BTCUSD_261225", Side: "SELL", Filled: d("1")})
	s.relay(true, ports.ForcedOrder{Remote: "BTCUSDT", Side: "SELL", Filled: d("1")})
	fl.on.Store(false)
	s.relay(false, ports.ForcedOrder{Remote: "BTCUSDT", Side: "SELL", Filled: d("1"), At: at.Add(time.Second)})
	fl.on.Store(true)
	if err := s.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(repo.liqs) != 2 || len(pub.recs) != 2 {
		t.Fatalf("stored %+v, published %d", repo.liqs, len(pub.recs))
	}
	if l := repo.liqs[0]; l.Symbol != "BTC-USDT-PERP" || l.PositionSide != "LONG" || l.Quantity.String() != "0.014" ||
		l.ValueUSD.String() != "138.768" || !l.At.Equal(at) {
		t.Fatalf("a USDⓈ-M long liquidated: %+v", l)
	}
	if l := repo.liqs[1]; l.Symbol != "BTC-USD-PERP" || l.PositionSide != "SHORT" || l.Quantity.String() != "3" || l.ValueUSD.String() != "300" {
		t.Fatalf("a COIN-M short liquidated, 3 contracts of 100 USD: %+v", l)
	}
	rec := pub.recs[1]
	var env eventv1.Envelope
	if err := proto.Unmarshal(rec.Envelope, &env); err != nil {
		t.Fatal(err)
	}
	var occurred marketv1.LiquidationOccurred
	if err := env.GetPayload().UnmarshalTo(&occurred); err != nil {
		t.Fatal(err)
	}
	if rec.Topic != event.TopicMarketLiquidations || rec.Key != "BTC-USD-PERP" || occurred.GetPositionSide() != "SHORT" ||
		occurred.GetAveragePrice() != "9496.5" || occurred.GetQuantity() != "3" || occurred.GetValueUsd() != "300" ||
		!occurred.GetTradedAt().AsTime().Equal(at) {
		t.Fatalf("published %s %s %+v", rec.Topic, rec.Key, &occurred)
	}
	if v := counted(t, s.liquidations.WithLabelValues("coinm", "relayed")); v != 1 {
		t.Fatalf("COIN-M liquidations relayed: %v", v)
	}
	// Nothing queued: nothing to do.
	if err := s.flush(ctx); err != nil || len(pub.recs) != 2 {
		t.Fatalf("an empty flush: %v, %d published", err, len(pub.recs))
	}
	// A publish that fails once goes out on the second try (review EN),
	// the same envelope (its event ID; C40 ⑥), counted as retried.
	s.sleep = func(context.Context, time.Duration) {}
	pub.fails = 1
	s.relay(false, ports.ForcedOrder{Remote: "BTCUSDT", Side: "BUY", Price: d("1"), AvgPrice: d("1"), Filled: d("2"), At: at.Add(30 * time.Second)})
	if err := s.flush(ctx); err != nil || len(pub.recs) != 3 {
		t.Fatalf("published again: %v, %d published", err, len(pub.recs))
	}
	if len(pub.failed) != 1 || !bytes.Equal(pub.failed[0].Envelope, pub.recs[2].Envelope) {
		t.Fatal("the second try sent another envelope")
	}
	if v := counted(t, s.retried.WithLabelValues("usdm")); v != 1 {
		t.Fatalf("retried: %v", v)
	}
	// One that fails twice is counted; the liquidations are dropped.
	pub.err = errors.New("redpanda down")
	s.relay(false, ports.ForcedOrder{Remote: "BTCUSDT", Side: "BUY", Price: d("1"), AvgPrice: d("1"), Filled: d("1"), At: at.Add(time.Minute)})
	if err := s.flush(ctx); err == nil {
		t.Fatal("a failed publish was not reported")
	}
	if v := counted(t, s.liquidations.WithLabelValues("usdm", "failed")); v != 1 {
		t.Fatalf("failed: %v", v)
	}
	// A full queue drops the rest.
	pub.err = nil
	for i := range liquidationsQueue + 3 {
		s.relay(false, ports.ForcedOrder{Remote: "BTCUSDT", Side: "BUY", Price: d("1"), AvgPrice: d("1"), Filled: d("1"), At: at.Add(time.Duration(i) * time.Millisecond)})
	}
	if v := counted(t, s.liquidations.WithLabelValues("usdm", "dropped")); v != 3 {
		t.Fatalf("dropped: %v", v)
	}
	if err := s.flush(ctx); err != nil {
		t.Fatal(err)
	}

	// The recent ones of a contract, newest first, at most 100; the
	// platform coin's perpetual has none.
	list, err := s.Liquidations(ctx, "BTC-USDT-PERP", 1000)
	if err != nil || len(list) != LiquidationsShown || !list[0].At.After(list[1].At) {
		t.Fatalf("%d liquidations, %v", len(list), err)
	}
	if _, err := s.Liquidations(ctx, "ASTRA-USDT-PERP", 10); !apperr.Is(err, "MARKET_NO_FUTURES_DATA") {
		t.Fatalf("the platform coin's perpetual: %v", err)
	}
}

func TestFuturesStatsListingSurvivesOneMarginFailing(t *testing.T) {
	s, src, _, _ := newTestFutures(t)
	ctx := context.Background()
	// Never read: the listing fails as a whole.
	src.perpErr = map[bool]error{true: errors.New("dapi down")}
	if err := s.Refresh(ctx); err == nil {
		t.Fatal("a listing without COIN-M's perpetuals ever read passed")
	}
	if _, known, _ := s.Market("BTC-USDT-PERP"); known {
		t.Fatal("known after a failed first listing")
	}
	src.perpErr = nil
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	// An hour on COIN-M fails again: its perpetuals from before stand in,
	// USDⓈ-M's are read anew.
	src.perpErr = map[bool]error{true: errors.New("dapi down")}
	src.perps[false] = src.perps[false][:1] // 1000PEPEUSDT stopped trading
	s.now = func() time.Time { return futuresT0.Add(61 * time.Minute) }
	if err := s.Refresh(ctx); err == nil || !strings.Contains(err.Error(), "kept from before") {
		t.Fatalf("a partly failed listing: %v", err)
	}
	if _, _, ok := s.Market("BTC-USD-PERP"); !ok {
		t.Fatal("BTC-USD-PERP dropped while dapi was down")
	}
	if _, _, ok := s.Market("1000PEPE-USDT-PERP"); ok {
		t.Fatal("USDⓈ-M's perpetuals were not read anew")
	}
}

func TestFuturesStatsSpreadsTheLongPeriods(t *testing.T) {
	for _, tc := range []struct {
		period string
		window time.Duration
	}{{"5m", 0}, {"15m", 0}, {"1h", 15 * time.Minute}, {"4h", 30 * time.Minute}, {"1d", 30 * time.Minute}} {
		offsets := map[time.Duration]bool{}
		for _, symbol := range []string{"BTC-USDT-PERP", "ETH-USDT-PERP", "SOL-USDT-PERP", "BTC-USD-PERP", "XRP-USDT-PERP"} {
			o := spread(ports.MetricOpenInterest, symbol, tc.period)
			if o < 0 || (tc.window == 0 && o != 0) || (tc.window > 0 && o >= tc.window) {
				t.Fatalf("%s %s: offset %s", symbol, tc.period, o)
			}
			if o != spread(ports.MetricOpenInterest, symbol, tc.period) {
				t.Fatal("the offset changes")
			}
			offsets[o] = true
		}
		if tc.window > 0 && len(offsets) < 3 {
			t.Fatalf("%s: offsets %v", tc.period, offsets)
		}
	}
	// A read of an hourly series is due at its next point plus its offset.
	s, src, _, _ := newTestFutures(t)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	btc, _, _ := s.Market("BTC-USDT-PERP")
	hour := futuresT0.Truncate(time.Hour)
	src.answer = func(c statsCall) ([]ports.FuturesStat, error) {
		return []ports.FuturesStat{{Symbol: c.symbol, Metric: c.metric, Period: c.period, At: hour, Values: map[string]decimal.Decimal{"x": decimal.Zero}}}, nil
	}
	se := &series{market: btc, period: "1h", loaded: true}
	if err := s.read(context.Background(), ports.MetricLongShortAccount, se); err != nil {
		t.Fatal(err)
	}
	if want := hour.Add(time.Hour + futuresPublished + spread(ports.MetricLongShortAccount, "BTC-USDT-PERP", "1h")); !se.due.Equal(want) {
		t.Fatalf("due %v, want %v", se.due, want)
	}
}

func TestFuturesStatsPurgesByPeriod(t *testing.T) {
	s, _, repo, _ := newTestFutures(t)
	s.Purge(context.Background())
	// 501 periods, at most the 15 days Postgres keeps (M1).
	want := map[string]time.Duration{
		"5m": 501 * 5 * time.Minute, "15m": 501 * 15 * time.Minute, "1h": 15 * 24 * time.Hour, "4h": 15 * 24 * time.Hour,
		"1d": 15 * 24 * time.Hour, "": 15 * 24 * time.Hour,
	}
	for period, kept := range want {
		if got := repo.purged[period]; !got.Equal(futuresT0.Add(-kept)) {
			t.Errorf("period %q purged before %v, want %v", period, got, futuresT0.Add(-kept))
		}
	}
	if !repo.liqsPurged.Equal(futuresT0.Add(-24 * time.Hour)) {
		t.Errorf("liquidations purged before %v", repo.liqsPurged)
	}
}

// A COIN-M contract's open interest now is its latest 5-minute point
// stored, where the panel's curve ends; a USDⓈ-M one's is read from the
// source (review A71).
func TestFuturesStatsInterestNow(t *testing.T) {
	s, _, repo, _ := newTestFutures(t)
	ctx := context.Background()
	coinM := ports.FuturesMarket{Symbol: "BTC-USD-PERP", CoinMargined: true, Remote: "BTCUSD_PERP", Pair: "BTCUSD", ContractSize: decimal.NewFromInt(100)}
	if q, at, err := s.interestNow(ctx, coinM); err != nil || !at.IsZero() || !q.IsZero() {
		t.Fatalf("nothing stored: %v %v %v", q, at, err)
	}
	point := func(at time.Time, metric, period, oi string) ports.FuturesStat {
		return ports.FuturesStat{Symbol: "BTC-USD-PERP", Metric: metric, Period: period, At: at, Values: map[string]decimal.Decimal{
			"open_interest": decimal.RequireFromString(oi), "open_interest_value": decimal.RequireFromString(oi).Mul(coinM.ContractSize),
		}}
	}
	if err := repo.Upsert(ctx, []ports.FuturesStat{
		point(futuresT0.Add(-10*time.Minute), ports.MetricOpenInterest, "5m", "3336000"),
		point(futuresT0.Add(-5*time.Minute), ports.MetricOpenInterest, "5m", "3336618"),
		point(futuresT0, ports.MetricOpenInterest, "15m", "1"),
		point(futuresT0, ports.MetricLongShortAccount, "5m", "2"),
	}); err != nil {
		t.Fatal(err)
	}
	if q, at, err := s.interestNow(ctx, coinM); err != nil || q.String() != "3336618" || !at.Equal(futuresT0.Add(-5*time.Minute)) {
		t.Fatalf("COIN-M now: %v %v %v", q, at, err)
	}
	usdM := ports.FuturesMarket{Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT", Pair: "BTCUSDT"}
	if q, at, err := s.interestNow(ctx, usdM); err != nil || q.String() != "94791.893" || !at.Equal(time.UnixMilli(1791284697161).UTC()) {
		t.Fatalf("USDⓈ-M now: %v %v %v", q, at, err)
	}
	if v := counted(t, s.requests.WithLabelValues("usdm", "open_interest_now", "ok")); v != 1 {
		t.Fatalf("source reads counted %v", v)
	}
	if v := counted(t, s.requests.WithLabelValues("coinm", "open_interest_now", "ok")); v != 0 {
		t.Fatalf("stored points counted as source reads: %v", v)
	}

	// Given as now while counted within 30 minutes, whichever margin: a
	// stored point whose series stopped, a reading while the source fails,
	// are no longer now (A80 ②).
	s.interest["BTC-USD-PERP"] = OpenInterest{Market: coinM, Quantity: decimal.RequireFromString("3336618"), At: futuresT0.Add(-5 * time.Minute)}
	s.interest["BTC-USDT-PERP"] = OpenInterest{Market: usdM, Quantity: decimal.RequireFromString("94791.893"), At: futuresT0.Add(-time.Minute)}
	if _, ok := s.OpenInterestNow("BTC-USD-PERP"); !ok {
		t.Fatal("a point 5 minutes old is now")
	}
	s.now = func() time.Time { return futuresT0.Add(25*time.Minute + time.Second) }
	if _, ok := s.OpenInterestNow("BTC-USD-PERP"); ok {
		t.Fatal("a point 30 minutes and a second old is not now")
	}
	if oi, ok := s.OpenInterestNow("BTC-USDT-PERP"); !ok || oi.Quantity.String() != "94791.893" {
		t.Fatalf("a reading 26 minutes old is: %+v %v", oi, ok)
	}
	s.now = func() time.Time { return futuresT0.Add(29*time.Minute + time.Second) }
	if _, ok := s.OpenInterestNow("BTC-USDT-PERP"); ok {
		t.Fatal("a reading 30 minutes and a second old is not now")
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
		values := map[string]decimal.Decimal{"x": decimal.NewFromInt(1)}
		if c.metric == ports.MetricOpenInterest {
			// Recent enough to be now (A80 ②).
			at = futuresT0.Add(-5 * time.Minute)
			if !at.After(c.after) {
				return nil, nil
			}
			values = map[string]decimal.Decimal{"open_interest": decimal.RequireFromString("3336618")}
		}
		return []ports.FuturesStat{{Symbol: c.symbol, Metric: c.metric, Period: c.period, At: at, Values: values}}, nil
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
	// Open interest now: the source's for a USDⓈ-M contract, the latest
	// 5-minute point stored for a COIN-M one, once the minute's loop has
	// taken it; measured from the start (every sleep moved the clock on).
	mu.Lock()
	clock = futuresT0
	mu.Unlock()
	for {
		usdM, okU := s.OpenInterestNow("BTC-USDT-PERP")
		coinM, okC := s.OpenInterestNow("BTC-USD-PERP")
		if okU && okC {
			if usdM.Quantity.String() != "94791.893" || usdM.Market.CoinMargined {
				t.Fatalf("USDⓈ-M open interest now: %+v", usdM)
			}
			if coinM.Quantity.String() != "3336618" || !coinM.At.Equal(futuresT0.Add(-5*time.Minute)) || !coinM.Market.CoinMargined {
				t.Fatalf("COIN-M open interest now: %+v", coinM)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("open interest now: %v %v", okU, okC)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run ended with %v", err)
	}
}
