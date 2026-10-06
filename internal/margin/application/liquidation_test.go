package application_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

// trading fills a liquidation's market orders at once against HOUSE at
// the price, on the fake ledger.
type trading struct {
	mu       sync.Mutex
	ledger   *ledger
	prices   *prices
	canceled int
	placed   map[string]string
}

func (t *trading) CancelAccount(context.Context, string, domain.Account) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.canceled++
	return nil
}

func (t *trading) PlaceLiquidation(_ context.Context, user string, a domain.Account, o ports.LiquidationOrder) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := o.LiquidationID + o.Symbol + o.Side
	if id, ok := t.placed[key]; ok {
		return id, nil
	}
	base, quote, _ := strings.Cut(o.Symbol, "-")
	all := t.prices.Prices()
	pb, _ := all.Of(base)
	pq, _ := all.Of(quote)
	price := pb.Value.DivRound(pq.Value, 18)
	t.ledger.mu.Lock()
	acct := t.ledger.margin[user][a]
	get := func(asset string) *balances {
		if acct[asset] == nil {
			acct[asset] = &balances{}
		}
		return acct[asset]
	}
	if o.Side == "SELL" {
		get(base).free = get(base).free.Sub(o.Quantity)
		get(quote).free = get(quote).free.Add(o.Quantity.Mul(price).RoundFloor(6))
	} else {
		get(quote).free = get(quote).free.Sub(o.QuoteAmount)
		get(base).free = get(base).free.Add(o.QuoteAmount.DivRound(price, 18).RoundFloor(8))
	}
	t.ledger.mu.Unlock()
	id := uuid.Must(uuid.NewV7()).String()
	if t.placed == nil {
		t.placed = map[string]string{}
	}
	t.placed[key] = id
	return id, nil
}

// TestLiquidation checks the liquidation (design §4.5): an account
// holding BTC bought with borrowed USDT, at its liquidation level twice
// with margin.liquidation on, is liquidated: its BTC sold to HOUSE, its
// fee to the insurance fund (2% of what was sold, first), the debt
// repaid, the rest left to it; an administrator's liquidation of an
// account whose BTC does not cover its debt has the insurance fund pay
// what is left of it.
func TestLiquidation(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	cross := domain.Cross()
	r.svc.Trading = &trading{ledger: r.ledger, prices: r.prices}
	m := &application.Monitor{Svc: r.svc, Liquidate: r.svc.AutoLiquidate}
	// Each user: 0.1 BTC in, 2000 USDT borrowed and spent on 0.0667 BTC.
	open := func() string {
		u := uuid.Must(uuid.NewV7()).String()
		r.ledger.fund(u, "BTC", d("0.1"))
		if _, err := r.svc.Transfer(ctx, application.TransferInput{
			UserID: u, IdemKey: "t", Direction: domain.DirectionIn, Account: cross, Asset: "BTC", Amount: d("0.1"),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: u, IdemKey: "b", Account: cross, Asset: "USDT", Amount: d("2000")}); err != nil {
			t.Fatal(err)
		}
		r.ledger.mu.Lock()
		acct := r.ledger.margin[u][cross]
		acct["USDT"].free = decimal.Zero
		acct["BTC"].free = d("0.1667")
		r.ledger.mu.Unlock()
		return u
	}
	auto, short := open(), open()
	advance := func(passes int) {
		t.Helper()
		for range passes {
			r.at(r.now().Add(3 * time.Second))
			if _, err := m.Pass(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	// BTC at 13,000: 0.1667 x 13000 x 0.95 / 2000.02 = 1.029, under 1.10.
	r.prices.setBTC("13000", true)
	advance(2)
	if l, _, err := r.svc.Liquidations(ctx, auto, nil, "", 10); err != nil || len(l) != 0 {
		t.Fatalf("liquidated with margin.liquidation off: %+v %v", l, err)
	}
	r.features.set(flags.KeyMarginLiquidation, true)
	r.svc.Features = r.features
	advance(8)
	list, _, err := r.svc.Liquidations(ctx, auto, nil, "", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("liquidations %+v %v", list, err)
	}
	l := list[0]
	// Sold 0.1667 BTC for 2167.1, repaid 2000.02, a fee of 2% of 2167.1.
	if l.Status != ports.LiquidationCompleted || l.Trigger != ports.TriggerAuto || !l.Traded.Equal(d("2167.1")) ||
		!l.Fee.Equal(d("43.342")) || !l.InsuranceCovered.IsZero() || len(l.Repaid) != 1 || !l.Repaid[0].Amount.Equal(d("2000.02")) {
		t.Fatalf("the liquidation %+v", l)
	}
	if b := r.ledger.owed(auto, cross, "USDT"); !b.free.Equal(d("123.738")) || !b.borrowed.IsZero() || !b.interest.IsZero() {
		t.Fatalf("what is left %+v", b)
	}
	if st, _, err := r.store.Read().Accounts().Get(ctx, auto, cross); err != nil || st.Status != domain.StatusNormal {
		t.Fatalf("the account after %+v %v", st, err)
	}
	if loan, err := r.store.Read().Loans().Get(ctx, auto, cross, "USDT"); err != nil || !loan.Principal.IsZero() || !loan.Interest.IsZero() {
		t.Fatalf("the loan after %+v %v", loan, err)
	}

	// The other account was liquidated too at 13,000; the next one: an
	// administrator's, at 9,000, where its BTC does not cover its debt.
	other := open()
	r.prices.setBTC("9000", true)
	approval := uuid.Must(uuid.NewV7()).String()
	started, err := r.svc.StartLiquidation(ctx, other, cross, ports.TriggerManual, approval, "ops@example.com")
	if err != nil || started.Status != ports.LiquidationStarted {
		t.Fatalf("manual %+v %v", started, err)
	}
	if again, err := r.svc.StartLiquidation(ctx, other, cross, ports.TriggerManual, approval, "ops@example.com"); err != nil || again.ID != started.ID {
		t.Fatalf("the same approval again %+v %v", again, err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: other, IdemKey: "b-2", Account: cross, Asset: "USDT", Amount: d("1")}); code(err) != "MARGIN_FROZEN" {
		t.Fatalf("borrowing while liquidated: %v", err)
	}
	advance(8)
	got, ok, err := r.store.Read().Liquidations().Get(ctx, started.ID)
	// Sold for 1500.3, the fee (30.006) first, then 1470.294 repaid; the
	// fund paid the 529.726 left.
	if err != nil || !ok || got.Status != ports.LiquidationCompleted || !got.Traded.Equal(d("1500.3")) || !got.Fee.Equal(d("30.006")) ||
		!got.InsuranceCovered.Equal(d("529.726")) {
		t.Fatalf("the shortfall %+v %v", got, err)
	}
	if b := r.ledger.owed(other, cross, "USDT"); !b.free.IsZero() || !b.borrowed.IsZero() || !b.interest.IsZero() {
		t.Fatalf("after the fund %+v", b)
	}
	if _, err := r.svc.StartLiquidation(ctx, other, cross, ports.TriggerManual, uuid.Must(uuid.NewV7()).String(), "ops@example.com"); code(err) != "MARGIN_NOTHING_OWED" {
		t.Fatalf("nothing owed: %v", err)
	}
	if list, _, err := r.svc.Liquidations(ctx, short, nil, "", 10); err != nil || len(list) != 1 {
		t.Fatalf("the second account at 13,000 %+v %v", list, err)
	}
}
