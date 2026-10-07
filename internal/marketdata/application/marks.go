package application

import (
	"context"
	"fmt"
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
	riskv1 "github.com/skill/exchange/api/gen/go/exchange/risk/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
)

// Perpetual contract prices and funding (requirements §11.7; plan §7.3
// task 3).
const (
	// MarkInterval is how often the prices are computed and the premium
	// index is sampled.
	MarkInterval = time.Second
	// MarkTimeout: a contract without a mark price for this long is
	// reported degraded (§11.7: 10 seconds).
	MarkTimeout = 10 * time.Second
	// fundingSave is how often a running period's samples are stored: a
	// restart loses at most this much of them.
	fundingSave = time.Minute
	// fundingPush is how often an unchanged funding estimate is pushed
	// again, for gateways that started since.
	fundingPush = 10 * time.Second
)

// ReasonIndexSources is the degradation reason when fewer index sources
// are usable than required.
const ReasonIndexSources = "INDEX_SOURCES"

// Where a contract's prices and funding rates came from (coin-M design
// §3.1, the source of MarkPriceUpdated and FundingRateUpdated): computed
// here, or the Binance contract's the contract follows.
const (
	MarkSourcePlatform = "PLATFORM"
	MarkSourceBinance  = "BINANCE"
	// settledEstimate labels, in metrics and logs, a period settled at the
	// last rate Binance estimated for it, its settled rate not found in
	// time; its source is BINANCE.
	settledEstimate = "BINANCE_ESTIMATE"
)

const (
	// DefaultReferenceStale is how old the reference market's mark price
	// may be before the self-computed one stands in
	// (MARK_SOURCE_STALE_SECONDS).
	DefaultReferenceStale = 10 * time.Second
	// settleWait is how long an ended period of a contract that follows
	// the reference market waits for the rate the market settled.
	settleWait = 2 * time.Minute
	// referenceRecover is how long the reference market must stream its
	// mark again before the prices follow it back from the self-computed
	// ones (review EL C37: a flapping stream must not switch them back and
	// forth); referenceLive is how old its latest mark may be on each tick
	// of that while (it streams every second; review ET ①: a mark fresh
	// enough to use, under the 10 seconds, but from a stream gone quiet
	// again, does not count).
	referenceRecover = 5 * time.Second
	referenceLive    = 2 * time.Second
	// basisDecimals is the precision of the basis while it is Binance's
	// (that of the self-computed one, domain's ratio precision).
	basisDecimals = 12
)

// settleMarkTolerance: the mark price the reference market settled a
// period at prices the period's payments when it is within this of the
// contract's current mark (review EM: one bad figure must not price every
// position's payment); else the current mark does.
var settleMarkTolerance = decimal.RequireFromString("0.05")

// ReferenceMarks is the reference market's mark prices and settled rates
// (MarkFeed).
type ReferenceMarks interface {
	// Latest returns the contract's latest reference mark and when it
	// arrived.
	Latest(symbol string) (domain.ReferenceMark, time.Time, bool)
	// Settled returns the rate the market settled the contract's period
	// ending at at, false while it is not known.
	Settled(symbol string, at time.Time) (domain.SettledFunding, bool)
}

// IndexSources gives the fresh spot prices of an index symbol, one per
// source; Marks applies the weights.
type IndexSources interface {
	Prices(symbol string) []domain.SourcePrice
}

// MarkPrice is a contract's latest prices and its funding estimate.
type MarkPrice struct {
	Symbol      string
	IndexSymbol string
	// Mark, Index, Basis and Components are from the last computation
	// that succeeded at At; zero before the first.
	Mark       decimal.Decimal
	Index      decimal.Decimal
	Basis      decimal.Decimal
	Components []domain.IndexComponent
	At         time.Time
	// The running funding period, ending at NextFunding.
	FundingRate  decimal.Decimal
	Premium      decimal.Decimal
	InterestRate decimal.Decimal
	Samples      int64
	NextFunding  time.Time
	// Degraded is set once SystemDegraded went out, until the mark price
	// is back.
	Degraded bool
	// Source is where Mark, Index and Basis came from (MarkSourcePlatform
	// or MarkSourceBinance), FundingSource where FundingRate did;
	// Computed and ComputedIndex are the self-computed prices of the same
	// tick, zero when they could not be computed.
	Source        string
	FundingSource string
	Computed      decimal.Decimal
	ComputedIndex decimal.Decimal
	// SourceDegraded is set while market.reference_mark is on for the
	// contract but the reference market's mark price is stale.
	SourceDegraded bool
}

