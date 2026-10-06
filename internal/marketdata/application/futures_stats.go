package application

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/kafka"
)

// The contracts' data panel (design 2026-10-06 §3.3): the reference
// market's statistics of the contracts it trades, read while
// market.futures_data is on, each series as its periods end; the open
// interest now, every minute; and its liquidation orders as they come,
// stored for a day and published on market.liquidations. A series keeps
// the 500 points a chart may ask for, none older than 30 days.

// FuturesMetrics are the statistics with periods, FuturesPeriods their
// periods.
var (
	FuturesMetrics = []string{
		ports.MetricOpenInterest, ports.MetricLongShortAccount, ports.MetricTopLongShortAccount,
		ports.MetricTopLongShortPosition, ports.MetricTakerRatio, ports.MetricBasis,
	}
	FuturesPeriods = []string{"5m", "15m", "1h", "4h", "1d"}
)

const (
	// FuturesPoints is the most points a series keeps and serves.
	FuturesPoints = 500
	// LiquidationsShown is the most recent liquidations served.
	LiquidationsShown = 100
	// futuresKept is the oldest a point may be (the source keeps 30
	// days), liquidationsKept the oldest liquidation.
	futuresKept      = 30 * 24 * time.Hour
	liquidationsKept = 24 * time.Hour
	// futuresListing is how often the contracts are read, and
	// futuresPerpetuals the source's perpetuals (USDⓈ-M's exchangeInfo
	// is a megabyte).
	futuresListing    = 10 * time.Minute
	futuresPerpetuals = time.Hour
	// futuresPublished is how long after its time a point is asked for:
	// the source publishes within a minute.
	futuresPublished = 70 * time.Second
	// futuresRetry is the first wait after a failed read or one that
	// brought nothing new, doubled up to futuresRetryMax (a quarter of
	// the period at most).
	futuresRetry    = time.Minute
	futuresRetryMax = 15 * time.Minute
	// futuresLook is the longest a lane sleeps before it looks at the
	// flag and the listing again.
	futuresLook = 15 * time.Second
	// futuresFundingAt is when, after each hour, the funding rates are
	// read (settlements are on the hour).
	futuresFundingAt = 90 * time.Second
	// futuresWarnEvery spaces a lane's warnings about failed reads.
	futuresWarnEvery = 10 * time.Minute
	// liquidationsQueue is how many liquidations wait to be stored and
	// published (more are dropped and counted), liquidationsFlush how
	// often they go.
	liquidationsQueue = 1000
	liquidationsFlush = time.Second
)

// ErrNoFuturesData answers for a contract the reference market does not
// trade (the platform coin's).
var ErrNoFuturesData = apperr.New(apperr.KindNotFound, "MARKET_NO_FUTURES_DATA",
	"the contract has no futures data: the reference market does not trade it")

// PeriodLength is how long a period of the statistics is.
func PeriodLength(period string) (time.Duration, bool) {
	d, ok := map[string]time.Duration{
		"5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour, "1d": 24 * time.Hour,
	}[period]
	return d, ok
}

// futuresRetention is how long a period's points are kept: the 500 a
// chart may ask for, at most 30 days.
func futuresRetention(period string) time.Duration {
	d, ok := PeriodLength(period)
	if !ok {
		return futuresKept
	}
	return min(futuresKept, (FuturesPoints+1)*d)
}

// bucketed: a point of the taker volumes is the volume of the period
// that starts at its time, published once the period ends; the others
// are snapshots at their time (the basis too: its 1d point of a day is
// out that morning).
func bucketed(metric string) bool { return metric == ports.MetricTakerRatio }

// OpenInterest is a contract's open interest now.
type OpenInterest struct {
	Market ports.FuturesMarket
	// Quantity is in the base asset (USDⓈ-M) or in contracts (COIN-M).
	Quantity decimal.Decimal
	At       time.Time
}

