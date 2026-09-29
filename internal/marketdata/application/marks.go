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

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	riskv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/risk/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
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

	// Loop state, touched by the loop only.
	contracts map[string]*contractMarks
	specs     []ports.Contract
	restored  map[string]ports.FundingPeriod
	due       map[string]bool
	started   time.Time

	mu     sync.Mutex
	latest map[string]MarkPrice

	age      *prometheus.GaugeVec
	used     *prometheus.GaugeVec
	degraded *prometheus.GaugeVec
	settled  prometheus.Counter
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

	pushedRate   decimal.Decimal
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
		settled: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_funding_settled_total", Help: "Funding periods settled.",
		}),
	}
	reg.MustRegister(m.age, m.used, m.degraded, m.settled)
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
		st = &contractMarks{settle: m.due[spec.Symbol]}
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
	for i := range prices {
		prices[i].Weight = 1
		if w, ok := m.weights[prices[i].Source]; ok {
			prices[i].Weight = w
		}
	}
	return prices
}

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

	index, comps, err := domain.Index(m.prices(spec.IndexSymbol), m.minSources)
	included := 0
	for _, c := range comps {
		if c.Included {
			included++
		}
	}
	m.used.WithLabelValues(spec.Symbol).Set(float64(included))
	fresh := err == nil
	if fresh {
		bids, asks := m.svc.Book(spec.Symbol)
		var bid, ask decimal.Decimal
		if len(bids) > 0 {
			bid = bids[0].Price
		}
		if len(asks) > 0 {
			ask = asks[0].Price
		}
		st.basis.Add(domain.BasisSample(index, bid, ask))
		impactBid, bidOK := domain.ImpactPrice(bids, spec.ImpactNotional)
		impactAsk, askOK := domain.ImpactPrice(asks, spec.ImpactNotional)
		st.sum = st.sum.Add(domain.Premium(index, impactBid, impactAsk, bidOK, askOK))
		st.samples++
		st.unsaved = true
		st.latest.Mark, st.latest.Index, st.latest.Basis = domain.Mark(index, st.basis.EMA), index, st.basis.EMA
		st.latest.Components, st.latest.At = comps, now
		if !indexes[spec.IndexSymbol] {
			indexes[spec.IndexSymbol] = true
			out = append(out, Update{spec.IndexSymbol, indexProto(spec.IndexSymbol, index, comps, now)})
		}
		m.recovered(ctx, st)
	} else {
		m.checkDegraded(ctx, st, now, fmt.Sprintf("%d of the %d index sources required are usable", included, m.minSources))
	}

	st.latest.Premium = domain.AveragePremium(st.sum, st.samples)
	st.latest.FundingRate = domain.FundingRate(st.latest.Premium, spec.InterestRate, spec.FundingCap)
	st.latest.Samples, st.latest.NextFunding = st.samples, st.period
	if fresh {
		out = append(out, Update{spec.Symbol, &marketv1.MarkPriceUpdated{
			Symbol: spec.Symbol, MarkPrice: st.latest.Mark.String(), IndexPrice: index.String(), Basis: st.basis.EMA.String(),
			FundingRate: st.latest.FundingRate.String(), NextFundingTime: timestamppb.New(st.period), ComputedAt: timestamppb.New(now),
		}})
	}
	if !st.latest.FundingRate.Equal(st.pushedRate) || !st.period.Equal(st.pushedPeriod) || now.Sub(st.pushedAt) >= fundingPush {
		out = append(out, Update{spec.Symbol, &marketv1.FundingRateUpdated{
			Symbol: spec.Symbol, FundingRate: st.latest.FundingRate.String(), Premium: st.latest.Premium.String(),
			InterestRate: spec.InterestRate.String(), Samples: st.samples, FundingTime: timestamppb.New(st.period),
		}})
		st.pushedRate, st.pushedPeriod, st.pushedAt = st.latest.FundingRate, st.period, now
	}
	if st.unsaved && now.Sub(st.savedAt) >= fundingSave {
		if err := m.savePeriod(ctx, st, now); err != nil {
			m.log.WarnContext(ctx, "funding samples not stored", "symbol", spec.Symbol, "error", err)
		}
	}
	// Ended periods settle at the first fresh mark price.
	if st.settle && fresh {
		finals, err := m.settle(ctx, st, now)
		out = append(out, finals...)
		if err != nil {
			m.log.WarnContext(ctx, "funding not settled", "symbol", spec.Symbol, "error", err)
		} else {
			st.settle = false
		}
	}
	return out
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
// current mark price (§11.7: the settlement time's).
func (m *Marks) settle(ctx context.Context, st *contractMarks, now time.Time) ([]Update, error) {
	periods, err := m.store.Read().Funding().Unsettled(ctx)
	if err != nil {
		return nil, err
	}
	var out []Update
	for _, p := range periods {
		if p.Symbol != st.spec.Symbol || p.FundingTime.After(now) {
			continue
		}
		p.Premium = domain.AveragePremium(p.PremiumSum, p.Samples)
		p.InterestRate = st.spec.InterestRate
		p.Rate = domain.FundingRate(p.Premium, p.InterestRate, st.spec.FundingCap)
		p.MarkPrice, p.IndexPrice = st.latest.Mark, st.latest.Index
		ok, err := m.store.Read().Funding().Settle(ctx, p)
		if err != nil {
			return out, err
		}
		if !ok {
			continue
		}
		m.settled.Inc()
		m.log.InfoContext(ctx, "funding period settled", "symbol", p.Symbol, "funding_time", p.FundingTime,
			"rate", p.Rate.String(), "samples", p.Samples, "mark_price", p.MarkPrice.String())
		out = append(out, Update{p.Symbol, &marketv1.FundingRateUpdated{
			Symbol: p.Symbol, FundingRate: p.Rate.String(), Premium: p.Premium.String(), InterestRate: p.InterestRate.String(),
			Samples: p.Samples, FundingTime: timestamppb.New(p.FundingTime), Final: true,
			MarkPrice: p.MarkPrice.String(), IndexPrice: p.IndexPrice.String(),
		}})
	}
	return out, nil
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
