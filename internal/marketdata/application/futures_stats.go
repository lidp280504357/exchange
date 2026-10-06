package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// The contracts' data panel (design 2026-10-06 §3.3): the reference
// market's statistics of the contracts it trades, read while
// market.futures_data is on, each series as its periods end; the open
// interest now, every minute; and its liquidation orders as they come.
// A series keeps the 500 points a chart may ask for, none older than 30
// days.

// KeyFuturesData turns the reading on (global); off, the stored
// statistics are still served.
const KeyFuturesData = "market.futures_data"

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
	// futuresKept is the oldest a point may be (the source keeps 30
	// days).
	futuresKept = 30 * 24 * time.Hour
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

// Liquidation is a liquidation of the reference market, in the
// platform's contract.
type Liquidation struct {
	Symbol string
	// PositionSide is the side of the position closed: LONG when the
	// liquidation order sold.
	PositionSide string
	Price        decimal.Decimal
	AvgPrice     decimal.Decimal
	// Quantity is filled: in the base asset (USDⓈ-M) or in contracts
	// (COIN-M).
	Quantity decimal.Decimal
	// ValueUSD is the average price times the quantity, or the contracts
	// times their face value.
	ValueUSD decimal.Decimal
	At       time.Time
}

// FuturesStats reads and keeps the reference market's statistics of the
// contracts.
type FuturesStats struct {
	src         ports.FuturesSource
	repo        ports.FuturesStatsRepo
	instruments ports.Instruments
	flags       Flags
	log         *slog.Logger
	now         func() time.Time
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
	onLiq    func(context.Context, Liquidation)

	requests     *prometheus.CounterVec
	points       *prometheus.CounterVec
	liquidations *prometheus.CounterVec
}

// NewFuturesStats returns the statistics of the contracts instruments
// lists, read from src into repo while fl turns them on; register its
// metrics with reg.
func NewFuturesStats(src ports.FuturesSource, repo ports.FuturesStatsRepo, instruments ports.Instruments, fl Flags,
	log *slog.Logger, reg prometheus.Registerer,
) *FuturesStats {
	s := &FuturesStats{
		src: src, repo: repo, instruments: instruments, flags: fl, log: log, now: time.Now, sleep: sleepCtx,
		markets: map[string]ports.FuturesMarket{}, remotes: map[string]ports.FuturesMarket{}, interest: map[string]OpenInterest{},
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_futures_stats_requests_total", Help: "Reads of the reference market's futures statistics, by margin, metric and result.",
		}, []string{"margin", "metric", "result"}),
		points: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_futures_stats_points_total", Help: "Points of the reference market's futures statistics stored, by metric.",
		}, []string{"metric"}),
		liquidations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_futures_liquidations_total", Help: "Liquidation orders of the reference market relayed, by margin.",
		}, []string{"margin"}),
	}
	reg.MustRegister(s.requests, s.points, s.liquidations)
	return s
}

// OnLiquidation sets where the liquidations go; without it they are
// only counted.
func (s *FuturesStats) OnLiquidation(fn func(context.Context, Liquidation)) { s.onLiq = fn }

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

func (s *FuturesStats) on() bool { return s.flags.Enabled(KeyFuturesData, flags.Subject{}) }

// Run reads the statistics, the open interest and the liquidations of
// both margins, and purges old points, until ctx ends.
func (s *FuturesStats) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	start := func(fn func(context.Context)) {
		wg.Go(func() { fn(ctx) })
	}
	start(s.listing)
	start(s.purging)
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
	for ctx.Err() == nil {
		if s.on() {
			if err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
				s.log.WarnContext(ctx, "futures statistics: listing failed", "error", err)
				s.sleep(ctx, futuresRetry)
				continue
			}
		}
		s.sleep(ctx, futuresLook)
	}
}