// FuturesStats reads and keeps the reference market's statistics of the
// contracts.
type FuturesStats struct {
	src       ports.FuturesSource
	repo      ports.FuturesStatsRepo
	contracts ports.FuturesContracts
	flags     Flags
	pub       kafka.Publisher
	events    *event.Factory
	log       *slog.Logger
	now       func() time.Time
	// sleep waits for d or until ctx ends (tests replace it).
	sleep func(ctx context.Context, d time.Duration)

	mu sync.Mutex
	// markets are the followed contracts by symbol, remotes by margin
	// and the source's symbol; listed is whether the listing was ever
	// read (until then no contract is known not to be followed).
	markets  map[string]ports.FuturesMarket
	remotes  map[string]ports.FuturesMarket
	listed   bool
	listedAt time.Time
	// perps are the source's perpetuals by margin and symbol, as read at
	// perpsAt.
	perps    map[bool]map[string]ports.Perpetual
	perpsAt  time.Time
	interest map[string]OpenInterest
	// liqs are the liquidations waiting to be stored and published.
	liqs chan queuedLiquidation

	requests     *prometheus.CounterVec
	points       *prometheus.CounterVec
	liquidations *prometheus.CounterVec
}

// NewFuturesStats returns the statistics of the contracts the listing
// has, read from src into repo while fl turns them on, the liquidations
// published with pub; register its metrics with reg.
func NewFuturesStats(src ports.FuturesSource, repo ports.FuturesStatsRepo, contracts ports.FuturesContracts, fl Flags,
	pub kafka.Publisher, events *event.Factory, log *slog.Logger, reg prometheus.Registerer,
) *FuturesStats {
	s := &FuturesStats{
		src: src, repo: repo, contracts: contracts, flags: fl, pub: pub, events: events, log: log, now: time.Now, sleep: sleepCtx,
		markets: map[string]ports.FuturesMarket{}, remotes: map[string]ports.FuturesMarket{}, interest: map[string]OpenInterest{},
		liqs: make(chan queuedLiquidation, liquidationsQueue),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_futures_stats_requests_total", Help: "Reads of the reference market's futures statistics, by margin, metric and result.",
		}, []string{"margin", "metric", "result"}),
		points: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_futures_stats_points_total", Help: "Points of the reference market's futures statistics stored, by metric.",
		}, []string{"metric"}),
		liquidations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_futures_liquidations_total",
			Help: "Liquidation orders of the reference market on the platform's contracts, by margin and result (relayed, dropped, failed).",
		}, []string{"margin", "result"}),
	}
	reg.MustRegister(s.requests, s.points, s.liquidations)
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func marginName(coinMargined bool) string {
	if coinMargined {
		return "coinm"
	}
	return "usdm"
}

func (s *FuturesStats) on() bool { return s.flags.Enabled(flags.KeyFuturesData, flags.Subject{}) }

// Run reads the statistics, the open interest and the liquidations of
// both margins, stores and publishes the liquidations, and purges old
// rows, until ctx ends.
func (s *FuturesStats) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	start := func(fn func(context.Context)) {
		wg.Go(func() { fn(ctx) })
	}
	start(s.listing)
	start(s.purging)
	start(s.flushing)
	for _, cm := range []bool{false, true} {
		for _, metric := range append(slices.Clone(FuturesMetrics), ports.MetricFunding) {
			start(func(ctx context.Context) { s.lane(ctx, cm, metric) })
		}
		start(func(ctx context.Context) { s.openInterest(ctx, cm) })
		start(func(ctx context.Context) { s.forcedOrders(ctx, cm) })
	}
	wg.Wait()
	return ctx.Err()
}

// listing reads the followed contracts while reading is on.
func (s *FuturesStats) listing(ctx context.Context) {
	var warned time.Time
	for ctx.Err() == nil {
		if s.on() {
			if err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
				if s.now().Sub(warned) >= futuresWarnEvery {
					s.log.WarnContext(ctx, "futures statistics: listing failed", "error", err)
					warned = s.now()
				}
				s.sleep(ctx, futuresRetry)
				continue
			}
		}
		s.sleep(ctx, futuresLook)
	}
}