// Marks computes the contracts' index and mark prices every MarkInterval,
// samples the premium index into the running funding period, settles the
// periods that ended, and publishes it all: MarkPriceUpdated,
// IndexPriceUpdated and FundingRateUpdated on market.candle.events,
// SystemDegraded and SystemRecovered on risk.events. Only one instance may
// run: the samples of a period live in its memory between saves.
type Marks struct {
	svc         *Service
	instruments ports.Instruments
	sources     IndexSources
	store       ports.Store
	pusher      *Pusher
	pub         kafka.Publisher
	events      *event.Factory
	weights     map[string]int32
	minSources  int
	log         *slog.Logger
	now         func() time.Time
	// refBook is the reference market's book of a contract while it is
	// usable (UseReferenceBooks); the engine's book otherwise.
	refBook func(symbol string, limit int) (bids, asks []domain.Level, ok bool)
	// refMarks are the reference market's mark prices, followed where
	// follows says so (FollowReference); stale bounds their age.
	refMarks ReferenceMarks
	follows  func(symbol string) bool
	// overlay is the price events' factors (nil: none; WithOverlay): with
	// risk they move the index, and the mark is computed meanwhile.
	overlay *Overlay
	stale   time.Duration

	// Loop state, touched by the loop only.
	contracts map[string]*contractMarks
	specs     []ports.Contract
	restored  map[string]ports.FundingPeriod
	due       map[string]bool
	started   time.Time

	mu     sync.Mutex
	latest map[string]MarkPrice

	age            *prometheus.GaugeVec
	used           *prometheus.GaugeVec
	degraded       *prometheus.GaugeVec
	settled        *prometheus.CounterVec
	source         *prometheus.GaugeVec
	sourceDegraded *prometheus.GaugeVec
	gap            *prometheus.GaugeVec
}

// UseReferenceBooks has the premium read the reference market's book of a
// contract (Books.Levels) while it is usable (ADR-0015: HOUSE trades at
// its prices), the engine's book otherwise. Call it before Run.
func (m *Marks) UseReferenceBooks(levels func(symbol string, limit int) (bids, asks []domain.Level, ok bool)) {
	m.refBook = levels
}

// FollowReference has the contracts follow the reference market's mark
// price, index price and funding rate where follows says so
// (market.reference_mark), while the market's mark is at most stale old
// (coin-M design §3.1); the self-computed prices stand in otherwise. Call
// it before Run.
func (m *Marks) FollowReference(marks ReferenceMarks, follows func(symbol string) bool, stale time.Duration) {
	m.refMarks, m.follows, m.stale = marks, follows, stale
	if m.stale <= 0 {
		m.stale = DefaultReferenceStale
	}
}

// book is the book a contract's premium reads.
func (m *Marks) book(symbol string) (bids, asks []domain.Level) {
	if m.refBook != nil {
		if bids, asks, ok := m.refBook(symbol, PublicDepth); ok {
			return bids, asks
		}
	}
	return m.svc.Book(symbol)
}

type contractMarks struct {
	spec   ports.Contract
	basis  domain.Basis
	latest MarkPrice
	// The running funding period: its end and premium samples.
	period  time.Time
	sum     decimal.Decimal
	samples int64
	savedAt time.Time
	unsaved bool
	// settle means a period that ended may be unsettled.
	settle bool
	// degradedAt is set while the contract is reported degraded.
	degradedAt time.Time
	// refRates are the last rates the reference market estimated for the
	// periods ending at their keys; refApart marks the periods the market
	// ends at another time (its rate is not followed then).
	refRates map[time.Time]decimal.Decimal
	refApart map[time.Time]bool
	// refBackSince is when the reference market's mark came back fresh
	// while the source was degraded (referenceRecover).
	refBackSince time.Time
	// overlaid is set while a price event that reaches risk has the mark
	// computed, and until the reference market's mark is followed again
	// after it: as after the source's loss, once it streamed for
	// referenceRecover (review GD).
	overlaid bool

	pushedRate   decimal.Decimal
	pushedSource string
	pushedPeriod time.Time
	pushedAt     time.Time
}

