package application_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

// TestReconcile checks margin-service's reconciliation: invariant 7 (the
// ledger's debts against the loans) and the pools (lent against the loans
// and within the cap). Clean after a borrow; a loan changed within the
// last minute is left for the next run; once it is older, a debt that
// differs from its loan, a debt without a loan and a pool above its cap
// are each found, as they hold on the second look.
func TestReconcile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
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

	// An administrator lowers USDT's pool under what it has lent.
	err := r.store.Tx(ctx, func(repos ports.Repos) error {
		usdt, ok, err := repos.Terms().Asset(ctx, "USDT")
		if err != nil || !ok {
			t.Fatalf("USDT terms %v %v", ok, err)
		}
		usdt.PoolCap, usdt.UserCap = d("500"), d("500")
		return repos.Terms().SaveAsset(ctx, usdt, "ops@example.com")
	})
	if err != nil {
		t.Fatal(err)
	}
	pools := run()[application.CheckPoolsMatchLoans]
	if len(pools) != 1 || pools[0].Key != "USDT" || !strings.HasSuffix(pools[0].Detail, "above its cap 500") {
		t.Fatalf("the pool above its cap %+v", pools)
	}
}
