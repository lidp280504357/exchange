// Package application runs the platform market maker (requirements
// §11.10): every Interval it keeps each configured pair quoted around the
// reference price and each configured perpetual contract around its mark
// price, and pulls every quote of a symbol when the flag is off, the
// symbol is not TRADING, its price is stale or (contracts) it takes no
// opening orders.
package application

import (
	"context"
	"errors"
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

// Spot is where pairs are quoted: the spot order API, the reference
// prices, the pairs and the spot balances.
type Spot struct {
	Orders   ports.Orders
	Balances ports.Balances
	Refs     ports.References
	Pairs    ports.Pairs
}

// Contracts is where perpetual contracts are quoted: the contract order
// API, the mark prices, the contracts and the positions.
type Contracts struct {
	Orders    ports.Orders
	Positions ports.Positions
	Marks     ports.References
	Specs     ports.Pairs
}

// Maker quotes the configured pairs and contracts.
type Maker struct {
	pairs     []domain.Params
	contracts []domain.Params
	spot      Spot
	perp      Contracts
	flags     ports.Flags
	log       *slog.Logger

	state map[string]*quoting

	placed    *prometheus.CounterVec
	canceled  *prometheus.CounterVec
	errors    *prometheus.CounterVec
	active    *prometheus.GaugeVec
	inventory *prometheus.GaugeVec
	position  *prometheus.GaugeVec
}

type quoting struct {
	ref    decimal.Decimal
	plan   []domain.Quote
	pulled string // why the quotes are pulled; empty while quoting
}

// New returns a maker for the pairs on spot and the contracts on perp,
// and registers its metrics with reg.
func New(pairs []domain.Params, spot Spot, contracts []domain.Params, perp Contracts, fl ports.Flags, log *slog.Logger,
	reg prometheus.Registerer,
) *Maker {
	m := &Maker{
		pairs: pairs, contracts: contracts, spot: spot, perp: perp, flags: fl, log: log,
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
			Name: "mm_inventory", Help: "The market maker's spot holdings (available plus frozen), by asset.",
		}, []string{"asset"}),
		position: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mm_position", Help: "The market maker's net position on a perpetual contract (long positive).",
		}, []string{"symbol"}),
	}
	reg.MustRegister(m.placed, m.canceled, m.errors, m.active, m.inventory, m.position)
	for _, p := range append(append([]domain.Params{}, pairs...), contracts...) {
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
			for _, p := range m.pairs {
				m.pull(pullCtx, m.spot.Orders, p.Symbol, "shutting down")
			}
			for _, p := range m.contracts {
				m.pull(pullCtx, m.perp.Orders, p.Symbol, "shutting down")
			}
			cancel()
			return nil
		case <-tick.C:
		}
		for _, p := range m.pairs {
			m.report(ctx, p.Symbol, m.Step(ctx, p))
		}
		for _, p := range m.contracts {
			m.report(ctx, p.Symbol, m.StepContract(ctx, p))
		}
	}
}

func (m *Maker) report(ctx context.Context, symbol string, err error) {
	if err != nil && ctx.Err() == nil {
		m.errors.WithLabelValues(symbol).Inc()
		m.log.WarnContext(ctx, "market maker step failed", "symbol", symbol, "error", err)
	}
}

// pull cancels the symbol's quotes, once per reason change.
func (m *Maker) pull(ctx context.Context, orders ports.Orders, symbol, reason string) {
	st := m.state[symbol]
	if st.pulled == reason {
		return
	}
	if err := orders.CancelAll(ctx, symbol); err != nil {
		m.log.WarnContext(ctx, "market maker could not pull its quotes", "symbol", symbol, "error", err)
		return
	}
	if st.pulled == "" {
		m.log.InfoContext(ctx, "market maker pulled its quotes", "symbol", symbol, "reason", reason)
	}
	st.pulled, st.ref, st.plan = reason, decimal.Zero, nil
	m.active.WithLabelValues(symbol).Set(0)
}

// venue is one kind of market: its order API, its prices and its
// instruments.
type venue struct {
	orders ports.Orders
	refs   ports.References
	specs  ports.Pairs
	kind   string // "pair" or "contract", in pull reasons
}

// planner returns the quotes to keep at ref, and a filter that takes the
// quotes to place one by one and reports those the funds cover now.
type planner func(ctx context.Context, info ports.PairInfo, ref decimal.Decimal) ([]domain.Quote, func(domain.Quote) bool, error)