// MarksConfig configures Marks.
type MarksConfig struct {
	// MinSources is the fewest index sources a price needs (§11.7: 2).
	MinSources int
	// Weights of the index sources by name; 1 when not listed, 0 turns a
	// source off.
	Weights map[string]int32
}

// NewMarks registers the contract price metrics with reg. sources may be
// nil: every contract then degrades.
func NewMarks(svc *Service, instruments ports.Instruments, sources IndexSources, store ports.Store, pusher *Pusher,
	pub kafka.Publisher, events *event.Factory, cfg MarksConfig, log *slog.Logger, reg prometheus.Registerer,
) *Marks {
	m := &Marks{
		svc: svc, instruments: instruments, sources: sources, store: store, pusher: pusher, pub: pub, events: events,
		weights: cfg.Weights, minSources: max(cfg.MinSources, 1), log: log, now: time.Now,
		contracts: map[string]*contractMarks{}, restored: map[string]ports.FundingPeriod{}, due: map[string]bool{},
		latest: map[string]MarkPrice{},
		age: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_mark_age_seconds", Help: "Age of the contract's mark price; -1 while there is none.",
		}, []string{"symbol"}),
		used: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_index_sources", Help: "Index sources the contract's last index price used.",
		}, []string{"symbol"}),
		degraded: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_contract_degraded", Help: "1 while the contract is reported degraded (no mark price for 10 seconds).",
		}, []string{"symbol"}),
		settled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_funding_settled_total", Help: "Funding periods settled, by the source of the rate.",
		}, []string{"source"}),
		source: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_mark_source", Help: "1 while the contract's prices follow the reference market's mark price, 0 while self-computed.",
		}, []string{"symbol"}),
		sourceDegraded: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_mark_source_degraded",
			Help: "1 while market.reference_mark is on for the contract but the reference market's mark price is stale (self-computed instead).",
		}, []string{"symbol"}),
		gap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_mark_reference_gap",
			Help: "The self-computed mark price relative to the reference market's: computed / reference - 1, while both are at hand.",
		}, []string{"symbol"}),
	}
	reg.MustRegister(m.age, m.used, m.degraded, m.settled, m.source, m.sourceDegraded, m.gap)
	return m
}

// Latest returns the contract's latest prices; false for a symbol that is
// not a contract Marks has seen.
func (m *Marks) Latest(symbol string) (MarkPrice, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.latest[symbol]
	return p, ok
}

// Settled returns up to limit settled funding periods of a contract with
// funding times in [from, to), newest first.
func (m *Marks) Settled(ctx context.Context, symbol string, from, to time.Time, limit int) ([]ports.FundingPeriod, error) {
	return m.store.Read().Funding().Settled(ctx, symbol, from, to, limit)
}

// Load reads the periods not settled yet: the running ones continue, the
// ended ones settle once a mark price is at hand.
func (m *Marks) Load(ctx context.Context) error {
	periods, err := m.store.Read().Funding().Unsettled(ctx)
	if err != nil {
		return err
	}
	now := m.now()
	for _, p := range periods {
		if p.FundingTime.After(now) {
			m.restored[p.Symbol] = p
		} else {
			m.due[p.Symbol] = true
		}
	}
	m.started = now
	return nil
}

// Run computes every MarkInterval until ctx ends (an app.Loop body).
func (m *Marks) Run(ctx context.Context) error {
	for {
		err := m.Load(ctx)
		if err == nil {
			break
		}
		m.log.WarnContext(ctx, "funding periods not loaded", "error", err)
		sleep(ctx, 5*time.Second)
		if ctx.Err() != nil {
			return nil
		}
	}
	tick := time.NewTicker(MarkInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			m.Tick(ctx)
		}
	}
}

