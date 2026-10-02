package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// A simulated market's heartbeat (ASTRA design §9): market-sim reports its
// target every few seconds whether its bots trade or not. A pair that has
// heard nothing for SimHaltAfter is halted while sim.halt_on_loss is on,
// with its perpetuals; once the reports have been back for SimResumeAfter
// (or the flag goes off) the guard lets them trade again.
const (
	SimHaltAfter   = time.Minute
	SimResumeAfter = 30 * time.Second
	// simFresh is how recent a report counts as the heartbeat being there.
	simFresh        = 15 * time.Second
	simHaltReason   = "simulated market silent for a minute (sim.halt_on_loss)"
	simResumeReason = "simulated market back"
)

// SimGuard halts the pairs whose simulated market went silent, and the
// contracts on them, and resumes what it halted. Halts are recorded before
// they happen (market.sim_halts), so a restart resumes them too. While a
// market stays silent every step halts what of it still trades, so a halt
// that failed halfway (instrument-service unavailable) is finished; to let
// a silent market trade, an operator turns sim.halt_on_loss off. On
// resuming, a pair or contract an operator moved elsewhere is left as it
// is. Only pairs heard since the service started are watched.
type SimGuard struct {
	platform    *PlatformReference
	instruments ports.Instruments
	store       ports.Store
	flags       Flags
	log         *slog.Logger
	now         func() time.Time
	check       time.Duration

	backSince map[string]time.Time // when a halted pair's reports came back

	age    *prometheus.GaugeVec
	halted prometheus.Gauge
}

// NewSimGuard watches the reports platform keeps and registers
// market_sim_heartbeat_age_seconds and market_sim_halted_pairs with reg.
func NewSimGuard(platform *PlatformReference, instruments ports.Instruments, store ports.Store, fl Flags, log *slog.Logger,
	reg prometheus.Registerer,
) *SimGuard {
	g := &SimGuard{
		platform: platform, instruments: instruments, store: store, flags: fl, log: log, now: time.Now, check: 5 * time.Second,
		backSince: map[string]time.Time{},
		age: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_sim_heartbeat_age_seconds", Help: "Seconds since the simulated market last reported a pair (its heartbeat).",
		}, []string{"symbol"}),
		halted: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "market_sim_halted_pairs", Help: "Pairs halted because their simulated market went silent.",
		}),
	}
	reg.MustRegister(g.age, g.halted)
	return g
}

// Run checks the heartbeats until ctx ends (an app.Loop body).
func (g *SimGuard) Run(ctx context.Context) error {
	t := time.NewTicker(g.check)
	defer t.Stop()
	for {
		if err := g.Step(ctx); err != nil && ctx.Err() == nil {
			g.log.WarnContext(ctx, "simulated market guard step failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step looks at the heartbeats once, halting or resuming as needed.
func (g *SimGuard) Step(ctx context.Context) error {
	now := g.now()
	on := g.flags.Enabled(flags.KeySimHaltOnLoss, flags.Subject{})
	reports := g.platform.Reports()
	for symbol, at := range reports {
		g.age.WithLabelValues(symbol).Set(now.Sub(at).Seconds())
	}
	halts, err := g.store.Read().SimHalts().List(ctx)
	if err != nil {
		return err
	}
	halted := map[string]bool{}
	for _, h := range halts {
		halted[h.Symbol] = true
	}
	if on {
		for symbol, at := range reports {
			if now.Sub(at) < SimHaltAfter {
				continue
			}
			recorded, err := g.halt(ctx, symbol, now, at, halted[symbol])
			if err != nil {
				return err
			}
			if recorded && !halted[symbol] {
				halts, halted[symbol] = append(halts, ports.Halt{Symbol: symbol, HaltedAt: now}), true
			}
		}
	}
	g.halted.Set(float64(len(halts)))
	for _, h := range halts {
		at, heard := reports[h.Symbol]
		fresh := heard && at.After(h.HaltedAt) && now.Sub(at) < simFresh
		switch {
		case !fresh:
			delete(g.backSince, h.Symbol)
		case g.backSince[h.Symbol].IsZero():
			g.backSince[h.Symbol] = now
		}
		back := fresh && now.Sub(g.backSince[h.Symbol]) >= SimResumeAfter
		if back || !on {
			if err := g.resume(ctx, h.Symbol); err != nil {
				return err
			}
			delete(g.backSince, h.Symbol)
		}
	}
	return nil
}

// halt halts a silent pair and the contracts on it, what of them still
// trades, recording the halt first unless recorded already; it returns
// whether the halt is recorded (a pair that does not trade gets none).
func (g *SimGuard) halt(ctx context.Context, symbol string, now, heard time.Time, recorded bool) (bool, error) {
	pairs, err := g.instruments.Pairs(ctx)
	if err != nil {
		return recorded, err
	}
	trading := false
	for _, p := range pairs {
		trading = trading || (p.Symbol == symbol && p.Status == pairTrading)
	}
	if !recorded {
		if !trading {
			return false, nil // nothing to halt; heard again or moved by hand
		}
		if err := g.store.Read().SimHalts().Add(ctx, symbol, now); err != nil {
			return false, err
		}
	}
	halted := false
	if trading {
		if _, err := g.instruments.SetPairStatus(ctx, symbol, pairHalt, simHaltReason); err != nil &&
			!apperr.Is(err, "INSTRUMENT_STATUS_TRANSITION_INVALID") {
			return true, err
		}
		halted = true
	}
	contracts, err := g.instruments.Contracts(ctx)
	if err != nil {
		return true, err
	}
	for _, c := range contracts {
		if c.IndexSymbol != symbol || c.Status != pairTrading {
			continue
		}
		if _, err := g.instruments.SetContractStatus(ctx, c.Symbol, pairHalt, simHaltReason); err != nil &&
			!apperr.Is(err, "INSTRUMENT_STATUS_TRANSITION_INVALID") {
			return true, err
		}
		halted = true
	}
	if halted {
		g.log.WarnContext(ctx, "pair halted: its simulated market went silent", "symbol", symbol, "last_heard", heard)
	}
	return true, nil
}

// resume lets a pair the guard halted, and its halted contracts, trade
// again; what an operator moved elsewhere meanwhile is only forgotten.
func (g *SimGuard) resume(ctx context.Context, symbol string) error {
	pairs, err := g.instruments.Pairs(ctx)
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if p.Symbol != symbol || p.Status != pairHalt {
			continue
		}
		if _, err := g.instruments.SetPairStatus(ctx, symbol, pairTrading, simResumeReason); err != nil &&
			!apperr.Is(err, "INSTRUMENT_STATUS_TRANSITION_INVALID") {
			return err
		}
		contracts, err := g.instruments.Contracts(ctx)
		if err != nil {
			return err
		}
		for _, c := range contracts {
			if c.IndexSymbol == symbol && c.Status == pairHalt {
				if _, err := g.instruments.SetContractStatus(ctx, c.Symbol, pairTrading, simResumeReason); err != nil &&
					!apperr.Is(err, "INSTRUMENT_STATUS_TRANSITION_INVALID") {
					return err
				}
			}
		}
		g.log.InfoContext(ctx, "pair resumed: its simulated market is back", "symbol", symbol)
	}
	return g.store.Read().SimHalts().Remove(ctx, symbol)
}