// Step brings one pair's quotes in line with the reference.
func (m *Maker) Step(ctx context.Context, p domain.Params) error {
	v := venue{orders: m.spot.Orders, refs: m.spot.Refs, specs: m.spot.Pairs, kind: "pair"}
	return m.step(ctx, p, v, func(ctx context.Context, pair ports.PairInfo, ref decimal.Decimal) ([]domain.Quote, func(domain.Quote) bool, error) {
		bal, err := m.spot.Balances.Spot(ctx)
		if err != nil {
			return nil, nil, err
		}
		base, quote := bal[pair.Base], bal[pair.Quote]
		m.inventory.WithLabelValues(pair.Base).Set(base.Available.Add(base.Frozen).InexactFloat64())
		m.inventory.WithLabelValues(pair.Quote).Set(quote.Available.Add(quote.Frozen).InexactFloat64())
		plan := domain.Plan(p, domain.Pair{TickSize: pair.TickSize, LotSize: pair.LotSize}, ref,
			base.Available.Add(base.Frozen), quote.Available.Add(quote.Frozen))
		// Only what is available now; funds of the orders just canceled
		// come back shortly, and the next step places the rest.
		spendQuote, spendBase := quote.Available, base.Available
		return plan, func(q domain.Quote) bool {
			if q.Side == domain.Buy {
				cost := q.Price.Mul(q.Quantity)
				if cost.GreaterThan(spendQuote) {
					return false
				}
				spendQuote = spendQuote.Sub(cost)
				return true
			}
			if q.Quantity.GreaterThan(spendBase) {
				return false
			}
			spendBase = spendBase.Sub(q.Quantity)
			return true
		}, nil
	})
}

// StepContract brings one contract's quotes in line with its mark price
// (§11.10: contracts are quoted around the mark price).
func (m *Maker) StepContract(ctx context.Context, p domain.Params) error {
	v := venue{orders: m.perp.Orders, refs: m.perp.Marks, specs: m.perp.Specs, kind: "contract"}
	return m.step(ctx, p, v, func(ctx context.Context, c ports.PairInfo, mark decimal.Decimal) ([]domain.Quote, func(domain.Quote) bool, error) {
		pos, err := m.perp.Positions.Net(ctx, p.Symbol)
		if err != nil {
			return nil, nil, err
		}
		m.position.WithLabelValues(p.Symbol).Set(pos.InexactFloat64())
		// The contract service refuses what the margin does not cover.
		return domain.PlanContract(p, domain.Pair{TickSize: c.TickSize, LotSize: c.LotSize}, mark, pos),
			func(domain.Quote) bool { return true }, nil
	})
}

func (m *Maker) step(ctx context.Context, p domain.Params, v venue, plan planner) error {
	st := m.state[p.Symbol]
	if !m.flags.Enabled(flags.KeyMarketMaker, flags.Subject{Symbol: p.Symbol}) {
		m.pull(ctx, v.orders, p.Symbol, "flag off")
		return nil
	}
	info, err := v.specs.Pair(ctx, p.Symbol)
	if err != nil {
		return err
	}
	if info.Status != "TRADING" {
		m.pull(ctx, v.orders, p.Symbol, v.kind+" "+info.Status)
		return nil
	}
	ref, fresh, err := v.refs.Price(ctx, p.Symbol)
	if err != nil || !fresh {
		// §11.10: no reference, no quotes.
		m.pull(ctx, v.orders, p.Symbol, "no fresh reference")
		return nil
	}
	open, err := v.orders.Open(ctx, p.Symbol)
	if err != nil {
		return err
	}
	if st.pulled == "" && !domain.Moved(p, st.ref, ref) && covered(open, st.plan) {
		return nil
	}
	quotes, afford, err := plan(ctx, info, ref)
	if err != nil {
		return err
	}
	want := make(map[string]bool, len(quotes))
	for _, q := range quotes {
		want[q.Key()] = true
	}
	kept := map[string]bool{}
	for _, o := range open {
		switch {
		case o.CancelRequested:
		case want[o.Key()] && !kept[o.Key()]:
			kept[o.Key()] = true
		default:
			if err := v.orders.Cancel(ctx, o.ID); err != nil {
				return err
			}
			m.canceled.WithLabelValues(p.Symbol).Inc()
		}
	}
	for _, q := range quotes {
		if kept[q.Key()] || !afford(q) {
			continue
		}
		if err := v.orders.Place(ctx, p.Symbol, q); err != nil {
			if errors.Is(err, ports.ErrPaused) {
				m.pull(ctx, v.orders, p.Symbol, "no opening orders")
				return nil
			}
			return err
		}
		m.placed.WithLabelValues(p.Symbol, q.Side).Inc()
	}
	if st.pulled != "" {
		m.log.InfoContext(ctx, "market maker quoting", "symbol", p.Symbol, "reference", ref.String(), "quotes", len(quotes))
	}
	st.pulled, st.ref, st.plan = "", ref, quotes
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