// Tick computes every contract once and publishes the results.
func (m *Marks) Tick(ctx context.Context) {
	now := m.now()
	if specs, err := m.instruments.Contracts(ctx); err != nil {
		m.log.WarnContext(ctx, "contracts not loaded; using the last list", "error", err)
	} else {
		m.specs = specs
	}
	var updates []Update
	indexes := map[string]bool{}
	listed := map[string]bool{}
	for _, spec := range m.specs {
		listed[spec.Symbol] = true
		st := m.state(spec)
		updates = append(updates, m.tickContract(ctx, st, now, indexes)...)
		m.mu.Lock()
		m.latest[spec.Symbol] = st.latest
		m.mu.Unlock()
		if st.latest.At.IsZero() {
			m.age.WithLabelValues(spec.Symbol).Set(-1)
		} else {
			m.age.WithLabelValues(spec.Symbol).Set(now.Sub(st.latest.At).Seconds())
		}
	}
	// A delisted contract leaves the answers; its unsettled periods stay
	// stored.
	m.mu.Lock()
	for symbol := range m.latest {
		if !listed[symbol] {
			delete(m.latest, symbol)
			delete(m.contracts, symbol)
		}
	}
	m.mu.Unlock()
	if err := m.pusher.Push(ctx, updates); err != nil && ctx.Err() == nil {
		m.pusher.failed.Inc()
		m.log.WarnContext(ctx, "contract prices not pushed", "error", err)
	}
}

// state returns the contract's loop state, created on first sight with
// the samples of its running period restored.
func (m *Marks) state(spec ports.Contract) *contractMarks {
	st, ok := m.contracts[spec.Symbol]
	if !ok {
		st = &contractMarks{settle: m.due[spec.Symbol], refRates: map[time.Time]decimal.Decimal{}, refApart: map[time.Time]bool{}}
		m.contracts[spec.Symbol] = st
	}
	st.spec = spec
	st.latest.Symbol, st.latest.IndexSymbol, st.latest.InterestRate = spec.Symbol, spec.IndexSymbol, spec.InterestRate
	return st
}

// prices returns the index sources of symbol with their weights.
func (m *Marks) prices(symbol string) []domain.SourcePrice {
	if m.sources == nil {
		return nil
	}
	prices := m.sources.Prices(symbol)
	f := one
	if m.overlay != nil { // a price event that reaches risk moves the index (J0 contract §2.3)
		f = m.overlay.RiskFactor(symbol)
	}
	for i := range prices {
		prices[i].Weight = 1
		if w, ok := m.weights[prices[i].Source]; ok {
			prices[i].Weight = w
		}
		prices[i].Price = domain.ScalePrice(prices[i].Price, f, m.overlay.Tick(symbol))
	}
	return prices
}

// WithOverlay has the contract prices follow the price events that reach
// risk (design 2026-10-07, general price control): the index from the
// scaled pair, the mark computed (PLATFORM) meanwhile and until the
// reference market's mark has streamed referenceRecover after their end.
// Call it before Run.
func (m *Marks) WithOverlay(o *Overlay) { m.overlay = o }