// Refresh reads the contracts when the last listing is older than
// futuresListing, and the source's perpetuals when theirs is older than
// futuresPerpetuals: a contract is followed when the source trades the
// contract its reference_symbol names (BTCUSDT, BTCUSD_PERP; delisted
// contracts are not listed, closed ones are followed). A margin whose
// perpetuals cannot be read keeps the ones read before (review EK ④);
// the listing fails only while one was never read.
func (s *FuturesStats) Refresh(ctx context.Context) error {
	s.mu.Lock()
	now := s.now()
	fresh := s.listed && now.Sub(s.listedAt) < futuresListing
	old, stale := s.perps, now.Sub(s.perpsAt) >= futuresPerpetuals
	s.mu.Unlock()
	if fresh {
		return nil
	}
	contracts, err := s.contracts.FuturesContracts(ctx)
	if err != nil {
		return err
	}
	perps := old
	var failed error
	if stale {
		perps = map[bool]map[string]ports.Perpetual{}
		for _, cm := range []bool{false, true} {
			list, err := s.src.Perpetuals(ctx, cm)
			if err != nil {
				if old[cm] == nil {
					return err
				}
				perps[cm], failed = old[cm], err
				continue
			}
			perps[cm] = map[string]ports.Perpetual{}
			for _, p := range list {
				perps[cm][p.Remote] = p
			}
		}
		s.mu.Lock()
		s.perps = perps
		if failed == nil {
			s.perpsAt = now
		}
		s.mu.Unlock()
	}
	markets, remotes := map[string]ports.FuturesMarket{}, map[string]ports.FuturesMarket{}
	for _, c := range contracts {
		p, traded := perps[c.CoinMargined][c.ReferenceSymbol]
		if c.ReferenceSymbol == "" || !traded {
			continue
		}
		m := ports.FuturesMarket{Symbol: c.Symbol, CoinMargined: c.CoinMargined, Remote: c.ReferenceSymbol, Pair: p.Pair, ContractSize: c.ContractSize}
		if c.CoinMargined && !m.ContractSize.IsPositive() {
			m.ContractSize = p.ContractSize
		}
		markets[c.Symbol], remotes[remoteKey(c.CoinMargined, c.ReferenceSymbol)] = m, m
	}
	s.mu.Lock()
	s.markets, s.remotes, s.listed, s.listedAt = markets, remotes, true, now
	s.mu.Unlock()
	if failed != nil {
		return fmt.Errorf("perpetuals kept from before: %w", failed)
	}
	return nil
}

func remoteKey(coinMargined bool, remote string) string {
	return marginName(coinMargined) + " " + remote
}

// Market returns a contract's market at the source: known is false while
// the listing was never read, followed whether the source trades it.
func (s *FuturesStats) Market(symbol string) (m ports.FuturesMarket, known, followed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, followed = s.markets[symbol]
	return m, s.listed, followed
}

// marketsOf lists the followed contracts of a margin.
func (s *FuturesStats) marketsOf(coinMargined bool) []ports.FuturesMarket {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ports.FuturesMarket{}
	for _, m := range s.markets {
		if m.CoinMargined == coinMargined {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b ports.FuturesMarket) int { return strings.Compare(a.Symbol, b.Symbol) })
	return out
}

// series is one contract's statistic at one period, as a lane reads it.
type series struct {
	market ports.FuturesMarket
	period string
	// cursor is the time of the latest point read (zero: none yet);
	// loaded tells it was read from the store.
	cursor time.Time
	loaded bool
	due    time.Time
	retry  time.Duration
}

// lane reads one metric of one margin's contracts: the series due first,
// one request at a time (the source paces each endpoint).
func (s *FuturesStats) lane(ctx context.Context, coinMargined bool, metric string) {
	periods := FuturesPeriods
	if metric == ports.MetricFunding {
		periods = []string{""}
	}
	all := map[string]*series{}
	var warned time.Time
	failures := 0
	for ctx.Err() == nil {
		if !s.on() {
			s.sleep(ctx, futuresLook)
			continue
		}
		now := s.now()
		followed := map[string]bool{}
		for _, m := range s.marketsOf(coinMargined) {
			for _, p := range periods {
				key := m.Symbol + " " + p
				followed[key] = true
				if all[key] == nil {
					all[key] = &series{market: m, period: p, due: now}
				}
			}
		}
		var next *series
		for key, se := range all {
			switch {
			case !followed[key]:
				delete(all, key)
			case next == nil || se.due.Before(next.due):
				next = se
			}
		}
		if next == nil || next.due.After(now) {
			wait := futuresLook
			if next != nil {
				wait = min(wait, next.due.Sub(now))
			}
			s.sleep(ctx, wait)
			continue
		}
		if err := s.read(ctx, metric, next); err != nil && ctx.Err() == nil {
			failures++
			if s.now().Sub(warned) >= futuresWarnEvery {
				s.log.WarnContext(ctx, "futures statistics: read failed", "margin", marginName(coinMargined), "metric", metric,
					"symbol", next.market.Symbol, "period", next.period, "failures", failures, "error", err)
				warned, failures = s.now(), 0
			}
		}
	}
}

