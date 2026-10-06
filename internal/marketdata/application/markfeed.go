package application

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

// The reference market's mark prices (coin-M design §3.1): the contracts
// with a reference market follow its mark price, index price and funding
// rate while market.reference_mark is on for them, the self-computed ones
// standing in whenever the market's are stale.
const (
	// markStreamsPerConn caps the contracts of one mark stream connection
	// (Binance allows 1024 streams on one).
	markStreamsPerConn = 200
	// fundingPoll is how often the settled rates still wanted are asked
	// for; fundingWindow is how long after its funding time the market
	// may record a settlement (Binance's are a millisecond after it).
	fundingPoll   = 10 * time.Second
	fundingWindow = time.Minute
	// fundingForget drops a wanted rate never found, and a rate found,
	// this long after its funding time.
	fundingForget = 24 * time.Hour
)

// MarkFeed follows the reference market's mark prices of every contract
// that has a reference market while market.reference_feed is on (whether
// market.reference_mark is on for it or not: the gap to the self-computed
// price is watched before it is turned on), one stream connection per
// market and group of contracts, restarted when the followed contracts
// change. It also fetches the rates the market settled, as Marks asks for
// them.
type MarkFeed struct {
	src   ports.MarkSource
	refs  *ReferenceMap
	flags Flags
	log   *slog.Logger
	now   func() time.Time
	// recheck is how often the flag is looked at, remap how often the
	// followed contracts are.
	recheck time.Duration
	remap   time.Duration

	mu       sync.Mutex
	followed map[string]ports.Reference // by contract
	latest   map[string]receivedMark
	settled  map[settledKey]domain.SettledFunding
	wanted   map[settledKey]bool

	failures prometheus.Counter
	fetches  *prometheus.CounterVec
}

type receivedMark struct {
	mark domain.ReferenceMark
	at   time.Time // when it arrived
}

type settledKey struct {
	symbol string
	at     time.Time
}

// NewMarkFeed follows the contracts refs maps to a reference market on
// src and registers its metrics with reg: market_mark_reference_age_seconds
// per followed contract is -1 while it has no mark.
func NewMarkFeed(src ports.MarkSource, refs *ReferenceMap, fl Flags, log *slog.Logger, reg prometheus.Registerer) *MarkFeed {
	f := &MarkFeed{
		src: src, refs: refs, flags: fl, log: log, now: time.Now, recheck: 10 * time.Second, remap: time.Minute,
		followed: map[string]ports.Reference{}, latest: map[string]receivedMark{},
		settled: map[settledKey]domain.SettledFunding{}, wanted: map[settledKey]bool{},
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_mark_stream_failures_total", Help: "Reference mark price stream connections that failed.",
		}),
		fetches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_mark_funding_fetches_total", Help: "Requests for the reference market's settled funding rates, by result.",
		}, []string{"result"}),
	}
	reg.MustRegister(f.failures, f.fetches, markAgeCollector{f})
	return f
}

// markAgeCollector reports market_mark_reference_age_seconds of the
// followed contracts.
type markAgeCollector struct{ f *MarkFeed }

var markAgeDesc = prometheus.NewDesc("market_mark_reference_age_seconds",
	"Age of the contract's latest mark price from the reference market; -1 while there is none.", []string{"symbol"}, nil)

func (c markAgeCollector) Describe(ch chan<- *prometheus.Desc) { ch <- markAgeDesc }

func (c markAgeCollector) Collect(ch chan<- prometheus.Metric) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	now := c.f.now()
	for symbol := range c.f.followed {
		age := -1.0
		if r, ok := c.f.latest[symbol]; ok {
			age = now.Sub(r.at).Seconds()
		}
		ch <- prometheus.MustNewConstMetric(markAgeDesc, prometheus.GaugeValue, age, symbol)
	}
}

func (f *MarkFeed) enabled() bool {
	return f.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{})
}

// Latest returns the contract's latest reference mark and when it
// arrived; false while it has none.
func (f *MarkFeed) Latest(symbol string) (domain.ReferenceMark, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.latest[symbol]
	return r.mark, r.at, ok
}

// Follows reports whether the feed follows the contract: the reference
// feed is on and the contract has a reference market (the platform
// coin's perpetual has none).
func (f *MarkFeed) Follows(symbol string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.followed[symbol]
	return ok
}

// Settled returns the rate the reference market settled the contract's
// period ending at at; while it is not known, the next fetch asks for it.
func (f *MarkFeed) Settled(symbol string, at time.Time) (domain.SettledFunding, bool) {
	k := settledKey{symbol, at}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.settled[k]
	if !ok {
		f.wanted[k] = true
	}
	return s, ok
}

// follow reads the followed contracts: those with a reference market,
// each at its own perpetual (its reference_symbol: BTC-USDT-PERP at
// USDⓈ-M BTCUSDT, BTC-USD-PERP at COIN-M BTCUSD_PERP).
func (f *MarkFeed) follow(ctx context.Context) map[string]ports.Reference {
	out := map[string]ports.Reference{}
	for symbol, ref := range f.refs.Get(ctx) {
		if ref.Market != ports.MarketSpot {
			out[symbol] = ref
		}
	}
	f.mu.Lock()
	f.followed = out
	for symbol := range f.latest {
		if _, ok := out[symbol]; !ok {
			delete(f.latest, symbol)
		}
	}
	f.mu.Unlock()
	return out
}

