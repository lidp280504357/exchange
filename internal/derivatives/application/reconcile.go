package application

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"
)

// Checks of the reconciliation (§11.4 invariant 6).
const (
	// Per contract the long quantity equals the short quantity.
	CheckPositionsBalanced = "POSITIONS_BALANCED"
	// Per settlement asset, PNL_CLEARING + Σ long cost − Σ short cost = 0.
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

// Reconciler checks invariant 6 against the ledger. It holds the service's
// fill lock while it reads, so no fill is half booked meanwhile.
type Reconciler struct {
	Svc *Service
	// Asset is the settlement asset of the contracts.
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
	symbols := make([]string, 0, len(totals))
	for symbol := range totals {
		symbols = append(symbols, symbol)
	}
	slices.Sort(symbols)
	net := decimal.Zero
	for _, symbol := range symbols {
		t := totals[symbol]
		if !t.NetQty.IsZero() {
			out[0].Mismatches = append(out[0].Mismatches, Mismatch{Key: symbol, Detail: "long − short = " + t.NetQty.String()})
		}
		net = net.Add(t.NetCost)
	}
	clearing, err := s.Ledger.PnLClearing(ctx, rc.Asset)
	if err != nil {
		return nil, err
	}
	if sum := clearing.Add(net); !sum.IsZero() {
		out[1].Mismatches = append(out[1].Mismatches, Mismatch{
			Key: rc.Asset, Detail: fmt.Sprintf("PNL_CLEARING %s + long cost − short cost %s = %s", clearing, net, sum),
		})
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
