package application_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// crossTakenOver opens alice's cross short of 0.5 at 50x against bob's
// long, both at 60000, and has the liquidation engine take it over at
// 61200: her equity there is 85 (the 85 she has available besides and the
// 600 margin, less the 600 she loses), her maintenance margin 122.4.
// While the liquidation lasts the account takes no cross order and lets
// nothing out.
func crossTakenOver(t *testing.T, r *rig, alice, bob string) {
	t.Helper()
	ctx := context.Background()
	fifty := int32(50)
	if _, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	r.trade(t, r.place(t, bob, domain.Buy, "60000", "0.5", false), r.place(t, alice, domain.Sell, "60000", "0.5", false), "60000")
	r.monitor(t, "61100") // warned
	r.monitor(t, "61200") // taken over
	if p := r.position(t, alice); !p.Liquidating {
		t.Fatalf("taken over %+v", p)
	}
	_, err := r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: perp.Symbol, Side: domain.Sell, Type: domain.Limit, Price: d("61200"), Qty: d("0.01"),
	})
	if e := apperr.From(err); e.Code != "DERIV_POSITION_LIQUIDATING" || e.Details["settle_asset"] != "USDT" {
		t.Fatalf("a cross order while the account is liquidated: %v", err)
	}
	if _, err := r.svc.CrossUnrealizedPnL(ctx, alice, "USDT"); apperr.From(err).Code != "DERIV_POSITION_LIQUIDATING" {
		t.Fatalf("a transfer out while the account is liquidated: %v", err)
	}
}

// liquidate has the engine fill alice's liquidation order at price.
func liquidate(t *testing.T, r *rig, alice, bob, price string) {
	t.Helper()
	r.monitor(t, "61200")
	liq, ok := r.liquidationOrder(t, alice)
	if !ok {
		t.Fatal("no liquidation order")
	}
	r.trade(t, r.place(t, bob, domain.Sell, price, "0.5", true), liq, price)
	if p := r.position(t, alice); !p.Flat() {
		t.Fatalf("alice %+v", p)
	}
}

// crossLiquidated takes alice's cross short over (crossTakenOver) and has
// the engine fill its liquidation order at price. Before the fill, extra
// comes into her available balance.
func crossLiquidated(t *testing.T, price, extra string) (r *rig, alice string) {
	t.Helper()
	r = setup(t)
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "700")
	r.fund(bob, "10000")
	crossTakenOver(t, r, alice, bob)
	if extra != "" {
		r.fund(alice, extra)
	}
	liquidate(t, r, alice, bob, price)
	return r, alice
}

// completed returns the CrossLiquidationCompleted events emitted (nil on
// PostgreSQL).
func (r *rig) completed() []*derivativesv1.CrossLiquidationCompleted {
	var out []*derivativesv1.CrossLiquidationCompleted
	for _, e := range r.events() {
		if c, ok := e.(*derivativesv1.CrossLiquidationCompleted); ok {
			out = append(out, c)
		}
	}
	return out
}

// What a cross account's liquidation leaves goes to the insurance fund as
// the liquidation clearance fee once its positions are closed and settled,
// as Binance does (C68): the account ends at zero, but what came in during
// the liquidation stays the user's. Once over, the account takes orders
// and transfers again.
func TestACrossLiquidationLeavesTheAccountAtZero(t *testing.T) {
	r, alice := crossLiquidated(t, "61300", "500")
	ctx := context.Background()
	// 685 − the 650 loss − the 15.325 fee: 19.675 left, and the 500.
	if !r.ledger.available[alice].Equal(d("519.675")) || !r.ledger.frozen[alice].IsZero() {
		t.Fatalf("alice %s/%s", r.ledger.available[alice], r.ledger.frozen[alice])
	}
	insurance := r.ledger.insurance
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	if !r.ledger.available[alice].Equal(d("500")) || !r.ledger.insurance.Sub(insurance).Equal(d("19.675")) {
		t.Fatalf("alice %s, the fund took %s", r.ledger.available[alice], r.ledger.insurance.Sub(insurance))
	}
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 0 {
		t.Fatalf("settled twice: %d %v", n, err)
	}
	if events := r.events(); events != nil {
		done := r.completed()
		if len(done) != 1 || done[0].GetUserId() != alice || done[0].GetSettleAsset() != "USDT" || done[0].GetClearanceFee() != "19.675" {
			t.Fatalf("completed %+v", done)
		}
	}
	if _, err := r.svc.CrossUnrealizedPnL(ctx, alice, "USDT"); err != nil {
		t.Fatalf("a transfer out once it is over: %v", err)
	}
	r.reconcile(t)
}

