package application_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

// TestReconcile checks margin-service's reconciliation: invariant 7 (the
// ledger's debts against the loans) and the pools (lent against the loans
// and the borrows in flight, and within the cap). Clean after a borrow; a
// loan changed within the last minute is left for the next run; once it
// is older, a debt that differs from its loan and a debt without a loan
// are found, as are a pool whose lent amount the loans do not account for
// and one above its cap, each holding on the second look; every run sets
// margin_reconcile_mismatches by check.
func TestReconcile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	reg := prometheus.NewRegistry()
	r.svc.Metrics, r.svc.Recheck = application.NewMetrics(reg), 10*time.Millisecond
	cross := domain.Cross()
	u := uuid.Must(uuid.NewV7()).String()
	r.ledger.fund(u, "BTC", d("1"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: u, IdemKey: "t", Direction: domain.DirectionIn, Account: cross, Asset: "BTC", Amount: d("1"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: u, IdemKey: "b", Account: cross, Asset: "USDT", Amount: d("1000")}); err != nil {
		t.Fatal(err)
	}
	// run reconciles and returns the mismatches by check, checking the
	// metric and the recorded run of each against them.
	runs := 0
	run := func() map[string][]application.Mismatch {
		t.Helper()
		results, err := r.svc.Reconcile(ctx)
		if err != nil || len(results) != 2 {
			t.Fatalf("reconcile %+v %v", results, err)
		}
		out := map[string][]application.Mismatch{}
		for _, c := range results {
			out[c.Check] = c.Mismatches
		}
		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, f := range families {
			if f.GetName() != "margin_reconcile_mismatches" {
				continue
			}
			for _, m := range f.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "check" {
						seen++
						if got, want := m.GetGauge().GetValue(), float64(len(out[l.GetValue()])); got != want {
							t.Errorf("margin_reconcile_mismatches{check=%s} = %v, want %v", l.GetValue(), got, want)
						}
					}
				}
			}
		}
		if seen != 2 {
			t.Errorf("margin_reconcile_mismatches for %d checks", seen)
		}
		// Each run is recorded, one row a check, with what it found.
		runs++
		// The run's two rows are the last ones (runs share started_at
		// while the test's clock stands still).
		rows, err := r.db.Query(ctx, `SELECT check_name, mismatches, jsonb_array_length(details)
			FROM (SELECT * FROM reconciliation_runs ORDER BY id DESC LIMIT 2) last`)
		if err != nil {
			t.Fatal(err)
		}
		recorded := 0
		for rows.Next() {
			var check string
			var mismatches, details int
			if err := rows.Scan(&check, &mismatches, &details); err != nil {
				t.Fatal(err)
			}
			if want := len(out[check]); mismatches != want || details != want {
				t.Errorf("run %d, %s recorded %d mismatches (%d details), found %d", runs, check, mismatches, details, want)
			}
			recorded++
		}
		if err := rows.Err(); err != nil || recorded != 2 {
			t.Fatalf("run %d recorded %d checks %v", runs, recorded, err)
		}
		var total int
		if err := r.db.QueryRow(ctx, `SELECT count(*) FROM reconciliation_runs`).Scan(&total); err != nil || total != 2*runs {
			t.Fatalf("%d rows after %d runs %v", total, runs, err)
		}
		return out
	}
	if got := run(); len(got[application.CheckLoansMatchLedger]) != 0 || len(got[application.CheckPoolsMatchLoans]) != 0 {
		t.Fatalf("after a borrow %+v", got)
	}

	// The ledger owes a USDT more than the loan says, and another user
	// owes ETH without a loan.
	other := uuid.Must(uuid.NewV7()).String()
	r.ledger.mu.Lock()
	r.ledger.margin[u][cross]["USDT"].borrowed = r.ledger.margin[u][cross]["USDT"].borrowed.Add(d("1"))
	r.ledger.margin[other] = map[domain.Account]map[string]*balances{cross: {"ETH": {borrowed: d("0.5")}}}
	r.ledger.mu.Unlock()
	if got := run(); len(got[application.CheckLoansMatchLedger]) != 1 {
		t.Fatalf("the loan changed within the minute %+v", got) // only the debt without a loan
	}
	r.at(r.now().Add(2 * time.Minute))
	got := run()[application.CheckLoansMatchLedger]
	if len(got) != 2 {
		t.Fatalf("two debts off %+v", got)
	}
	for _, m := range got {
		switch {
		case strings.HasPrefix(m.Key, u+"/"):
			if m.Key != u+"/MARGIN_CROSS/USDT" || !strings.HasPrefix(m.Detail, "loan 1000 + ") || !strings.Contains(m.Detail, "ledger 1001 + ") {
				t.Errorf("the USDT debt %+v", m)
			}
		case m.Key != other+"/MARGIN_CROSS/ETH" || m.Detail != "no loan, ledger 0.5 + 0":
			t.Errorf("the ETH debt %+v", m)
		}
	}

	// The USDT pool says it lent a USDT the loans do not account for.
	if err := r.store.Tx(ctx, func(repos ports.Repos) error { return repos.Pools().AddLent(ctx, "USDT", d("1")) }); err != nil {
		t.Fatal(err)
	}
	pools := run()[application.CheckPoolsMatchLoans]
	if len(pools) != 1 || pools[0].Key != "USDT" || pools[0].Detail != "pool lent 1001, loans and borrows in flight 1000" {
		t.Fatalf("the pool off its loans %+v", pools)
	}
	// An administrator lowers USDT's pool under what it has lent.
	err := r.store.Tx(ctx, func(repos ports.Repos) error {
		if err := repos.Pools().AddLent(ctx, "USDT", d("-1")); err != nil {
			return err
		}
		usdt, ok, err := repos.Terms().Asset(ctx, "USDT")
		if err != nil || !ok {
			return fmt.Errorf("USDT terms %v: %w", ok, err)
		}
		usdt.PoolCap, usdt.UserCap = d("500"), d("500")
		return repos.Terms().SaveAsset(ctx, usdt, "ops@example.com")
	})
	if err != nil {
		t.Fatal(err)
	}
	pools = run()[application.CheckPoolsMatchLoans]
	if len(pools) != 1 || pools[0].Key != "USDT" || pools[0].Detail != "pool lent 1000 above its cap 500" {
		t.Fatalf("the pool above its cap %+v", pools)
	}
}
