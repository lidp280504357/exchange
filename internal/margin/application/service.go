// Package application runs margin trading (design 2026-10-06): transfers
// between SPOT and the margin accounts, borrowing from HOUSE, repaying,
// the hourly interest and the accounts' valuation. Every write that
// changes a user's margin accounts runs under the user's lock; a write to
// the ledger is recorded PENDING first, so that a crash or a ledger outage
// midway is finished by Recover with the same idempotency key.
package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// FeatureEligibility is the user-service feature margin trading checks
// (review CO, C9): MARGIN_TRADE, open to ACTIVE accounts while
// margin.enabled allows the user — the sites show the margin pages by the
// same answer. Only what adds risk asks: transfers in and borrowing
// (orders' borrows included); transfers out and repaying lower it and
// stay open.
const FeatureEligibility = "MARGIN_TRADE"

// Service runs margin trading.
type Service struct {
	Store       ports.Store
	Ledger      ports.Ledger
	Prices      ports.Prices
	Instruments ports.Instruments
	Eligibility ports.Eligibility
	Features    ports.Features
	// Trading is spot-trading-service's internal API, which a liquidation
	// cancels the account's orders and trades with.
	Trading ports.Trading
	Log     *slog.Logger
	Now     func() time.Time
	Metrics *Metrics
	// Touched hears of each user whose margin accounts changed (the
	// monitor, which values them again and publishes them); may be nil.
	Touched func(userID string)
	// Recheck is how long the reconciliation waits before it looks again
	// at a mismatch (0: 5 seconds for the loans, 2 for the pools; tests
	// shorten it).
	Recheck time.Duration
	// SettleWait bounds how long Settle waits for the orders it canceled
	// to let go of what they held, SettlePoll is how often it looks (0: 8
	// seconds and 200 ms). Sleep waits between the looks, nil for real
	// (the tests run the engine there).
	SettleWait, SettlePoll time.Duration
	Sleep                  func(ctx context.Context, d time.Duration) error
}

// touch tells Touched that a user's margin accounts changed.
func (s *Service) touch(userID string) {
	if s.Touched != nil && userID != "" {
		s.Touched(userID)
	}
}

// Metrics of margin trading.
type Metrics struct {
	// Ops counts the writes by kind (transfer, borrow, repay, interest)
	// and outcome (done, failed, pending).
	Ops *prometheus.CounterVec
	// InterestRun is when the last hourly run finished.
	InterestRun   prometheus.Gauge
	Reconciled    *prometheus.GaugeVec
	LastReconcile prometheus.Gauge
	// The monitor: the accounts it watches, when its last pass ended, the
	// warnings it gave and the accounts it found due for liquidation.
	Watched         prometheus.Gauge
	MonitorPass     prometheus.Gauge
	Warned          prometheus.Counter
	LiquidationsDue prometheus.Counter
	// Liquidations counts the liquidations started, by trigger;
	// LiquidationOldest is how long the oldest under way has run.
	Liquidations      *prometheus.CounterVec
	LiquidationOldest prometheus.Gauge
	// LiquidationsShort counts the liquidations waiting for the
	// insurance fund (SHORTFALL).
	LiquidationsShort prometheus.Gauge
}

// NewMetrics registers the metrics with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Ops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "margin_ops_total", Help: "Margin writes to the ledger by kind and outcome.",
		}, []string{"kind", "outcome"}),
		InterestRun: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "margin_interest_last_run_timestamp_seconds", Help: "When the last hourly interest run finished.",
		}),
		Reconciled: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "margin_reconcile_mismatches", Help: "Mismatches found by the last reconciliation, by check.",
		}, []string{"check"}),
		LastReconcile: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "margin_reconcile_last_success_timestamp_seconds", Help: "When the last reconciliation completed.",
		}),
	}
	m.Watched = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "margin_monitor_accounts", Help: "Margin accounts the monitor values each pass.",
	})
	m.MonitorPass = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "margin_monitor_last_pass_timestamp_seconds", Help: "When the margin level monitor last finished a pass.",
	})
	m.Warned = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "margin_warnings_total", Help: "Margin accounts that fell under their warning level.",
	})
	m.LiquidationsDue = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "margin_liquidations_due_total", Help: "Times a margin account was found at its liquidation level two passes in a row.",
	})
	m.Liquidations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "margin_liquidations_total", Help: "Margin liquidations started, by trigger (AUTO, MANUAL).",
	}, []string{"trigger"})
	for _, trigger := range []string{ports.TriggerAuto, ports.TriggerManual} {
		m.Liquidations.WithLabelValues(trigger) // 0 from the start: MarginLiquidationDue compares with it
	}
	m.LiquidationOldest = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "margin_liquidation_oldest_seconds", Help: "How long the oldest margin liquidation under way has run (0 without one).",
	})
	m.LiquidationsShort = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "margin_liquidations_shortfall", Help: "Margin liquidations waiting for the insurance fund to hold enough of an asset.",
	})
	reg.MustRegister(m.Ops, m.InterestRun, m.Reconciled, m.LastReconcile, m.Watched, m.MonitorPass, m.Warned, m.LiquidationsDue,
		m.Liquidations, m.LiquidationOldest, m.LiquidationsShort)
	return m
}