// A liquidation that leaves nothing (the fund paid the loss beyond the
// account) books no fee; it still ends, saying so.
func TestACrossLiquidationThatLeavesNothing(t *testing.T) {
	r, alice := crossLiquidated(t, "61500", "")
	ctx := context.Background()
	if !r.ledger.available[alice].IsZero() {
		t.Fatalf("alice %s", r.ledger.available[alice])
	}
	insurance := r.ledger.insurance
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	if !r.ledger.insurance.Equal(insurance) {
		t.Fatalf("the fund moved %s", r.ledger.insurance.Sub(insurance))
	}
	if events := r.events(); events != nil {
		if done := r.completed(); len(done) != 1 || done[0].GetClearanceFee() != "0" {
			t.Fatalf("completed %+v", done)
		}
	}
	r.reconcile(t)
}

// flakyLedger fails or refuses the bookings of clearance fees as told.
type flakyLedger struct {
	ports.Ledger
	err   error
	asked []decimal.Decimal
}

func (l *flakyLedger) Settle(ctx context.Context, req ports.SettleRequest) ([]domain.Outcome, error) {
	if len(req.Moves) == 1 && req.Moves[0].Type == domain.MoveInsurance {
		l.asked = append(l.asked, req.Moves[0].Amount)
		if err := l.err; err != nil {
			l.err = nil
			return nil, err
		}
	}
	return l.Ledger.Settle(ctx, req)
}