func (m *Marks) tickContract(ctx context.Context, st *contractMarks, now time.Time, indexes map[string]bool) []Update {
	spec := st.spec
	var out []Update
	// A new funding period: store the one that ended (retried next tick if
	// that fails) and start sampling afresh.
	if end := domain.NextFunding(now, spec.FundingIntervalHours); !end.Equal(st.period) {
		if !st.period.IsZero() {
			if err := m.savePeriod(ctx, st, now); err != nil {
				m.log.WarnContext(ctx, "funding period not stored", "symbol", spec.Symbol, "error", err)
				return nil
			}
			st.settle = st.settle || !st.period.After(now)
		}
		st.period, st.sum, st.samples, st.unsaved = end, decimal.Zero, 0, true
		if p, ok := m.restored[spec.Symbol]; ok {
			switch {
			case p.FundingTime.Equal(end):
				st.sum, st.samples = p.PremiumSum, p.Samples
			case !p.FundingTime.After(now): // it ended since the load
				st.settle = true
			}
			delete(m.restored, spec.Symbol)
		}
	}

	prices := m.prices(spec.IndexSymbol)
	need := m.minSources
	if len(prices) == 1 && prices[0].Source == SourcePlatform {
		need = 1 // the platform's own market is all an unfollowed pair has
	}
	index, comps, err := domain.Index(prices, need)
	included := 0
	for _, c := range comps {
		if c.Included {
			included++
		}
	}
	m.used.WithLabelValues(spec.Symbol).Set(float64(included))
	// The self-computed prices and premium samples go on whatever the
	// prices follow: they stand in for the reference market's.
	computed := err == nil
	st.latest.Computed, st.latest.ComputedIndex = decimal.Zero, decimal.Zero
	if computed {
		bids, asks := m.book(spec.Symbol)
		var bid, ask decimal.Decimal
		if len(bids) > 0 {
			bid = bids[0].Price
		}
		if len(asks) > 0 {
			ask = asks[0].Price
		}
		st.basis.Add(domain.BasisSample(index, bid, ask))
		impact := domain.ImpactPrice
		if spec.Inverse() {
			impact = domain.ImpactPriceInverse // the notional is contracts (coin-M §2.4)
		}
		impactBid, bidOK := impact(bids, spec.ImpactNotional)
		impactAsk, askOK := impact(asks, spec.ImpactNotional)
		st.sum = st.sum.Add(domain.Premium(index, impactBid, impactAsk, bidOK, askOK))
		st.samples++
		st.unsaved = true
		st.latest.Computed, st.latest.ComputedIndex = domain.Mark(index, st.basis.EMA), index
		if !indexes[spec.IndexSymbol] {
			indexes[spec.IndexSymbol] = true
			out = append(out, Update{spec.IndexSymbol, indexProto(spec.IndexSymbol, index, comps, now)})
		}
	}
	ref, follow := m.reference(ctx, st, now)
	fresh := follow || computed
	switch {
	case follow:
		// The basis is Binance's mark over its index.
		st.latest.Mark, st.latest.Index, st.latest.Source = ref.Mark, ref.Index, MarkSourceBinance
		st.latest.Basis = ref.Mark.Sub(ref.Index).DivRound(ref.Index, basisDecimals)
	case computed:
		st.latest.Mark, st.latest.Index, st.latest.Source = st.latest.Computed, index, MarkSourcePlatform
		st.latest.Basis = st.basis.EMA
	}
	if fresh {
		st.latest.Components, st.latest.At = comps, now
		m.recovered(ctx, st)
	} else {
		m.checkDegraded(ctx, st, now, fmt.Sprintf("%d of the %d index sources required are usable", included, need))
	}
	if follow {
		m.source.WithLabelValues(spec.Symbol).Set(1)
	} else {
		m.source.WithLabelValues(spec.Symbol).Set(0)
	}

	st.latest.Premium = domain.AveragePremium(st.sum, st.samples)
	st.latest.FundingRate = domain.FundingRate(st.latest.Premium, spec.InterestRate, spec.FundingCap)
	st.latest.FundingSource = MarkSourcePlatform
	if r, ok := st.refRates[st.period]; ok && follow {
		st.latest.FundingRate, st.latest.FundingSource = r, MarkSourceBinance
	}
	st.latest.Samples, st.latest.NextFunding = st.samples, st.period
	if fresh {
		out = append(out, Update{spec.Symbol, &marketv1.MarkPriceUpdated{
			Symbol: spec.Symbol, MarkPrice: st.latest.Mark.String(), IndexPrice: st.latest.Index.String(), Basis: st.latest.Basis.String(),
			FundingRate: st.latest.FundingRate.String(), NextFundingTime: timestamppb.New(st.period), ComputedAt: timestamppb.New(now),
			Source: st.latest.Source,
		}})
	}
	if !st.latest.FundingRate.Equal(st.pushedRate) || st.latest.FundingSource != st.pushedSource || !st.period.Equal(st.pushedPeriod) ||
		now.Sub(st.pushedAt) >= fundingPush {
		msg := &marketv1.FundingRateUpdated{
			Symbol: spec.Symbol, FundingRate: st.latest.FundingRate.String(), Premium: st.latest.Premium.String(),
			InterestRate: spec.InterestRate.String(), Samples: st.samples, FundingTime: timestamppb.New(st.period),
			Source: st.latest.FundingSource,
		}
		if msg.Source == MarkSourceBinance {
			msg.Premium, msg.Samples = "", 0 // not what the rate came from
		}
		out = append(out, Update{spec.Symbol, msg})
		st.pushedRate, st.pushedSource, st.pushedPeriod, st.pushedAt = st.latest.FundingRate, st.latest.FundingSource, st.period, now
	}
	if st.unsaved && now.Sub(st.savedAt) >= fundingSave {
		if err := m.savePeriod(ctx, st, now); err != nil {
			m.log.WarnContext(ctx, "funding samples not stored", "symbol", spec.Symbol, "error", err)
		}
	}
	// Ended periods settle at the first fresh mark price.
	if st.settle && fresh {
		finals, done, err := m.settle(ctx, st, now)
		out = append(out, finals...)
		switch {
		case err != nil:
			m.log.WarnContext(ctx, "funding not settled", "symbol", spec.Symbol, "error", err)
		case done:
			st.settle = false
		}
	}
	for end := range st.refRates {
		if now.Sub(end) > fundingForget {
			delete(st.refRates, end)
		}
	}
	for end := range st.refApart {
		if now.Sub(end) > fundingForget {
			delete(st.refApart, end)
		}
	}
	return out
}