// Refresh reads the contracts when the last listing is older than
// futuresListing, and the source's perpetuals when theirs is older than
// futuresPerpetuals: a contract is followed when the source trades it
// (BTC-USDT-PERP as BTCUSDT, BTC-USD-PERP as BTCUSD_PERP; delisted ones
// are not listed, closed ones are followed).
func (s *FuturesStats) Refresh(ctx context.Context) error {
	s.mu.Lock()
	now := s.now()
	fresh := s.listed && now.Sub(s.listedAt) < futuresListing
	perps := s.perps
	if now.Sub(s.perpsAt) >= futuresPerpetuals {
		perps = nil
	}
	s.mu.Unlock()
	if fresh {
		return nil
	}
	contracts, err := s.instruments.Contracts(ctx)
	if err != nil {
		return err
	}
	if perps == nil {
		perps = map[bool]map[string]ports.Perpetual{}
		for _, cm := range []bool{false, true} {
			list, err := s.src.Perpetuals(ctx, cm)
			if err != nil {
				return err
			}
			perps[cm] = map[string]ports.Perpetual{}
			for _, p := range list {
				perps[cm][p.Remote] = p
			}
		}
		s.mu.Lock()
		s.perps, s.perpsAt = perps, now
		s.mu.Unlock()
	}
	markets, remotes := map[string]ports.FuturesMarket{}, map[string]ports.FuturesMarket{}
	for _, c := range contracts {
		cm, remote, ok := remoteContract(c.Symbol)
		if !ok {
			continue
		}
		if p, traded := perps[cm][remote]; traded {
			m := ports.FuturesMarket{Symbol: c.Symbol, CoinMargined: cm, Remote: remote, Pair: p.Pair, ContractSize: p.ContractSize}
			markets[c.Symbol], remotes[remoteKey(cm, remote)] = m, m
		}
	}
	s.mu.Lock()
	s.markets, s.remotes, s.listed, s.listedAt = markets, remotes, true, now
	s.mu.Unlock()
	return nil
}

func remoteKey(coinMargined bool, remote string) string {
	return marginName(coinMargined) + " " + remote
}

// remoteContract is the source's perpetual of a platform contract:
// <BASE>-USDT-PERP is USDⓈ-M <BASE>USDT, <BASE>-USD-PERP COIN-M
// <BASE>USD_PERP (the platform's codes follow the source's, 1000PEPE
// included).
func remoteContract(symbol string) (coinMargined bool, remote string, ok bool) {
	if base, found := strings.CutSuffix(symbol, "-USDT-PERP"); found && base != "" {
		return false, base + "USDT", true
	}
	if base, found := strings.CutSuffix(symbol, "-USD-PERP"); found && base != "" {
		return true, base + "USD_PERP", true
	}
	return false, "", false
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
	if due := nextAt.Add(futuresPublished); n > 0 || due.After(now) {
		return due, 0
	}
	// Nothing new, though the next point is late: wait longer each time.
	retry := backoff(se)
	return now.Add(retry), retry
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

// forcedOrders relays the liquidation orders of the followed contracts.
func (s *FuturesStats) forcedOrders(ctx context.Context, coinMargined bool) {
	wait := time.Second
	for ctx.Err() == nil {
		if !s.on() {
			s.sleep(ctx, futuresLook)
			continue
		}
		opened := s.now()
		err := s.src.ForcedOrders(ctx, coinMargined, func(o ports.ForcedOrder) { s.relay(ctx, coinMargined, o) })
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

// relay hands on a liquidation order of a followed contract.
func (s *FuturesStats) relay(ctx context.Context, coinMargined bool, o ports.ForcedOrder) {
	if !s.on() {
		return
	}
	s.mu.Lock()
	m, found := s.remotes[remoteKey(coinMargined, o.Remote)]
	s.mu.Unlock()
	if !found {
		return
	}
	l := Liquidation{Symbol: m.Symbol, PositionSide: "SHORT", Price: o.Price, AvgPrice: o.AvgPrice, Quantity: o.Filled, At: o.At}
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
	s.liquidations.WithLabelValues(marginName(coinMargined)).Inc()
	if s.onLiq != nil {
		s.onLiq(ctx, l)
	}
}

// purging deletes old points every hour.
func (s *FuturesStats) purging(ctx context.Context) {
	for ctx.Err() == nil {
		s.Purge(ctx)
		s.sleep(ctx, time.Hour)
	}
}

// Purge deletes the points older than their period keeps.
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
}

// Series returns up to limit latest points of a contract's statistic,
// oldest first: what is stored, also while reading is off.
func (s *FuturesStats) Series(ctx context.Context, symbol, metric, period string, limit int) ([]ports.FuturesStat, error) {
	if _, known, followed := s.Market(symbol); known && !followed {
		return nil, ErrNoFuturesData
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
