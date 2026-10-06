package application

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/derivatives/domain"
)

// Checks of the reconciliation (§11.4 invariant 6).
const (
	// Per contract the long quantity equals the short quantity.
	CheckPositionsBalanced = "POSITIONS_BALANCED"
	// Per settlement asset, PNL_CLEARING + Σ long cost − Σ short cost = 0
	// over its linear contracts, − Σ long cost + Σ short cost over its
	// inverse ones, whose costs move the other way (coin-M §2.2).
	CheckPnLClearing = "PNL_CLEARING_MATCHES_POSITIONS"
	// No settlement waits on the ledger (parked after a refusal).
	CheckPendingSettlements = "NO_PENDING_SETTLEMENTS"
)

// Mismatch is one finding of a check.
type Mismatch struct {
	Key    string `json:"key"`
	Detail string `json:"detail"`
}

// CheckResult is the outcome of one check.
type CheckResult struct {
	Check      string
	Mismatches []Mismatch
}

// Reconciler checks invariant 6 against the ledger, for every settlement
// asset of the listed contracts (USDT, and the coins of the coin-margined
// ones). It holds the service's fill lock while it reads, so no fill is
// half booked meanwhile.
type Reconciler struct {
	Svc *Service
	// Asset is checked even while no listed contract settles in it (USDT).
	Asset string
}

// Run checks and records each result in reconciliation_runs.
func (rc *Reconciler) Run(ctx context.Context) ([]CheckResult, error) {
	s := rc.Svc
	s.fills.Lock()
	defer s.fills.Unlock()
	started := s.Now()
	out := []CheckResult{
		{Check: CheckPositionsBalanced, Mismatches: []Mismatch{}},
		{Check: CheckPnLClearing, Mismatches: []Mismatch{}},
		{Check: CheckPendingSettlements, Mismatches: []Mismatch{}},
	}
	r := s.Store.Read()
	totals, err := r.Positions().Totals(ctx)
	if err != nil {
		return nil, err
	}
	listed, err := s.Instruments.Contracts(ctx)
	if err != nil {
		return nil, err
	}
	contracts := make(map[string]domain.Contract, len(listed))
	for _, c := range listed {
		contracts[c.Symbol] = c
	}
	symbols := make([]string, 0, len(totals))
	for symbol := range totals {
		symbols = append(symbols, symbol)
	}
	slices.Sort(symbols)
	nets := map[string]decimal.Decimal{} // by settlement asset
	if rc.Asset != "" {
		nets[rc.Asset] = decimal.Zero
	}
	for _, c := range listed {
		nets[c.Settle()] = decimal.Zero
	}
	for _, symbol := range symbols {
		t := totals[symbol]
		if !t.NetQty.IsZero() {
			out[0].Mismatches = append(out[0].Mismatches, Mismatch{Key: symbol, Detail: "long − short = " + t.NetQty.String()})
		}
		c, ok := contracts[symbol]
		switch {
		case !ok:
			// Not listed (delisted with positions, or a listing gap): its
			// costs cannot be put to an asset, which the check reports.
			out[1].Mismatches = append(out[1].Mismatches, Mismatch{
				Key: symbol, Detail: "positions on a contract instrument-service does not list; signed costs " + t.NetCost.String(),
			})
		case c.Inverse():
			nets[c.Settle()] = nets[c.Settle()].Sub(t.NetCost)
		default:
			nets[c.Settle()] = nets[c.Settle()].Add(t.NetCost)
		}
	}
	assets := make([]string, 0, len(nets))
	for asset := range nets {
		assets = append(assets, asset)
	}
	slices.Sort(assets)
	for _, asset := range assets {
		clearing, err := s.Ledger.PnLClearing(ctx, asset)
		if err != nil {
			return nil, err
		}
		if sum := clearing.Add(nets[asset]); !sum.IsZero() {
			out[1].Mismatches = append(out[1].Mismatches, Mismatch{
				Key: asset, Detail: fmt.Sprintf("PNL_CLEARING %s + the positions' signed costs %s = %s", clearing, nets[asset], sum),
			})
		}
	}
	pending, err := r.Pending().Due(ctx, 100)
	if err != nil {
		return nil, err
	}
	for _, p := range pending {
		out[2].Mismatches = append(out[2].Mismatches, Mismatch{Key: p.IdemKey, Detail: fmt.Sprintf("%d attempts: %s", p.Attempts, p.LastError)})
	}
	for _, res := range out {
		details, _ := json.Marshal(res.Mismatches)
		if err := r.Runs().Record(ctx, started, res.Check, len(res.Mismatches), details); err != nil {
			return nil, err
		}
		if s.Metrics != nil {
			s.Metrics.Reconciled.WithLabelValues(res.Check).Set(float64(len(res.Mismatches)))
		}
	}
	if s.Metrics != nil {
		s.Metrics.LastReconcile.SetToCurrentTime()
	}
	return out, nil
}