// reference returns the reference market's mark of the contract when its
// prices follow it: market.reference_mark is on for it and the market's
// latest mark is at most stale old. With the flag on and the mark stale
// (the start's first stale seconds aside) the source is degraded: the
// self-computed prices stand in, without reduce-only, and
// MarkPriceSourceDegraded alerts. It keeps the market's estimate of the
// running period's rate, for the settlement, and watches the gap between
// the two mark prices whether the flag is on or not.
func (m *Marks) reference(ctx context.Context, st *contractMarks, now time.Time) (domain.ReferenceMark, bool) {
	symbol := st.spec.Symbol
	if m.refMarks == nil {
		return domain.ReferenceMark{}, false
	}
	ref, received, ok := m.refMarks.Latest(symbol)
	fresh := ok && now.Sub(received) <= m.stale && ref.Mark.IsPositive() && ref.Index.IsPositive()
	if fresh && st.latest.Computed.IsPositive() {
		gap, _ := st.latest.Computed.Div(ref.Mark).Sub(decimal.NewFromInt(1)).Float64()
		m.gap.WithLabelValues(symbol).Set(gap)
	}
	if m.overlay != nil && m.overlay.MarkComputed(st.spec.IndexSymbol) {
		// The reference market's mark knows nothing of the price event.
		m.markSource(ctx, st, false, "")
		st.overlaid, st.refBackSince = true, time.Time{}
		return domain.ReferenceMark{}, false
	}
	if !m.follows(symbol) {
		m.markSource(ctx, st, false, "")
		st.overlaid = false
		return domain.ReferenceMark{}, false
	}
	if !fresh {
		st.refBackSince = time.Time{}
		last := received
		if last.Before(m.started) {
			last = m.started
		}
		if now.Sub(last) > m.stale {
			detail := "no mark price from the reference market"
			if ok {
				detail = fmt.Sprintf("the reference market's mark price is %s old", now.Sub(received).Round(time.Second))
			}
			m.markSource(ctx, st, true, detail)
		}
		return domain.ReferenceMark{}, false
	}
	// Back after the source degraded or a price event: the self-computed
	// prices go on until the market has streamed its mark for
	// referenceRecover, each tick's at most referenceLive old (none of its
	// own at hand: follow at once).
	if (st.latest.SourceDegraded || st.overlaid) && st.latest.Computed.IsPositive() {
		if now.Sub(received) > referenceLive {
			st.refBackSince = time.Time{}
			return domain.ReferenceMark{}, false
		}
		if st.refBackSince.IsZero() {
			st.refBackSince = now
		}
		if now.Sub(st.refBackSince) < referenceRecover {
			return domain.ReferenceMark{}, false
		}
	}
	st.refBackSince, st.overlaid = time.Time{}, false
	m.markSource(ctx, st, false, "")
	if ref.HasRate {
		if ref.NextFunding.Equal(st.period) {
			r, cut := capped(st, ref.FundingRate)
			if prev, had := st.refRates[st.period]; cut && (!had || !prev.Equal(r)) {
				m.log.WarnContext(ctx, "the reference market's funding rate estimate is past the contract's cap: capped",
					"symbol", symbol, "rate", ref.FundingRate.String(), "cap", st.spec.FundingCap.String())
			}
			st.refRates[st.period] = r
		} else if !st.refApart[st.period] {
			st.refApart[st.period] = true
			m.log.WarnContext(ctx, "the reference market ends the funding period at another time: its rate is not followed for it",
				"symbol", symbol, "period_end", st.period, "reference_next_funding", ref.NextFunding)
		}
	}
	return ref, true
}