// drop forgets the marks when the feed goes off, so nothing follows a
// stale one.
func (f *MarkFeed) drop() {
	f.mu.Lock()
	f.followed, f.latest = map[string]ports.Reference{}, map[string]receivedMark{}
	f.mu.Unlock()
}

// markGroup is the contracts of one stream connection, all of one
// futures market.
type markGroup struct {
	market string
	refs   []ports.Reference
}

// markGroups splits the followed contracts by market into connections of
// at most markStreamsPerConn, in symbol order.
func markGroups(m map[string]ports.Reference) []markGroup {
	byMarket := map[string][]ports.Reference{}
	for _, ref := range m {
		byMarket[ref.Market] = append(byMarket[ref.Market], ref)
	}
	var out []markGroup
	for _, market := range []string{ports.MarketUSDM, ports.MarketCoinM} {
		refs := byMarket[market]
		slices.SortFunc(refs, func(x, y ports.Reference) int { return strings.Compare(x.Symbol, y.Symbol) })
		for chunk := range slices.Chunk(refs, markStreamsPerConn) {
			out = append(out, markGroup{market: market, refs: chunk})
		}
	}
	return out
}

// Run follows the marks while market.reference_feed is on (an app.Loop
// body), restarting the connections when the followed contracts change.
func (f *MarkFeed) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if !f.enabled() {
			f.drop()
			sleep(ctx, f.recheck)
			continue
		}
		followed := f.follow(ctx)
		if len(followed) == 0 {
			sleep(ctx, f.recheck)
			continue
		}
		session, stop := context.WithCancel(ctx)
		var wg sync.WaitGroup
		for _, g := range markGroups(followed) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.connection(session, g)
			}()
		}
		mapped := f.now()
		for session.Err() == nil {
			sleep(session, f.recheck)
			if session.Err() != nil {
				break
			}
			if !f.enabled() {
				stop()
				break
			}
			if f.now().Sub(mapped) < f.remap {
				continue
			}
			mapped = f.now()
			if !sameFollowed(f.follow(session), followed) {
				f.log.InfoContext(ctx, "reference marks: followed contracts changed")
				stop()
			}
		}
		stop()
		wg.Wait()
	}
	return nil
}

// connection keeps one stream connection up until ctx ends, with backoff
// after failures.
func (f *MarkFeed) connection(ctx context.Context, g markGroup) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := f.now()
		err := f.src.MarkStream(ctx, g.refs, func(m domain.ReferenceMark) {
			at := f.now()
			f.mu.Lock()
			if _, ok := f.followed[m.Symbol]; ok {
				f.latest[m.Symbol] = receivedMark{mark: m, at: at}
			}
			f.mu.Unlock()
		})
		if ctx.Err() != nil {
			return
		}
		f.failures.Inc()
		f.log.WarnContext(ctx, "reference mark stream failed", "market", g.market, "contracts", len(g.refs), "error", err)
		if f.now().Sub(started) > time.Minute {
			backoff = time.Second
		}
		sleep(ctx, backoff)
		backoff = min(2*backoff, 30*time.Second)
	}
}

// RunFunding fetches the settled rates Marks waits for every fundingPoll
// (an app.Loop body).
func (f *MarkFeed) RunFunding(ctx context.Context) error {
	for ctx.Err() == nil {
		sleep(ctx, fundingPoll)
		if ctx.Err() == nil {
			f.FetchFunding(ctx)
		}
	}
	return nil
}

// FetchFunding asks the reference market for the settled rates wanted,
// one request per market and funding time (or per contract where the
// market wants one), and keeps what it finds.
func (f *MarkFeed) FetchFunding(ctx context.Context) {
	type batch struct {
		market string
		at     time.Time
	}
	now := f.now()
	batches := map[batch][]ports.Reference{}
	f.mu.Lock()
	for k := range f.wanted {
		ref, ok := f.followed[k.symbol]
		switch {
		case now.Sub(k.at) > fundingForget || !ok:
			delete(f.wanted, k) // too old, or no longer followed
		case !now.Before(k.at):
			b := batch{ref.Market, k.at}
			batches[b] = append(batches[b], ref)
		}
	}
	for k := range f.settled {
		if now.Sub(k.at) > fundingForget {
			delete(f.settled, k)
		}
	}
	f.mu.Unlock()
	for b, refs := range batches {
		list, err := f.src.SettledFunding(ctx, refs, b.at, b.at.Add(fundingWindow))
		if err != nil {
			if ctx.Err() == nil {
				f.fetches.WithLabelValues("failed").Inc()
				f.log.WarnContext(ctx, "reference funding rates not read", "funding_time", b.at, "contracts", len(refs), "error", err)
			}
			continue
		}
		found := 0
		f.mu.Lock()
		for _, s := range list {
			// The market's funding time is a little after the period's end.
			if s.FundingTime.Before(b.at) || !s.FundingTime.Before(b.at.Add(fundingWindow)) {
				continue
			}
			k := settledKey{s.Symbol, b.at}
			if f.wanted[k] {
				delete(f.wanted, k)
				f.settled[k] = s
				found++
			}
		}
		f.mu.Unlock()
		result := "found"
		if found < len(refs) {
			result = "pending"
		}
		f.fetches.WithLabelValues(result).Inc()
	}
}