// read fetches a series' points after its cursor, stores them and sets
// when it is due next.
func (s *FuturesStats) read(ctx context.Context, metric string, se *series) error {
	now := s.now()
	if !se.loaded {
		last, err := s.repo.Last(ctx, se.market.Symbol, metric, se.period)
		if err != nil {
			se.due = now.Add(futuresRetry)
			return err
		}
		se.cursor, se.loaded = last, true
	}
	limit := FuturesPoints
	if metric == ports.MetricFunding {
		limit = 1000
	}
	points, err := s.src.Stats(ctx, se.market, metric, se.period, se.cursor, limit)
	if err == nil && len(points) > 0 {
		err = s.repo.Upsert(ctx, points)
	}
	margin := marginName(se.market.CoinMargined)
	if err != nil {
		s.requests.WithLabelValues(margin, metric, "error").Inc()
		se.retry = backoff(se)
		se.due = now.Add(se.retry)
		return err
	}
	s.requests.WithLabelValues(margin, metric, "ok").Inc()
	s.points.WithLabelValues(metric).Add(float64(len(points)))
	before := se.cursor
	if len(points) > 0 {
		se.cursor = points[len(points)-1].At
	}
	se.due, se.retry = s.nextDue(metric, se, before, len(points), limit, now)
	return nil
}

// nextDue is when a series is read next after a read at now that brought
// n points (limit asked) after before.
func (s *FuturesStats) nextDue(metric string, se *series, before time.Time, n, limit int, now time.Time) (time.Time, time.Duration) {
	if n >= limit {
		return now, 0 // more to page through
	}
	if metric == ports.MetricFunding {
		// Settlements are on the hour: read after each one.
		return now.Truncate(time.Hour).Add(time.Hour + futuresFundingAt), 0
	}
	d, _ := PeriodLength(se.period)
	if n == 0 && !before.IsZero() && before.Add(time.Duration(limit)*d).Before(now.Add(-d)) {
		// A page with nothing in it, wholly in the past: the source has a
		// gap there; read the next page.
		se.cursor = before.Add(time.Duration(limit) * d)
		return now, 0
	}
	// The next point is the one a period after the cursor (or the latest
	// period's): published once its time, or its period, has passed.
	nextAt := now.Truncate(d)
	if !se.cursor.IsZero() {
		nextAt = se.cursor.Add(d)
	}
	if bucketed(metric) {
		nextAt = nextAt.Add(d)
	}
	if due := nextAt.Add(futuresPublished + spread(metric, se.market.Symbol, se.period)); n > 0 || due.After(now) {
		return due, 0
	}
	// Nothing new, though the next point is late: wait longer each time.
	retry := backoff(se)
	return now.Add(retry), retry
}

// spread staggers the series of an hour or longer over the first quarter
// of their period (half an hour at most), each by its own offset, so that
// the hours, and above all UTC midnight when every period ends, do not
// ask for all of them at once (review EK ③).
func spread(metric, symbol, period string) time.Duration {
	d, _ := PeriodLength(period)
	window := min(d/4, 30*time.Minute)
	if d < time.Hour || window <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(metric + " " + symbol + " " + period))
	return time.Duration(h.Sum32()%uint32(window/time.Second)) * time.Second //nolint:gosec // window is at most half an hour
}

// backoff doubles a series' wait from futuresRetry, up to futuresRetryMax
// and a quarter of its period.
func backoff(se *series) time.Duration {
	ceiling := futuresRetryMax
	if d, ok := PeriodLength(se.period); ok {
		ceiling = min(ceiling, max(futuresRetry, d/4))
	}
	return min(ceiling, max(futuresRetry, 2*se.retry))
}