// markSource records whether the reference market's mark price is
// missing for a contract that should follow it.
func (m *Marks) markSource(ctx context.Context, st *contractMarks, degraded bool, detail string) {
	switch {
	case degraded && !st.latest.SourceDegraded:
		m.log.ErrorContext(ctx, "reference mark price stale: the self-computed prices stand in", "symbol", st.spec.Symbol, "detail", detail)
	case !degraded && st.latest.SourceDegraded:
		m.log.InfoContext(ctx, "reference mark price back: the contract follows it again", "symbol", st.spec.Symbol)
	default:
		return
	}
	st.latest.SourceDegraded = degraded
	if degraded {
		m.sourceDegraded.WithLabelValues(st.spec.Symbol).Set(1)
	} else {
		m.sourceDegraded.WithLabelValues(st.spec.Symbol).Set(0)
	}
}

// followsFunding reports whether a contract's period ending at end settles
// at the reference market's rate: the contract follows the market and
// the market did not end the period at another time.
func (m *Marks) followsFunding(st *contractMarks, end time.Time) bool {
	return m.refMarks != nil && m.follows(st.spec.Symbol) && !st.refApart[end]
}

func (m *Marks) savePeriod(ctx context.Context, st *contractMarks, now time.Time) error {
	err := m.store.Read().Funding().Save(ctx, ports.FundingPeriod{
		Symbol: st.spec.Symbol, FundingTime: st.period, PremiumSum: st.sum, Samples: st.samples,
	})
	if err == nil {
		st.savedAt, st.unsaved = now, false
	}
	return err
}

// settle fixes the rates of the contract's periods that ended, at the
// current mark price (§11.7: the settlement time's) and the self-computed
// rate; a contract that follows the reference market takes the rate and
// mark price the market settled the period at, waiting for them up to
// settleWait, then the last rate the market estimated for the period,
// then the self-computed one. done is false while a period waits.
func (m *Marks) settle(ctx context.Context, st *contractMarks, now time.Time) ([]Update, bool, error) {
	periods, err := m.store.Read().Funding().Unsettled(ctx)
	if err != nil {
		return nil, false, err
	}
	var out []Update
	done := true
	for _, p := range periods {
		if p.Symbol != st.spec.Symbol || p.FundingTime.After(now) {
			continue
		}
		p.Premium = domain.AveragePremium(p.PremiumSum, p.Samples)
		p.InterestRate = st.spec.InterestRate
		p.Rate = domain.FundingRate(p.Premium, p.InterestRate, st.spec.FundingCap)
		p.MarkPrice, p.IndexPrice, p.Source = st.latest.Mark, st.latest.Index, MarkSourcePlatform
		how := MarkSourcePlatform
		if m.followsFunding(st, p.FundingTime) {
			s, found := m.refMarks.Settled(p.Symbol, p.FundingTime)
			estimate, estimated := st.refRates[p.FundingTime]
			switch {
			case found:
				rate, cut := capped(st, s.Rate)
				if cut {
					m.log.WarnContext(ctx, "the reference market settled the period past the contract's cap: capped",
						"symbol", p.Symbol, "funding_time", p.FundingTime, "rate", s.Rate.String(), "cap", st.spec.FundingCap.String())
				}
				p.Rate, p.Source, how = rate, MarkSourceBinance, MarkSourceBinance
				switch {
				case !s.Mark.IsPositive():
				case !p.MarkPrice.IsPositive() || s.Mark.Div(p.MarkPrice).Sub(decimal.NewFromInt(1)).Abs().LessThanOrEqual(settleMarkTolerance):
					p.MarkPrice = s.Mark
				default:
					m.log.WarnContext(ctx, "the reference market settled the period at a mark price far from the contract's: the contract's prices it",
						"symbol", p.Symbol, "funding_time", p.FundingTime, "reference_mark", s.Mark.String(), "mark", p.MarkPrice.String())
				}
			case now.Sub(p.FundingTime) < settleWait:
				done = false
				continue
			case estimated:
				p.Rate, p.Source, how = estimate, MarkSourceBinance, settledEstimate
			}
		}
		ok, err := m.store.Read().Funding().Settle(ctx, p)
		if err != nil {
			return out, false, err
		}
		if !ok {
			continue
		}
		m.settled.WithLabelValues(how).Inc()
		m.log.InfoContext(ctx, "funding period settled", "symbol", p.Symbol, "funding_time", p.FundingTime,
			"rate", p.Rate.String(), "source", how, "samples", p.Samples, "mark_price", p.MarkPrice.String())
		final := &marketv1.FundingRateUpdated{
			Symbol: p.Symbol, FundingRate: p.Rate.String(), Premium: p.Premium.String(), InterestRate: p.InterestRate.String(),
			Samples: p.Samples, FundingTime: timestamppb.New(p.FundingTime), Final: true,
			MarkPrice: p.MarkPrice.String(), IndexPrice: p.IndexPrice.String(), Source: p.Source,
		}
		if p.Source == MarkSourceBinance {
			final.Premium, final.Samples = "", 0
		}
		out = append(out, Update{p.Symbol, final})
	}
	return out, done, nil
}