// The fee worked out is kept: a booking whose answer was lost is booked
// again with the same amount under the same key, whatever the balance by
// then. One the ledger refused booked nothing: worked out again.
func TestTheClearanceFeeIsBookedOnce(t *testing.T) {
	r, alice := crossLiquidated(t, "61300", "")
	ctx := context.Background()
	flaky := &flakyLedger{Ledger: r.ledger, err: errors.New("ledger unreachable")}
	r.svc.Ledger = flaky
	if n, err := r.svc.SettleCrossLiquidations(ctx); err == nil || n != 0 {
		t.Fatalf("an unreachable ledger: %d %v", n, err)
	}
	r.fund(alice, "100") // came in since
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	if len(flaky.asked) != 2 || !flaky.asked[0].Equal(d("19.675")) || !flaky.asked[1].Equal(d("19.675")) {
		t.Fatalf("booked %v", flaky.asked)
	}
	if !r.ledger.available[alice].Equal(d("100")) {
		t.Fatalf("alice %s", r.ledger.available[alice])
	}

	r, alice = crossLiquidated(t, "61300", "")
	flaky = &flakyLedger{Ledger: r.ledger, err: apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")}
	r.svc.Ledger = flaky
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 0 {
		t.Fatalf("refused: %d %v", n, err)
	}
	if open, err := r.store.Read().CrossLiquidations().AllOpen(ctx); err != nil || len(open) != 1 || open[0].Fee.Valid {
		t.Fatalf("after a refusal %+v %v", open, err)
	}
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 1 || !r.ledger.available[alice].IsZero() {
		t.Fatalf("worked out again: %d %v, alice %s", n, err, r.ledger.available[alice])
	}
}

// While a cross account is being liquidated nothing takes from its
// available balance, as Binance's "user in liquidation mode": no opening
// order on a contract settled in its asset, isolated ones too, no isolated
// margin added, no transfer out - also once its positions are closed, until
// the clearance fee is booked. An isolated position still closes and has
// margin taken back, and what that sets free stays the user's: the fee
// takes what the liquidation left.
func TestACrossAccountBeingLiquidatedKeepsItsBalance(t *testing.T) {
	r := setup(t)
	r.svc.Instruments = instruments{eth: true}
	r.book.Set(ethPerp.Symbol, d("3000"), time.Now())
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	// 700 for the cross short of crossTakenOver, 35.3 for an isolated ETH
	// long of 0.2 at 20x (margin 30, fee 0.3) and 5 more margin on it.
	r.fund(alice, "735.3")
	r.fund(bob, "10000")
	isolated, twenty := domain.Isolated, int32(20)
	if _, err := r.svc.UpdateSettings(ctx, alice, ethPerp.Symbol, application.SettingsChange{MarginMode: &isolated, Leverage: &twenty}); err != nil {
		t.Fatal(err)
	}
	r.trade(t, r.placeOn(t, ethPerp.Symbol, bob, domain.Sell, "3000", "0.2"), r.placeOn(t, ethPerp.Symbol, alice, domain.Buy, "3000", "0.2"), "3000")
	if _, err := r.svc.AdjustMargin(ctx, alice, ethPerp.Symbol, "", d("5")); err != nil {
		t.Fatal(err)
	}
	crossTakenOver(t, r, alice, bob)
	if _, err := r.svc.AdjustMargin(ctx, alice, ethPerp.Symbol, "", d("-5")); err != nil {
		t.Fatalf("isolated margin taken back: %v", err)
	}
	// Half the isolated long closes at 3010: 1 made, the 0.1505 fee out of
	// its margin, the rest of that half's 15 set free.
	sell, err := r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: ethPerp.Symbol, Side: domain.Sell, Type: domain.Limit, Price: d("3010"), Qty: d("0.1"), ReduceOnly: true,
	})
	if err != nil {
		t.Fatalf("an isolated close: %v", err)
	}
	r.trade(t, r.placeOn(t, ethPerp.Symbol, bob, domain.Buy, "3010", "0.1"), sell, "3010")
	liquidate(t, r, alice, bob, "61300")
	// 85 + 5 + 15.8495 + the short's 600 margin - its 650 loss - the
	// 15.325 fee.
	if !r.ledger.available[alice].Equal(d("40.5245")) {
		t.Fatalf("alice %s", r.ledger.available[alice])
	}
	refused := func(what string) {
		t.Helper()
		_, err := r.svc.Place(ctx, domain.Request{
			UserID: alice, Symbol: ethPerp.Symbol, Side: domain.Buy, Type: domain.Limit, Price: d("3000"), Qty: d("0.01"),
		})
		if e := apperr.From(err); e.Code != "DERIV_POSITION_LIQUIDATING" || e.Details["settle_asset"] != "USDT" {
			t.Fatalf("an isolated opening order %s: %v", what, err)
		}
		if _, err := r.svc.AdjustMargin(ctx, alice, ethPerp.Symbol, "", d("1")); apperr.From(err).Code != "DERIV_POSITION_LIQUIDATING" {
			t.Fatalf("isolated margin added %s: %v", what, err)
		}
		if _, err := r.svc.CrossUnrealizedPnL(ctx, alice, "USDT"); apperr.From(err).Code != "DERIV_POSITION_LIQUIDATING" {
			t.Fatalf("a transfer out %s: %v", what, err)
		}
	}
	refused("once the cross positions are closed")
	insurance := r.ledger.insurance
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	// The 19.675 the liquidation left went; 5 + 15.8495 stay.
	if !r.ledger.insurance.Sub(insurance).Equal(d("19.675")) || !r.ledger.available[alice].Equal(d("20.8495")) {
		t.Fatalf("the fund took %s, alice %s", r.ledger.insurance.Sub(insurance), r.ledger.available[alice])
	}
	r.placeOn(t, ethPerp.Symbol, alice, domain.Buy, "3000", "0.01")
	if _, err := r.svc.AdjustMargin(ctx, alice, ethPerp.Symbol, "", d("1")); err != nil {
		t.Fatalf("isolated margin added once it is over: %v", err)
	}
	if _, err := r.svc.CrossUnrealizedPnL(ctx, alice, "USDT"); err != nil {
		t.Fatalf("a transfer out once it is over: %v", err)
	}
	r.reconcile(t)
}

