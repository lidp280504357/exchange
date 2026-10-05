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
	Log         *slog.Logger
	Now         func() time.Time
	Metrics     *Metrics
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
	reg.MustRegister(m.Ops, m.InterestRun, m.Reconciled, m.LastReconcile)
	return m
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

// Terms returns an account's terms: the cross account's, or its pair's.
func (c Catalog) Terms(a domain.Account) (domain.Terms, error) {
	if a.IsCross() {
		return c.Cross, nil
	}
	p, ok := c.Pairs[a.Symbol]
	if !ok || !p.Isolated {
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
