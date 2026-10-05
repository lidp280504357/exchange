package application

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/ports"
)

// Check names of margin-service's reconciliation.
const (
	// Invariant 7 (design §3.3, E0 contract §7.3): every margin account's
	// debt rows in the ledger equal the loans here, principal and
	// interest.
	CheckLoansMatchLedger = "LOANS_MATCH_LEDGER"
	// Each pool's lent amount equals the principal of its loans and the
	// borrows in flight, and stays within its cap.
	CheckPoolsMatchLoans = "POOLS_MATCH_LOANS"
)

// settleGrace is how long a loan changed just now is left out of the
// comparison: a write may sit between the two reads.
const settleGrace = time.Minute

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

// Reconcile runs the checks, records each in reconciliation_runs and
// sets the metrics.
func (s *Service) Reconcile(ctx context.Context) ([]CheckResult, error) {
	started := s.Now()
	read := s.Store.Read()
	debts, err := s.Ledger.Debts(ctx)
	if err != nil {
		return nil, err
	}
	loans, err := read.Loans().Open(ctx, "")
	if err != nil {
		return nil, err
	}
	cat, err := s.catalog(ctx, read)
	if err != nil {
		return nil, err
	}
	// Loans read after the debts: one changed meanwhile is recent.
	recent := func(l ports.Loan) bool { return l.UpdatedAt.After(started.Add(-settleGrace)) }

	ledger := map[ports.LoanKey]ports.Debt{}
	for _, d := range debts {
		ledger[ports.LoanKey{UserID: d.UserID, Account: d.Account, Asset: d.Asset}] = d
	}
	book := map[ports.LoanKey]ports.Loan{}
	for _, l := range loans {
		book[ports.LoanKey{UserID: l.UserID, Account: l.Account, Asset: l.Asset}] = l
	}
	loansCheck := CheckResult{Check: CheckLoansMatchLedger, Mismatches: []Mismatch{}}
	for k, l := range book {
		d := ledger[k]
		if recent(l) || (l.Principal.Equal(d.Principal) && l.Interest.Equal(d.Interest)) {
			continue
		}
		loansCheck.Mismatches = append(loansCheck.Mismatches, Mismatch{
			Key:    fmt.Sprintf("%s/%s/%s", k.UserID, k.Account, k.Asset),
			Detail: fmt.Sprintf("loan %s + %s, ledger %s + %s", l.Principal, l.Interest, d.Principal, d.Interest),
		})
	}
	for k, d := range ledger {
		if _, ok := book[k]; ok {
			continue
		}
		// The ledger owes what the book does not: a loan closed just now
		// shows as such in the book (updated recently, nothing owed).
		l, err := read.Loans().Get(ctx, k.UserID, k.Account, k.Asset)
		if err != nil {
			return nil, err
		}
		if recent(l) {
			continue
		}
		loansCheck.Mismatches = append(loansCheck.Mismatches, Mismatch{
			Key: fmt.Sprintf("%s/%s/%s", k.UserID, k.Account, k.Asset), Detail: fmt.Sprintf("no loan, ledger %s + %s", d.Principal, d.Interest),
		})
	}

	// A borrow may finish between the reads: a mismatch must hold on a
	// second look a moment later.
	poolsCheck := CheckResult{Check: CheckPoolsMatchLoans}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		if poolsCheck.Mismatches, err = s.poolMismatches(ctx, cat); err != nil {
			return nil, err
		}
		if len(poolsCheck.Mismatches) == 0 {
			break
		}
	}

	results := []CheckResult{loansCheck, poolsCheck}
	for _, r := range results {
		details, _ := json.Marshal(r.Mismatches)
		if err := read.Runs().Record(ctx, started, r.Check, len(r.Mismatches), details); err != nil {
			return nil, err
		}
		if s.Metrics != nil {
			s.Metrics.Reconciled.WithLabelValues(r.Check).Set(float64(len(r.Mismatches)))
		}
	}
	if s.Metrics != nil {
		s.Metrics.LastReconcile.SetToCurrentTime()
	}
	return results, nil
}

// poolMismatches compares each pool's lent amount with the principal of
// its loans and the borrows in flight, and with its cap.
func (s *Service) poolMismatches(ctx context.Context, cat Catalog) ([]Mismatch, error) {
	read := s.Store.Read()
	pending, err := read.Borrows().Pending(ctx, s.Now().Add(time.Hour), 10000)
	if err != nil {
		return nil, err
	}
	loans, err := read.Loans().Open(ctx, "")
	if err != nil {
		return nil, err
	}
	lent, err := read.Pools().Lent(ctx)
	if err != nil {
		return nil, err
	}
	owed := map[string]decimal.Decimal{}
	for _, l := range loans {
		owed[l.Asset] = owed[l.Asset].Add(l.Principal)
	}
	for _, b := range pending {
		owed[b.Asset] = owed[b.Asset].Add(b.Amount)
	}
	assets := map[string]bool{}
	for a := range lent {
		assets[a] = true
	}
	for a := range owed {
		assets[a] = true
	}
	out := []Mismatch{}
	for _, a := range slices.Sorted(maps.Keys(assets)) {
		if !lent[a].Equal(owed[a]) {
			out = append(out, Mismatch{Key: a, Detail: fmt.Sprintf("pool lent %s, loans and borrows in flight %s", lent[a], owed[a])})
		}
		if t, ok := cat.Assets[a]; ok && lent[a].GreaterThan(t.PoolCap) {
			out = append(out, Mismatch{Key: a, Detail: fmt.Sprintf("pool lent %s above its cap %s", lent[a], t.PoolCap)})
		}
	}
	return out, nil
}