// A cross order the take-over asked to cancel can fill before the engine
// has the cancel: the position it opens is taken over too, so that the
// liquidation, which waits for every cross position to close, ends.
func TestAPositionOpenedAfterTheTakeOverIsLiquidatedToo(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob, carol := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.fund(alice, "800")
	r.fund(bob, "10000")
	r.fund(carol, "10000")
	hedge, fifty := domain.Hedge, int32(50)
	if _, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{PositionMode: &hedge, Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	order := func(side domain.Side, ps domain.PositionSide, price, qty string) domain.Order {
		t.Helper()
		o, err := r.svc.Place(ctx, domain.Request{
			UserID: alice, Symbol: perp.Symbol, Side: side, PositionSide: ps, Type: domain.Limit, Price: d(price), Qty: d(qty),
		})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	r.trade(t, r.place(t, bob, domain.Buy, "60000", "0.5", false), order(domain.Sell, domain.SideShort, "60000", "0.5"), "60000")
	resting := order(domain.Buy, domain.SideLong, "59500", "0.01")
	sides := func() map[domain.PositionSide]domain.Position {
		t.Helper()
		views, err := r.svc.Positions(ctx, alice, perp.Symbol)
		if err != nil {
			t.Fatal(err)
		}
		out := map[domain.PositionSide]domain.Position{}
		for _, v := range views {
			out[v.Side] = v.Position
		}
		return out
	}
	mark := 60500
	for ; !sides()[domain.SideShort].Liquidating; mark += 100 {
		if mark > 64000 {
			t.Fatal("never taken over")
		}
		r.monitor(t, strconv.Itoa(mark))
	}
	if o, err := r.svc.Get(ctx, alice, resting.ID); err != nil || !o.CancelRequested {
		t.Fatalf("the resting order %+v %v", o, err)
	}
	// The engine fills it before it has the cancel.
	r.trade(t, resting, r.placeOn(t, perp.Symbol, carol, domain.Sell, "59500", "0.01"), "59500")
	if long := sides()[domain.SideLong]; !long.Qty.Equal(d("0.01")) || !long.Liquidating {
		t.Fatalf("the long opened after the take-over %+v", long)
	}
	// The liquidation engine closes both.
	r.monitor(t, strconv.Itoa(mark))
	list, _, err := r.svc.List(ctx, alice, perp.Symbol, "ACTIVE", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, liq := range list {
		if liq.Kind != domain.KindLiquidation {
			continue
		}
		other := domain.Sell
		if liq.Side == domain.Sell {
			other = domain.Buy
		}
		r.trade(t, r.placeOn(t, perp.Symbol, carol, other, liq.Price.String(), liq.Qty.String()), liq, liq.Price.String())
		n++
	}
	if n != 2 {
		t.Fatalf("%d liquidation orders", n)
	}
	if s := sides(); !s[domain.SideShort].Flat() || !s[domain.SideLong].Flat() {
		t.Fatalf("alice %+v", s)
	}
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	if !r.ledger.available[alice].IsZero() || !r.ledger.frozen[alice].IsZero() {
		t.Fatalf("alice %s/%s", r.ledger.available[alice], r.ledger.frozen[alice])
	}
	r.reconcile(t)
}

// What funding pays to or takes from the positions of a cross account
// being liquidated counts too: here the short being closed receives 3.06
// (0.5 x 61200 x 0.0001), and the account still ends at zero.
func TestFundingDuringACrossLiquidationCounts(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "700")
	r.fund(bob, "10000")
	crossTakenOver(t, r, alice, bob)
	eight := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	r.svc.Now = func() time.Time { return eight.Add(3 * time.Second) }
	if n, err := r.svc.SnapshotFunding(ctx); err != nil || n != 1 {
		t.Fatalf("snapshot: %d %v", n, err)
	}
	r.rates[fmt.Sprintf("%s|%d", perp.Symbol, eight.Unix())] = [2]decimal.Decimal{d("0.0001"), d("61200")}
	if n, err := r.svc.SettleFunding(ctx); err != nil || n != 1 {
		t.Fatalf("funding: %d %v", n, err)
	}
	r.svc.Now = time.Now
	liquidate(t, r, alice, bob, "61300")
	// The 19.675 of TestACrossLiquidationLeavesTheAccountAtZero and the 3.06.
	if !r.ledger.available[alice].Equal(d("22.735")) {
		t.Fatalf("alice %s", r.ledger.available[alice])
	}
	if n, err := r.svc.SettleCrossLiquidations(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	if !r.ledger.available[alice].IsZero() {
		t.Fatalf("alice %s", r.ledger.available[alice])
	}
	r.reconcile(t)
}