// capped is the reference market's rate within the contract's cap
// (domain.CapRate, review EM: the spec's limit holds whatever the market
// says), and whether the cap took something off.
func capped(st *contractMarks, rate decimal.Decimal) (decimal.Decimal, bool) {
	r := domain.CapRate(rate, st.spec.FundingCap)
	return r, !r.Equal(rate)
}

// checkDegraded reports the contract degraded once it has had no mark
// price for MarkTimeout (since the start at the earliest), so a source
// reconnecting for a few seconds does not stop trading.
func (m *Marks) checkDegraded(ctx context.Context, st *contractMarks, now time.Time, detail string) {
	last := st.latest.At
	if last.Before(m.started) {
		last = m.started
	}
	if !st.degradedAt.IsZero() || now.Sub(last) < MarkTimeout {
		return
	}
	msg := &riskv1.SystemDegraded{Reason: ReasonIndexSources, Symbol: st.spec.Symbol, Detail: detail}
	if !st.latest.At.IsZero() {
		msg.LastMarkAt = timestamppb.New(st.latest.At)
	}
	if err := m.publishRisk(ctx, st.spec.Symbol, msg); err != nil {
		m.log.WarnContext(ctx, "degradation not reported", "symbol", st.spec.Symbol, "error", err)
		return
	}
	st.degradedAt, st.latest.Degraded = now, true
	m.degraded.WithLabelValues(st.spec.Symbol).Set(1)
	m.log.ErrorContext(ctx, "contract degraded", "symbol", st.spec.Symbol, "reason", ReasonIndexSources, "detail", detail)
}

// recovered reports a degraded contract whose mark price is back.
func (m *Marks) recovered(ctx context.Context, st *contractMarks) {
	if st.degradedAt.IsZero() {
		return
	}
	msg := &riskv1.SystemRecovered{Reason: ReasonIndexSources, Symbol: st.spec.Symbol, DegradedAt: timestamppb.New(st.degradedAt)}
	if err := m.publishRisk(ctx, st.spec.Symbol, msg); err != nil {
		m.log.WarnContext(ctx, "recovery not reported", "symbol", st.spec.Symbol, "error", err)
		return
	}
	st.degradedAt, st.latest.Degraded = time.Time{}, false
	m.degraded.WithLabelValues(st.spec.Symbol).Set(0)
	m.log.InfoContext(ctx, "contract prices recovered", "symbol", st.spec.Symbol)
}

// publishRisk queues a risk event on the outbox: a degradation puts the
// contract under reduce-only, so the report must not be lost with the
// process.
func (m *Marks) publishRisk(ctx context.Context, symbol string, msg proto.Message) error {
	env, err := m.events.New(ctx, msg, "symbol", symbol)
	if err != nil {
		return err
	}
	return m.store.Read().Emit(ctx, event.TopicRisk, env)
}

func indexProto(symbol string, price decimal.Decimal, comps []domain.IndexComponent, at time.Time) *marketv1.IndexPriceUpdated {
	out := &marketv1.IndexPriceUpdated{Symbol: symbol, Price: price.String(), ComputedAt: timestamppb.New(at)}
	for _, c := range comps {
		out.Components = append(out.Components, &marketv1.IndexComponent{
			Source: c.Source, Price: c.Price.String(), Weight: c.Weight, Included: c.Included,
		})
	}
	return out
}

// Contracts returns the latest prices of every contract, by symbol order.
func (m *Marks) Contracts() []MarkPrice {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MarkPrice, 0, len(m.latest))
	for _, p := range m.latest {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b MarkPrice) int { return strings.Compare(a.Symbol, b.Symbol) })
	return out
}
