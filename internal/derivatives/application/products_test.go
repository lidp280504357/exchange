package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// productFlags are the product lines' flags, closed by key since a time;
// Refresh counts the rereads.
type productFlags struct {
	mu        sync.Mutex
	closed    map[string]bool
	at        map[string]time.Time
	refreshes int
	down      error
}

func (f *productFlags) Enabled(string, flags.Subject) bool { return false }

func (f *productFlags) Closed(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed[key]
}

func (f *productFlags) Get(key string) (flags.Flag, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	closed, ok := f.closed[key]
	return flags.Flag{Key: key, Enabled: !closed, UpdatedAt: f.at[key]}, ok
}

func (f *productFlags) Refresh(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	return f.down
}

func (f *productFlags) set(key string, closed bool) { f.setAt(key, closed, time.Now()) }

func (f *productFlags) setAt(key string, closed bool, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed[key], f.at[key] = closed, at
}

// A closed product line (design 2026-10-07, product switches, K1b) takes
// only what closes: an opening order and a new take-profit are
// PRODUCT_CLOSED (naming the line), a reduce-only close goes through.
// Closing it cancels the users' open orders and take-profits on its
// contracts, each audited, and leaves the other line's and the market
// makers' alone; the sweep cancels an opening order that came in as it
// closed and keeps the closes placed since. Reopened, it takes opening
// orders again.
func TestAClosedProductLineTakesOnlyCloses(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	fl := &productFlags{closed: map[string]bool{}, at: map[string]time.Time{}}
	bot := uuid.NewString()
	r.svc.Features, r.svc.Instruments, r.svc.FeeFree = fl, instruments{coin: true}, []string{bot}
	r.book.Set(coinPerp.Symbol, d("60000"), time.Now())
	alice, bob := uuid.NewString(), uuid.NewString()
	for _, u := range []string{alice, bob, bot} {
		r.fund(u, "10000")
	}
	r.ledger.available[bob+"|BTC"] = d("0.1")
	long := r.place(t, alice, domain.Buy, "60000", "0.2", false)
	short := r.place(t, bob, domain.Sell, "60000", "0.2", false)
	r.trade(t, long, short, "60000")
	bid := r.place(t, alice, domain.Buy, "59000", "0.1", false)
	botBid := r.place(t, bot, domain.Buy, "59000", "0.1", false)
	coinBid := r.placeOn(t, coinPerp.Symbol, bob, domain.Buy, "59000", "1")
	tp, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: alice, Symbol: perp.Symbol, Kind: domain.TakeProfit, TriggerPrice: d("61000"), Qty: d("0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelRequested := func(user, id string) bool {
		t.Helper()
		o, err := r.svc.Get(ctx, user, id)
		if err != nil {
			t.Fatal(err)
		}
		return o.CancelRequested
	}

	if _, err := r.svc.CancelProduct(ctx, flags.KeyProductUSDTM, "ops@example.com", "closing the line"); apperr.From(err).Code != "COMMON_CONFLICT" {
		t.Fatalf("canceling an open line's orders: %v", err)
	}
	fl.set(flags.KeyProductUSDTM, true)
	if _, err := r.svc.CancelProduct(ctx, flags.KeyProductUSDTM, "ops@example.com", ""); apperr.From(err).Kind != apperr.KindInvalid {
		t.Fatalf("without a reason: %v", err)
	}
	fl.down = errors.New("flags unreadable")
	if _, err := r.svc.CancelProduct(ctx, flags.KeyProductUSDTM, "ops@example.com", "closing the line"); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("the flags unreadable: %v", err)
	}
	fl.down = nil
	if orders, positions, err := r.svc.ProductCounts(ctx, flags.KeyProductUSDTM); err != nil || orders != 2 || positions != 2 {
		t.Fatalf("counts before: %d orders, %d positions, %v", orders, positions, err)
	}
	reads := fl.refreshes
	done, err := r.svc.CancelProduct(ctx, flags.KeyProductUSDTM, "ops@example.com", "closing the line")
	if err != nil || len(done) != 2 || fl.refreshes != reads+1 {
		t.Fatalf("closing: %+v %v (%d reads)", done, err, fl.refreshes-reads)
	}
	for _, p := range done {
		if p.UserID != alice || p.Symbol != perp.Symbol || p.Conditional != (p.ID == tp.ID) || (p.ID != tp.ID && p.ID != bid.ID) {
			t.Fatalf("canceled %+v", p)
		}
	}
	if !cancelRequested(alice, bid.ID) || cancelRequested(bot, botBid.ID) || cancelRequested(bob, coinBid.ID) {
		t.Fatal("only alice's order on the closed line is canceled")
	}
	list, _, err := r.svc.Conditionals(ctx, alice, perp.Symbol, "", "", 10)
	if err != nil || len(list) != 1 || list[0].Status != domain.ConditionalCanceled || list[0].Reason != "PRODUCT_CLOSED" {
		t.Fatalf("the take-profit %+v %v", list, err)
	}
	if events := r.events(); events != nil {
		audited := 0
		for _, e := range events {
			if a, ok := e.(*auditv1.AdminActionPerformed); ok && a.GetAction() == "admin.orders.canceled" {
				if a.GetActor() != "ops@example.com" || a.GetTarget() != "user:"+alice || a.GetReason() != "closing the line" {
					t.Fatalf("audit %+v", a)
				}
				audited++
			}
		}
		if audited != 2 {
			t.Fatalf("%d cancels audited", audited)
		}
	}
	if again, err := r.svc.CancelProduct(ctx, flags.KeyProductUSDTM, "ops@example.com", "closing the line"); err != nil || len(again) != 0 {
		t.Fatalf("closing again: %+v %v", again, err)
	}

	// Closed: no opening order, no new take-profit; a close, the market
	// maker's quotes and the other line's orders go in.
	_, err = r.svc.Place(ctx, domain.Request{UserID: alice, Symbol: perp.Symbol, Side: domain.Buy, Type: domain.Limit, Price: d("59000"), Qty: d("0.1")})
	if e := apperr.From(err); e.Code != "PRODUCT_CLOSED" || e.Kind != apperr.KindForbidden || e.Details["product"] != "usdt_m" {
		t.Fatalf("an opening order: %v", err)
	}
	if _, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: alice, Symbol: perp.Symbol, Kind: domain.StopLoss, TriggerPrice: d("59000"),
	}); apperr.From(err).Code != "PRODUCT_CLOSED" {
		t.Fatalf("a stop-loss: %v", err)
	}
	closing := r.place(t, alice, domain.Sell, "60500", "0.1", true)
	r.place(t, bot, domain.Sell, "61000", "0.1", false)
	r.placeOn(t, coinPerp.Symbol, bob, domain.Buy, "59000", "1")

	// An opening order taken as the line closed is swept, not one placed
	// well before (the console's cancel takes that) nor the close.
	fl.set(flags.KeyProductUSDTM, false)
	late := r.place(t, bob, domain.Buy, "59000", "0.1", false)
	fl.setAt(flags.KeyProductUSDTM, true, time.Now().Add(time.Minute))
	if n, err := r.svc.SweepClosed(ctx); err != nil || n != 0 {
		t.Fatalf("sweep of what came in a minute before: %d %v", n, err)
	}
	fl.set(flags.KeyProductUSDTM, true)
	if n, err := r.svc.SweepClosed(ctx); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if !cancelRequested(bob, late.ID) || cancelRequested(alice, closing.ID) {
		t.Fatal("the sweep cancels the opening order only")
	}
	if n, err := r.svc.SweepClosed(ctx); err != nil || n != 0 {
		t.Fatalf("sweep again: %d %v", n, err)
	}
	if orders, positions, err := r.svc.ProductCounts(ctx, flags.KeyProductUSDTM); err != nil || orders != 1 || positions != 2 {
		t.Fatalf("counts after: %d orders (alice's close), %d positions, %v", orders, positions, err)
	}

	// Open again: opening orders are taken and stay.
	fl.set(flags.KeyProductUSDTM, false)
	r.place(t, alice, domain.Buy, "59000", "0.1", false)
	if n, err := r.svc.SweepClosed(ctx); err != nil || n != 0 {
		t.Fatalf("sweep of an open line: %d %v", n, err)
	}

	// The coin-margined line closes on its own.
	fl.set(flags.KeyProductCoinM, true)
	_, err = r.svc.Place(ctx, domain.Request{UserID: bob, Symbol: coinPerp.Symbol, Side: domain.Buy, Type: domain.Limit, Price: d("59000"), Qty: d("1")})
	if e := apperr.From(err); e.Code != "PRODUCT_CLOSED" || e.Details["product"] != "coin_m" {
		t.Fatalf("an opening order on the coin-margined line: %v", err)
	}
	r.place(t, alice, domain.Buy, "59000", "0.1", false)
}

// The product lines by the names the API uses.
func TestProductLinesByName(t *testing.T) {
	for name, want := range map[string]string{"usdt_m": flags.KeyProductUSDTM, "coin_m": flags.KeyProductCoinM} {
		if key, err := application.ProductKey(name); err != nil || key != want {
			t.Fatalf("%s: %s %v", name, key, err)
		}
	}
	if _, err := application.ProductKey("spot"); apperr.From(err).Kind != apperr.KindNotFound {
		t.Fatalf("spot is spot-trading-service's: %v", err)
	}
	if application.ProductOf(perp) != flags.KeyProductUSDTM || application.ProductOf(coinPerp) != flags.KeyProductCoinM {
		t.Fatal("a contract's line")
	}
}
