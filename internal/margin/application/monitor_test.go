package application_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

// pushes records what the monitor publishes.
type pushes struct {
	mu   sync.Mutex
	sent []*marginv1.MarginAccountUpdated
}

func (p *pushes) Push(_ context.Context, accounts []*marginv1.MarginAccountUpdated) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, accounts...)
	return nil
}

// take returns what was published since the last take.
func (p *pushes) take() []*marginv1.MarginAccountUpdated {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.sent
	p.sent = nil
	return out
}

// TestMonitor checks the margin level monitor: an account owing BTC is
// warned as BTC rises past its warning level, due for liquidation at its
// liquidation level twice in a row, NORMAL again when BTC is back; its
// changes are published, and a touched account without debts too.
func TestMonitor(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	cross := domain.Cross()
	user, other := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	var due []string
	sent := &pushes{}
	m := &application.Monitor{
		Svc: r.svc, Pushes: sent, Liquidate: func(_ context.Context, st ports.Account, _ domain.Valuation) error {
			due = append(due, st.UserID)
			return nil
		},
	}
	r.svc.Touched = m.Touch
	for _, u := range []string{user, other} {
		r.ledger.fund(u, "USDT", d("1000"))
		if _, err := r.svc.Transfer(ctx, application.TransferInput{
			UserID: u, IdemKey: "t", Direction: domain.DirectionIn, Account: cross, Asset: "USDT", Amount: d("1000"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 1000 USDT own, 0.06 BTC borrowed and held: (1000 + 0.057 p) / 0.06 p.
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b", Account: cross, Asset: "BTC", Amount: d("0.06")}); err != nil {
		t.Fatal(err)
	}
	state := func() ports.Account {
		st, _, err := r.store.Read().Accounts().Get(ctx, user, cross)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	status := func() domain.Status { return state().Status }
	pass := func() []*marginv1.MarginAccountUpdated {
		t.Helper()
		if _, err := m.Pass(ctx); err != nil {
			t.Fatal(err)
		}
		return sent.take()
	}
	of := func(list []*marginv1.MarginAccountUpdated, u string) *marginv1.MarginAccountUpdated {
		for _, a := range list {
			if a.GetUserId() == u {
				return a
			}
		}
		return nil
	}
	got := pass()
	if a := of(got, user); a == nil || a.GetStatus() != "NORMAL" || a.GetMarginLevel() == "" || len(a.GetBalances()) != 2 {
		t.Fatalf("first pass %v", got)
	}
	if a := of(got, other); a == nil || a.GetMarginLevel() != "" {
		t.Fatalf("the touched account without debts %v", got)
	}
	if got = pass(); of(got, user) != nil || of(got, other) != nil {
		t.Fatalf("nothing changed, yet %v", got)
	}
	// BTC at 50,000: 3850 / 3000 = 1.283, under 1.3.
	r.prices.setBTC("50000", true)
	got = pass()
	warned := state()
	if warned.Status != domain.StatusWarned || warned.WarnedAt.IsZero() || of(got, user).GetStatus() != "WARNED" {
		t.Fatalf("warned: %+v %v", warned, got)
	}
	r.at(r.now().Add(time.Minute))
	if pass(); !state().WarnedAt.Equal(warned.WarnedAt) {
		t.Fatal("warned twice")
	}
	// BTC at 120,000: 7840 / 7200 = 1.089, at the liquidation level twice.
	r.prices.setBTC("120000", true)
	pass()
	if len(due) != 0 {
		t.Fatalf("due after one pass: %v", due)
	}
	pass()
	if len(due) != 1 || due[0] != user {
		t.Fatalf("due %v", due)
	}
	// Back at 30,000: NORMAL again.
	r.prices.setBTC("30000", true)
	if got = pass(); status() != domain.StatusNormal || of(got, user).GetStatus() != "NORMAL" {
		t.Fatalf("back: %s %v", status(), got)
	}
	// A repayment touches the account: published at once, though its
	// level hardly moves.
	if _, err := r.svc.Repay(ctx, application.RepayInput{UserID: user, IdemKey: "r", Account: cross, Asset: "BTC", Amount: d("0.001")}); err != nil {
		t.Fatal(err)
	}
	if got = pass(); of(got, user) == nil {
		t.Fatalf("after repaying %v", got)
	}
}