// SeedMetrics sets the gauge of the last finished interest run from the
// database: a restart does not blind MarginInterestStalled until the next
// hour is charged (review CR).
func (s *Service) SeedMetrics(ctx context.Context) error {
	if s.Metrics == nil {
		return nil
	}
	at, ok, err := s.Store.Read().Interest().LastFinished(ctx)
	if err != nil || !ok {
		return err
	}
	s.Metrics.InterestRun.Set(float64(at.Unix()))
	return nil
}

func (s *Service) count(kind, outcome string) {
	if s.Metrics != nil {
		s.Metrics.Ops.WithLabelValues(kind, outcome).Inc()
	}
}

// enabled refuses a user's margin writes while margin.enabled is off for
// them.
func (s *Service) enabled(userID string) error {
	if s.Features == nil || !s.Features.Enabled(flags.KeyMarginEnabled, flags.Subject{UserID: userID}) {
		return domain.ErrDisabled
	}
	return nil
}

// eligible asks user-service whether the user may trade now.
func (s *Service) eligible(ctx context.Context, userID, symbol string) error {
	allowed, reason, err := s.Eligibility.Check(ctx, userID, FeatureEligibility, symbol)
	if err != nil {
		return err
	}
	if !allowed {
		return apperr.New(apperr.KindForbidden, reason, "margin trading is not available to this account now")
	}
	return nil
}

// Catalog is the margin terms in force: the assets of the margin list
// with their decimals, the pairs, the cross account's terms.
type Catalog struct {
	Assets map[string]domain.AssetTerms
	Pairs  map[string]domain.Pair
	Cross  domain.Terms
}

// catalog loads the terms in force.
func (s *Service) catalog(ctx context.Context, r ports.Repos) (Catalog, error) {
	assets, err := r.Terms().Assets(ctx)
	if err != nil {
		return Catalog{}, err
	}
	pairs, err := r.Terms().Pairs(ctx)
	if err != nil {
		return Catalog{}, err
	}
	cross, ok, err := r.Terms().Cross(ctx)
	if err != nil {
		return Catalog{}, err
	}
	if !ok {
		cross = domain.DefaultTerms(domain.AccountCross, 3)
	}
	c := Catalog{Assets: make(map[string]domain.AssetTerms, len(assets)), Pairs: make(map[string]domain.Pair, len(pairs)), Cross: cross}
	for _, a := range assets {
		if a.Decimals, err = s.Instruments.Decimals(ctx, a.Asset); err != nil {
			return Catalog{}, err
		}
		c.Assets[a.Asset] = a
	}
	for _, p := range pairs {
		c.Pairs[p.Symbol] = p
	}
	return c, nil
}

// Terms returns an account's terms: the cross account's, or its pair's —
// also when the pair takes no new isolated account (those open keep their
// terms, review CY C17 ①).
func (c Catalog) Terms(a domain.Account) (domain.Terms, error) {
	if a.IsCross() {
		return c.Cross, nil
	}
	p, ok := c.Pairs[a.Symbol]
	if !ok {
		return domain.Terms{}, domain.ErrNotBorrowable.WithDetail("symbol", a.Symbol)
	}
	return p.Terms, nil
}

// Asset returns the terms of an asset the account may hold, or
// MARGIN_ASSET_NOT_BORROWABLE.
func (c Catalog) Asset(a domain.Account, asset string) (domain.AssetTerms, error) {
	t, listed := c.Assets[asset]
	var pair *domain.Pair
	if p, ok := c.Pairs[a.Symbol]; ok {
		pair = &p
	}
	if !domain.MayHold(a, pair, t, listed) {
		return domain.AssetTerms{}, domain.ErrNotBorrowable.WithDetail("asset", asset)
	}
	return t, nil
}

// holding returns the account's holding of one asset (zero without one).
func holding(list []domain.Holding, asset string) domain.Holding {
	for _, h := range list {
		if h.Asset == asset {
			return h
		}
	}
	return domain.Holding{Asset: asset}
}
