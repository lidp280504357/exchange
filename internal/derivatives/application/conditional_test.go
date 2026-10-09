package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
)

// staleStore answers the active conditional orders read before they
// ended: the trigger loop reads them outside any transaction, and a user
// or the liquidation engine can end one between that read and its order.
type staleStore struct {
	ports.Store
	active []domain.Conditional
}

func (s staleStore) Read() ports.Repos { return staleRepos{Repos: s.Store.Read(), active: s.active} }

type staleRepos struct {
	ports.Repos
	active []domain.Conditional
}

func (r staleRepos) Conditionals() ports.ConditionalRepo {
	return staleConditionals{ConditionalRepo: r.Repos.Conditionals(), active: r.active}
}

type staleConditionals struct {
	ports.ConditionalRepo
	active []domain.Conditional
}

func (c staleConditionals) Active(context.Context, string) ([]domain.Conditional, error) {
	return c.active, nil
}

// conditional returns one of the user's conditional orders.
func (r *rig) conditional(t *testing.T, user, id string) domain.Conditional {
	t.Helper()
	list, _, err := r.svc.Conditionals(context.Background(), user, "", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no conditional %s", id)
	return domain.Conditional{}
}

// ordersOfKind counts the user's orders of a kind.
func (r *rig) ordersOfKind(t *testing.T, user string, kind domain.Kind) int {
	t.Helper()
	list, _, err := r.svc.List(context.Background(), user, perp.Symbol, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, o := range list {
		if o.Kind == kind {
			n++
		}
	}
	return n
}

// A take-profit its user cancels after the trigger loop read it places
// nothing and stays CANCELED (review C69 ③): its order and its TRIGGERED
// are one transaction under the user's lock, which checks it is active.
func TestATriggerAfterTheUserCanceledPlacesNothing(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	r.trade(t, r.place(t, alice, domain.Buy, "60000", "0.2", false), r.place(t, bob, domain.Sell, "60000", "0.2", false), "60000")
	tp, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: alice, Symbol: perp.Symbol, Kind: domain.TakeProfit, TriggerPrice: d("61000"), Qty: d("0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	read, err := r.store.Read().Conditionals().Active(ctx, "")
	if err != nil || len(read) != 1 {
		t.Fatalf("active %+v %v", read, err)
	}
	if _, err := r.svc.CancelConditional(ctx, alice, tp.ID); err != nil {
		t.Fatal(err)
	}
	r.svc.Store = staleStore{Store: r.store, active: read}
	r.book.Set(perp.Symbol, d("61000"), time.Now())
	if n, err := r.svc.Trigger(ctx); err != nil || n != 0 {
		t.Fatalf("trigger: %d %v", n, err)
	}
	r.svc.Store = r.store
	if c := r.conditional(t, alice, tp.ID); c.Status != domain.ConditionalCanceled || c.Reason != "USER" || c.OrderID != "" {
		t.Fatalf("the take-profit %+v", c)
	}
	if n := r.ordersOfKind(t, alice, domain.KindTakeProfit); n != 0 {
		t.Fatalf("%d take-profit orders placed", n)
	}
}

// A stop-loss ended with its position's take-over keeps that end against a
// trigger that read it before (review C69 ①): before, the trigger's update
// overwrote CANCELED/LIQUIDATION with FAILED.
func TestATakeOverKeepsItsEndAgainstAStaleTrigger(t *testing.T) {
	r, _, bob := liquidationSetup(t)
	ctx := context.Background()
	sl, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: bob, Symbol: perp.Symbol, Kind: domain.StopLoss, TriggerPrice: d("59500"),
	})
	if err != nil {
		t.Fatal(err)
	}
	read, err := r.store.Read().Conditionals().Active(ctx, "")
	if err != nil || len(read) != 1 {
		t.Fatalf("active %+v %v", read, err)
	}
	r.monitor(t, "59000") // Bob's long taken over, its stop-loss ended
	r.svc.Store = staleStore{Store: r.store, active: read}
	if n, err := r.svc.Trigger(ctx); err != nil || n != 0 {
		t.Fatalf("trigger: %d %v", n, err)
	}
	r.svc.Store = r.store
	if c := r.conditional(t, bob, sl.ID); c.Status != domain.ConditionalCanceled || c.Reason != application.ReasonLiquidation {
		t.Fatalf("the stop-loss %+v", c)
	}
	if n := r.ordersOfKind(t, bob, domain.KindStopLoss); n != 0 {
		t.Fatalf("%d stop-loss orders placed", n)
	}
	// Its order is refused as such, before anything else is checked.
	_, err = r.svc.Place(ctx, domain.Request{
		UserID: bob, Symbol: perp.Symbol, Side: domain.Sell, Type: domain.Market, Qty: d("0.5"), ReduceOnly: true,
		ClientOrderID: sl.ID, Kind: domain.KindStopLoss, Conditional: sl.ID,
	})
	if !errors.Is(err, domain.ErrConditionalEnded) {
		t.Fatalf("an ended stop-loss's order: %v", err)
	}
}

// An end never overwrites another (review C69 ①): the store ends only an
// active conditional order.
func TestAnEndedConditionalStaysEnded(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	r.trade(t, r.place(t, alice, domain.Buy, "60000", "0.2", false), r.place(t, bob, domain.Sell, "60000", "0.2", false), "60000")
	tp, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: alice, Symbol: perp.Symbol, Kind: domain.TakeProfit, TriggerPrice: d("61000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.CancelConditional(ctx, alice, tp.ID); err != nil {
		t.Fatal(err)
	}
	tp.Status, tp.Reason = domain.ConditionalFailed, "DERIV_POSITION_LIQUIDATING"
	err = r.store.Tx(ctx, func(repos ports.Repos) error { return repos.Conditionals().Update(ctx, tp) })
	if !errors.Is(err, domain.ErrConditionalEnded) {
		t.Fatalf("an ended conditional updated: %v", err)
	}
	if c := r.conditional(t, alice, tp.ID); c.Status != domain.ConditionalCanceled || c.Reason != "USER" {
		t.Fatalf("the take-profit %+v", c)
	}
}

// In hedge mode a take-over ends only the conditionals of the side it takes
// over (review C69 ②): the isolated long's stop-loss ends with it, the
// short's take-profit stays.
func TestATakeOverEndsOnlyItsSidesConditionals(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	hedge, isolated, fifty := domain.Hedge, domain.Isolated, int32(50)
	if _, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{
		PositionMode: &hedge, MarginMode: &isolated, Leverage: &fifty,
	}); err != nil {
		t.Fatal(err)
	}
	order := func(side domain.Side, ps domain.PositionSide, qty string) domain.Order {
		t.Helper()
		o, err := r.svc.Place(ctx, domain.Request{
			UserID: alice, Symbol: perp.Symbol, Side: side, PositionSide: ps, Type: domain.Limit, Price: d("60000"), Qty: d(qty),
		})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	r.trade(t, r.place(t, bob, domain.Sell, "60000", "0.5", false), order(domain.Buy, domain.SideLong, "0.5"), "60000")
	r.trade(t, r.place(t, bob, domain.Buy, "60000", "0.1", false), order(domain.Sell, domain.SideShort, "0.1"), "60000")
	create := func(ps domain.PositionSide, kind, trigger string) domain.Conditional {
		t.Helper()
		c, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
			UserID: alice, Symbol: perp.Symbol, PositionSide: ps, Kind: kind, TriggerPrice: d(trigger),
		})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	longSL := create(domain.SideLong, domain.StopLoss, "58000")
	shortTP := create(domain.SideShort, domain.TakeProfit, "58500")
	r.monitor(t, "59000") // the isolated long of 0.5 at 50x taken over
	views, err := r.svc.Positions(ctx, alice, perp.Symbol)
	if err != nil || len(views) != 2 {
		t.Fatalf("alice's positions %+v %v", views, err)
	}
	for _, v := range views {
		if v.Liquidating != (v.Side == domain.SideLong) {
			t.Fatalf("alice's %s %+v", v.Side, v.Position)
		}
	}
	if c := r.conditional(t, alice, longSL.ID); c.Status != domain.ConditionalCanceled || c.Reason != application.ReasonLiquidation {
		t.Fatalf("the long's stop-loss %+v", c)
	}
	if c := r.conditional(t, alice, shortTP.ID); c.Status != domain.ConditionalActive {
		t.Fatalf("the short's take-profit %+v", c)
	}
}