// openInterest reads each followed contract's open interest every minute.
func (s *FuturesStats) openInterest(ctx context.Context, coinMargined bool) {
	var warned time.Time
	for ctx.Err() == nil {
		if !s.on() {
			s.sleep(ctx, futuresLook)
			continue
		}
		round := s.now()
		for _, m := range s.marketsOf(coinMargined) {
			q, at, err := s.src.OpenInterest(ctx, m)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.requests.WithLabelValues(marginName(coinMargined), "open_interest_now", "error").Inc()
				if s.now().Sub(warned) >= futuresWarnEvery {
					s.log.WarnContext(ctx, "futures statistics: open interest failed", "symbol", m.Symbol, "error", err)
					warned = s.now()
				}
				continue
			}
			s.requests.WithLabelValues(marginName(coinMargined), "open_interest_now", "ok").Inc()
			s.mu.Lock()
			s.interest[m.Symbol] = OpenInterest{Market: m, Quantity: q, At: at}
			s.mu.Unlock()
		}
		s.sleep(ctx, time.Until(round.Add(time.Minute)))
	}
}

// OpenInterestNow returns a contract's open interest as last read.
func (s *FuturesStats) OpenInterestNow(symbol string) (OpenInterest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oi, ok := s.interest[symbol]
	return oi, ok
}

// forcedOrders follows the liquidation orders of a margin's contracts.
func (s *FuturesStats) forcedOrders(ctx context.Context, coinMargined bool) {
	wait := time.Second
	for ctx.Err() == nil {
		if !s.on() {
			s.sleep(ctx, futuresLook)
			continue
		}
		opened := s.now()
		err := s.src.ForcedOrders(ctx, coinMargined, func(o ports.ForcedOrder) { s.relay(coinMargined, o) })
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, ports.ErrQuiet) || s.now().Sub(opened) > time.Minute:
			// Quiet, or ended after a while (Binance closes connections
			// after a day): connect again soon.
			wait = time.Second
		default:
			wait = min(time.Minute, 2*wait)
		}
		if err != nil && !errors.Is(err, ports.ErrQuiet) {
			s.log.WarnContext(ctx, "futures statistics: liquidation stream failed", "margin", marginName(coinMargined), "error", err)
		}
		s.sleep(ctx, wait)
	}
}

// relay queues a liquidation order of a followed contract, in the
// platform's contract, to be stored and published.
func (s *FuturesStats) relay(coinMargined bool, o ports.ForcedOrder) {
	if !s.on() {
		return
	}
	s.mu.Lock()
	m, found := s.remotes[remoteKey(coinMargined, o.Remote)]
	s.mu.Unlock()
	if !found {
		return
	}
	l := ports.Liquidation{Symbol: m.Symbol, PositionSide: "SHORT", Price: o.Price, AvgPrice: o.AvgPrice, Quantity: o.Filled, At: o.At}
	if o.Side == "SELL" {
		l.PositionSide = "LONG"
	}
	price := o.AvgPrice
	if !price.IsPositive() {
		price = o.Price
	}
	l.ValueUSD = price.Mul(o.Filled)
	if coinMargined {
		l.ValueUSD = m.ContractSize.Mul(o.Filled)
	}
	select {
	case s.liqs <- queuedLiquidation{l: l, coinMargined: coinMargined}:
	default:
		s.liquidations.WithLabelValues(marginName(coinMargined), "dropped").Inc()
	}
}

// queuedLiquidation is a liquidation waiting to be stored and published,
// with its contract's margin for the metrics.
type queuedLiquidation struct {
	l            ports.Liquidation
	coinMargined bool
}

// flushing stores and publishes the queued liquidations every second.
func (s *FuturesStats) flushing(ctx context.Context) {
	var warned time.Time
	for ctx.Err() == nil {
		s.sleep(ctx, liquidationsFlush)
		if err := s.flush(ctx); err != nil && ctx.Err() == nil && s.now().Sub(warned) >= futuresWarnEvery {
			s.log.WarnContext(ctx, "futures statistics: liquidations not stored or published", "error", err)
			warned = s.now()
		}
	}
}

// flush stores the queued liquidations and publishes them on
// market.liquidations, keyed by contract; ones that fail are counted and
// dropped (display data, the next ones follow).
func (s *FuturesStats) flush(ctx context.Context) error {
	var queued []queuedLiquidation
drain:
	for len(queued) < liquidationsQueue {
		select {
		case q := <-s.liqs:
			queued = append(queued, q)
		default:
			break drain
		}
	}
	if len(queued) == 0 {
		return nil
	}
	list := make([]ports.Liquidation, 0, len(queued))
	for _, q := range queued {
		list = append(list, q.l)
	}
	count := func(result string) {
		for _, q := range queued {
			s.liquidations.WithLabelValues(marginName(q.coinMargined), result).Inc()
		}
	}
	err := s.repo.AddLiquidations(ctx, list)
	if err == nil {
		err = s.publish(ctx, list)
	}
	if err != nil {
		count("failed")
		return err
	}
	count("relayed")
	return nil
}

