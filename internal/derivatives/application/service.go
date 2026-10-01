// Package application runs perpetual contract trading (requirements §5.8,
// §11.7; plan §7.3 task 5): settings, orders and their reservations, the
// engine's fills settled on positions and in the ledger, margin changes.
// Everything that changes a user's orders, positions or margin runs in a
// transaction holding the user's lock, so the plans computed from the
// stored state stay true until they are booked.
package application

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	derivativesv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

// FeatureDerivatives is the eligibility feature of opening orders (§5.4;
// user-service also checks the derivatives.trading flag for it).
const FeatureDerivatives = "DERIVATIVES_TRADE"

// Service runs contract trading.
type Service struct {
	Store       ports.Store
	Ledger      ports.Ledger
	Instruments ports.Instruments
	Eligibility ports.Eligibility
	Marks       ports.Marks
	Rates       ports.FundingRates
	// FeeFree are the accounts whose orders pay no fees: the simulated
	// market's bots (ASTRA design §4).
	FeeFree []string
	// HouseUser is HOUSE's account (HOUSE_USER_ID, ADR-0015): its side of
	// a trade against the reference liquidity has no order, and its
	// positions are never liquidated or deleveraged.
	HouseUser string
	// Features decides whether a contract's orders trade only with HOUSE;
	// nil means users always trade with each other.
	Features ports.Features
	Log      *slog.Logger
	Now      func() time.Time
	Metrics  *Metrics
	// Started is when the service started: missing mark prices count as
	// stale only some time after it.
	Started time.Time

	// fills serializes the engine's fills with the reconciliation, which
	// must not see a fill half booked.
	fills sync.Mutex
	last  lastPrices
}

// Metrics of contract trading.
type Metrics struct {
	Fills         prometheus.Counter
	Parked        prometheus.Counter
	Reconciled    *prometheus.GaugeVec
	LastReconcile prometheus.Gauge
	// Liquidations counts the steps: warning, takeover, order, adl.
	Liquidations *prometheus.CounterVec
}

// NewMetrics registers the metrics with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Fills: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "derivatives_fills_total", Help: "Sides of contract trades applied to positions and settled.",
		}),
		Parked: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "derivatives_settlements_parked_total", Help: "Settlements the ledger refused, parked for retries.",
		}),
		Reconciled: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "derivatives_reconcile_mismatches", Help: "Mismatches found by the last reconciliation, by check.",
		}, []string{"check"}),
		LastReconcile: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "derivatives_reconcile_last_success_timestamp_seconds", Help: "When the last reconciliation completed.",
		}),
		Liquidations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "derivatives_liquidation_steps_total", Help: "Liquidation steps: warning, takeover, order, adl.",
		}, []string{"step"}),
	}
	for _, step := range []string{"warning", "takeover", "order", "adl"} {
		m.Liquidations.WithLabelValues(step)
	}
	reg.MustRegister(m.Fills, m.Parked, m.Reconciled, m.LastReconcile, m.Liquidations)
	return m
}

// settings returns the user's settings on the contract, the defaults
// until the first change.
func settings(ctx context.Context, r ports.Repos, userID string, c domain.Contract) (domain.Settings, error) {
	s, err := r.Settings().Get(ctx, userID, c.Symbol)
	if err != nil {
		return domain.Settings{}, err
	}
	if s != nil {
		return *s, nil
	}
	return domain.DefaultSettings(userID, c), nil
}

// byside indexes positions by side.
func byside(list []domain.Position) map[domain.PositionSide]domain.Position {
	out := make(map[domain.PositionSide]domain.Position, len(list))
	for _, p := range list {
		out[p.Side] = p
	}
	return out
}

// mark returns the contract's fresh mark price.
func (s *Service) mark(symbol string) (decimal.Decimal, error) {
	m, fresh := s.Marks.Mark(symbol)
	if !fresh {
		return decimal.Zero, domain.ErrMarkUnavailable.WithDetail("symbol", symbol)
	}
	return m.Price, nil
}

// crossUnrealized sums the unrealized result of the user's cross
// positions at the mark prices; a position without a fresh mark fails it.
func (s *Service) crossUnrealized(ctx context.Context, r ports.Repos, userID string) (decimal.Decimal, error) {
	list, err := r.Positions().OfUser(ctx, userID, "")
	if err != nil {
		return decimal.Zero, err
	}
	sum := decimal.Zero
	for _, p := range list {
		if p.Flat() || p.MarginMode != domain.Cross {
			continue
		}
		m, err := s.mark(p.Symbol)
		if err != nil {
			return decimal.Zero, err
		}
		sum = sum.Add(p.UnrealizedPnL(m))
	}
	return sum, nil
}

// positionProto renders a position for events.
func positionProto(p domain.Position) *derivativesv1.Position {
	out := &derivativesv1.Position{
		PositionId: p.ID, UserId: p.UserID, Symbol: p.Symbol, PositionSide: string(p.Side), Quantity: p.Qty.String(),
		EntryCost: p.EntryCost.String(), Margin: p.Margin.String(), MarginMode: string(p.MarginMode), Leverage: p.Leverage,
		RealizedPnl: p.RealizedPnL.String(), Funding: p.Funding.String(), Version: p.Version,
	}
	if !p.Flat() {
		out.EntryPrice = p.EntryPrice().String()
	}
	return out
}

// emitPosition queues the event of a position a fill changed.
func emitPosition(ctx context.Context, r ports.Repos, c domain.PositionChange, tradeID string) error {
	var msg proto.Message
	pos := positionProto(c.Position)
	switch c.Reason {
	case domain.ChangeOpen:
		msg = &derivativesv1.PositionOpened{Position: pos, TradeId: tradeID}
	case domain.ChangeClose:
		msg = &derivativesv1.PositionClosed{Position: pos, TradeId: tradeID}
	default:
		msg = &derivativesv1.PositionChanged{Position: pos, TradeId: tradeID, Reason: c.Reason}
	}
	return r.Emit(ctx, event.TopicDerivPosition, msg, "user", c.Position.UserID)
}
