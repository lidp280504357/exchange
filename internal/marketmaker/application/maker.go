// Package application runs the platform market maker (requirements
// §11.10): every PushInterval it keeps each configured pair quoted around
// the reference price, and pulls every quote when the flag is off, the pair
// is not TRADING or the reference is stale.
package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/marketmaker/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Interval is how often quotes are refreshed (§11.10: 500 ms).
const Interval = 500 * time.Millisecond

// Maker quotes the configured pairs.
type Maker struct {
	params   []domain.Params
	orders   ports.Orders
	balances ports.Balances
	refs     ports.References
	pairs    ports.Pairs
	flags    ports.Flags
	log      *slog.Logger

	state map[string]*quoting

	placed    *prometheus.CounterVec
	canceled  *prometheus.CounterVec
	errors    *prometheus.CounterVec
	active    *prometheus.GaugeVec
	inventory *prometheus.GaugeVec
}

type quoting struct {
	ref    decimal.Decimal
	plan   []domain.Quote
	pulled string // why the quotes are pulled; empty while quoting
}

// New returns a maker for params and registers its metrics with reg.
func New(params []domain.Params, orders ports.Orders, balances ports.Balances, refs ports.References, pairs ports.Pairs,
	fl ports.Flags, log *slog.Logger, reg prometheus.Registerer,
) *Maker {
	m := &Maker{
		params: params, orders: orders, balances: balances, refs: refs, pairs: pairs, flags: fl, log: log,
		state: map[string]*quoting{},
		placed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mm_orders_placed_total", Help: "Quotes placed by the market maker, by symbol and side.",
		}, []string{"symbol", "side"}),
		canceled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mm_orders_canceled_total", Help: "Quotes the market maker asked to cancel, by symbol.",
		}, []string{"symbol"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mm_errors_total", Help: "Market maker steps that failed, by symbol.",
		}, []string{"symbol"}),
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mm_quoting", Help: "1 while the market maker quotes the symbol, 0 while its quotes are pulled.",
		}, []string{"symbol"}),
		inventory: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mm_inventory", Help: "The market maker's holdings (available plus frozen), by asset.",
		}, []string{"asset"}),
	}
	reg.MustRegister(m.placed, m.canceled, m.errors, m.active, m.inventory)
	for _, p := range params {
		m.state[p.Symbol] = &quoting{pulled: "starting"}
		m.active.WithLabelValues(p.Symbol).Set(0)
	}
	return m
}

// Run quotes until ctx ends, then pulls every quote (an app.Loop body).
func (m *Maker) Run(ctx context.Context) error {
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// Nobody watches the quotes once the process is gone.
			pullCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			for _, p := range m.params {
				m.pull(pullCtx, p.Symbol, "shutting down")
			}
			cancel()
			return nil
		case <-tick.C:
		}
		for _, p := range m.params {
			if err := m.Step(ctx, p); err != nil && ctx.Err() == nil {
				m.errors.WithLabelValues(p.Symbol).Inc()
				m.log.WarnContext(ctx, "market maker step failed", "symbol", p.Symbol, "error", err)
			}
		}
	}
}

// pull cancels the pair's quotes, once per reason change.
func (m *Maker) pull(ctx context.Context, symbol, reason string) {
	st := m.state[symbol]
	if st.pulled == reason {
		return
	}
	if err := m.orders.CancelAll(ctx, symbol); err != nil {
		m.log.WarnContext(ctx, "market maker could not pull its quotes", "symbol", symbol, "error", err)
		return
	}
	if st.pulled == "" {
		m.log.InfoContext(ctx, "market maker pulled its quotes", "symbol", symbol, "reason", reason)
	}
	st.pulled, st.ref, st.plan = reason, decimal.Zero, nil
	m.active.WithLabelValues(symbol).Set(0)
}

// Step brings one pair's quotes in line with the reference.
func (m *Maker) Step(ctx context.Context, p domain.Params) error {
	st := m.state[p.Symbol]
	if !m.flags.Enabled(flags.KeyMarketMaker, flags.Subject{Symbol: p.Symbol}) {
		m.pull(ctx, p.Symbol, "flag off")
		return nil
	}
	pair, err := m.pairs.Pair(ctx, p.Symbol)
	if err != nil {
		return err
	}
	if pair.Status != "TRADING" {
		m.pull(ctx, p.Symbol, "pair "+pair.Status)
		return nil
	}
	ref, fresh, err := m.refs.Price(ctx, p.Symbol)
	if err != nil || !fresh {
		// §11.10: no reference, no quotes.
		m.pull(ctx, p.Symbol, "no fresh reference")
		return nil
	}
	open, err := m.orders.Open(ctx, p.Symbol)
	if err != nil {
		return err
	}
	if st.pulled == "" && !domain.Moved(p, st.ref, ref) && covered(open, st.plan) {
		return nil
	}
	bal, err := m.balances.Spot(ctx)
	if err != nil {
		return err
	}
	base, quote := bal[pair.Base], bal[pair.Quote]
	m.inventory.WithLabelValues(pair.Base).Set(base.Available.Add(base.Frozen).InexactFloat64())
	m.inventory.WithLabelValues(pair.Quote).Set(quote.Available.Add(quote.Frozen).InexactFloat64())
	plan := domain.Plan(p, domain.Pair{TickSize: pair.TickSize, LotSize: pair.LotSize}, ref,
		base.Available.Add(base.Frozen), quote.Available.Add(quote.Frozen))
	want := make(map[string]bool, len(plan))
	for _, q := range plan {
		want[q.Key()] = true
	}
	kept := map[string]bool{}
	for _, o := range open {
		switch {
		case o.CancelRequested:
		case want[o.Key()] && !kept[o.Key()]:
			kept[o.Key()] = true
		default:
			if err := m.orders.Cancel(ctx, o.ID); err != nil {
				return err
			}
			m.canceled.WithLabelValues(p.Symbol).Inc()
		}
	}
	// Place what is missing and fundable now; funds of the orders just
	// canceled come back shortly, and the next step places the rest.
	spendQuote, spendBase := quote.Available, base.Available
	for _, q := range plan {
		if kept[q.Key()] {
			continue
		}
		if q.Side == domain.Buy {
			cost := q.Price.Mul(q.Quantity)
			if cost.GreaterThan(spendQuote) {
				continue
			}
			spendQuote = spendQuote.Sub(cost)
		} else {
			if q.Quantity.GreaterThan(spendBase) {
				continue
			}
			spendBase = spendBase.Sub(q.Quantity)
		}
		if err := m.orders.Place(ctx, p.Symbol, q); err != nil {
			return err
		}
		m.placed.WithLabelValues(p.Symbol, q.Side).Inc()
	}
	if st.pulled != "" {
		m.log.InfoContext(ctx, "market maker quoting", "symbol", p.Symbol, "reference", ref.String(), "quotes", len(plan))
	}
	st.pulled, st.ref, st.plan = "", ref, plan
	m.active.WithLabelValues(p.Symbol).Set(1)
	return nil
}

// covered reports whether every planned quote is on the book.
func covered(open []ports.Order, plan []domain.Quote) bool {
	have := make(map[string]bool, len(open))
	for _, o := range open {
		if !o.CancelRequested {
			have[o.Key()] = true
		}
	}
	for _, q := range plan {
		if !have[q.Key()] {
			return false
		}
	}
	return true
}