func (s *FuturesStats) publish(ctx context.Context, list []ports.Liquidation) error {
	recs := make([]kafka.Record, 0, len(list))
	for _, l := range list {
		env, err := s.events.New(ctx, &marketv1.LiquidationOccurred{
			Symbol: l.Symbol, PositionSide: l.PositionSide, Price: l.Price.String(), AveragePrice: l.AvgPrice.String(),
			Quantity: l.Quantity.String(), ValueUsd: l.ValueUSD.String(), TradedAt: timestamppb.New(l.At),
		}, "symbol", l.Symbol)
		if err != nil {
			return err
		}
		raw, err := proto.Marshal(env)
		if err != nil {
			return fmt.Errorf("liquidation %s: %w", l.Symbol, err)
		}
		recs = append(recs, kafka.Record{Topic: event.TopicMarketLiquidations, Key: l.Symbol, EventType: env.GetEventType(), Envelope: raw})
	}
	return s.pub.Publish(ctx, recs...)
}

// purging deletes old points and liquidations every hour.
func (s *FuturesStats) purging(ctx context.Context) {
	for ctx.Err() == nil {
		s.Purge(ctx)
		s.sleep(ctx, time.Hour)
	}
}

// Purge deletes the points older than their period keeps, and the
// liquidations older than a day.
func (s *FuturesStats) Purge(ctx context.Context) {
	now := s.now()
	for _, p := range append(slices.Clone(FuturesPeriods), "") {
		n, err := s.repo.Purge(ctx, p, now.Add(-futuresRetention(p)))
		if err != nil {
			if ctx.Err() == nil {
				s.log.WarnContext(ctx, "futures statistics: purge failed", "period", p, "error", err)
			}
			return
		}
		if n > 0 {
			s.log.InfoContext(ctx, "futures statistics purged", "period", p, "points", n)
		}
	}
	if n, err := s.repo.PurgeLiquidations(ctx, now.Add(-liquidationsKept)); err != nil {
		if ctx.Err() == nil {
			s.log.WarnContext(ctx, "futures statistics: liquidations purge failed", "error", err)
		}
	} else if n > 0 {
		s.log.InfoContext(ctx, "futures liquidations purged", "liquidations", n)
	}
}

// followed answers ErrNoFuturesData for a contract known not to be the
// reference market's; until the listing was read, every contract may be.
func (s *FuturesStats) followed(symbol string) error {
	if _, known, followed := s.Market(symbol); known && !followed {
		return ErrNoFuturesData
	}
	return nil
}

// Series returns up to limit latest points of a contract's statistic,
// oldest first: what is stored, also while reading is off.
func (s *FuturesStats) Series(ctx context.Context, symbol, metric, period string, limit int) ([]ports.FuturesStat, error) {
	if err := s.followed(symbol); err != nil {
		return nil, err
	}
	switch {
	case metric == ports.MetricFunding:
		period = ""
	case !slices.Contains(FuturesMetrics, metric):
		return nil, apperr.Invalid("metric must be one of open_interest, long_short_account, top_long_short_account, " +
			"top_long_short_position, taker_ratio, basis, funding")
	case !slices.Contains(FuturesPeriods, period):
		return nil, apperr.Invalid("period must be one of 5m, 15m, 1h, 4h, 1d")
	}
	return s.repo.Recent(ctx, symbol, metric, period, max(1, min(limit, FuturesPoints)))
}

// Liquidations returns up to limit (at most LiquidationsShown) of a
// contract's latest liquidations from the last day, newest first.
func (s *FuturesStats) Liquidations(ctx context.Context, symbol string, limit int) ([]ports.Liquidation, error) {
	if err := s.followed(symbol); err != nil {
		return nil, err
	}
	return s.repo.RecentLiquidations(ctx, symbol, max(1, min(limit, LiquidationsShown)))
}
