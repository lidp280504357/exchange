package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// engine plays the matching engine in Flatten's waits (Service.Sleep):
// the cancels asked for come back CANCELED; a console order (ADMIN) fills
// whole against HOUSE at its price when fill says so, else IOC leaves it
// unfilled.
func (r *rig) engine(house string, fill func(domain.Order) bool) func(context.Context, time.Duration) error {
	return func(ctx context.Context, _ time.Duration) error {
		active, err := r.store.Read().Orders().ActiveOn(ctx, []string{perp.Symbol, ethPerp.Symbol, coinPerp.Symbol})
		if err != nil {
			return err
		}
		for _, o := range active {
			r.seq++
			u := domain.Update{OrderID: o.ID, Seq: r.seq, Status: domain.StatusCanceled, Filled: o.Filled, FilledQuote: o.FilledQuote, Reason: "USER"}
			switch {
			case o.CancelRequested:
			case o.Kind != domain.KindAdmin:
				continue
			case fill(o):
				tr := application.Trade{ID: uuid.Must(uuid.NewV7()).String(), Symbol: o.Symbol, Seq: r.seq, Price: o.Price, Qty: o.Qty, At: time.Now()}
				if o.Side == domain.Buy {
					tr.BuyerOrderID, tr.BuyerUserID, tr.SellerUserID, tr.HouseSide = o.ID, o.UserID, house, domain.Sell
				} else {
					tr.SellerOrderID, tr.SellerUserID, tr.BuyerUserID, tr.HouseSide, tr.BuyerIsMaker = o.ID, o.UserID, house, domain.Buy, true
				}
				if err := r.svc.OnTrade(ctx, tr); err != nil {
					return err
				}
				r.seq++
				u = domain.Update{OrderID: o.ID, Seq: r.seq, Status: domain.StatusFilled, Filled: o.Qty, FilledQuote: o.Price.Mul(o.Qty)}
			default:
				u.Reason = "IOC"
			}
			if err := r.svc.OnUpdate(ctx, u); err != nil {
				return err
			}
		}
		return nil
	}
}

// flattenSetup gives alice a long of 0.5 BTC-USDT-PERP, ETH-USDT-PERP in
// hedge mode with a long of 2 and a short of 1, a short of 10 contracts
// of BTC-USD-PERP, bob the other sides; HOUSE is funded to take them.
func flattenSetup(t *testing.T) (r *rig, alice, house string) {
	t.Helper()
	r = setup(t)
	r.svc.Instruments = instruments{coin: true, eth: true}
	ctx := context.Background()
	r.book.Set(ethPerp.Symbol, d("3000"), time.Now())
	r.book.Set(coinPerp.Symbol, d("60000"), time.Now())
	alice, bob, house := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.svc.HouseUser = house
	for _, u := range []string{alice, bob} {
		r.fund(u, "100000")
		r.fundIn(u, "BTC", "1")
	}
	r.fund(house, "10000000")
	r.fundIn(house, "BTC", "1000")
	r.trade(t, r.place(t, bob, domain.Sell, "60000", "0.5", false), r.place(t, alice, domain.Buy, "60000", "0.5", false), "60000")
	hedge := domain.Hedge
	if _, err := r.svc.UpdateSettings(ctx, alice, ethPerp.Symbol, application.SettingsChange{PositionMode: &hedge}); err != nil {
		t.Fatal(err)
	}
	eth := func(side domain.Side, ps domain.PositionSide, qty string) domain.Order {
		t.Helper()
		o, err := r.svc.Place(ctx, domain.Request{
			UserID: alice, Symbol: ethPerp.Symbol, Side: side, PositionSide: ps, Type: domain.Limit, Price: d("3000"), Qty: d(qty),
		})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	r.trade(t, r.placeOn(t, ethPerp.Symbol, bob, domain.Sell, "3000", "2"), eth(domain.Buy, domain.SideLong, "2"), "3000")
	r.trade(t, r.placeOn(t, ethPerp.Symbol, bob, domain.Buy, "3000", "1"), eth(domain.Sell, domain.SideShort, "1"), "3000")
	r.trade(t, r.placeOn(t, coinPerp.Symbol, bob, domain.Buy, "60000", "10"), r.placeOn(t, coinPerp.Symbol, alice, domain.Sell, "60000", "10"), "60000")
	return r, alice, house
}

// A purge flattens a test account's contract accounts (L4b): its orders
// and take-profits canceled and audited, every position - both hedge
// sides, USDT and coin-margined - closed against HOUSE with IOC orders at
// the mark moved by 0.5% against it; a repeat has nothing left to do.
func TestFlattenClosesEverythingAgainstHOUSE(t *testing.T) {
	r, alice, house := flattenSetup(t)
	ctx := context.Background()
	bid := r.place(t, alice, domain.Buy, "59000", "0.1", false)
	tp, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: alice, Symbol: perp.Symbol, Kind: domain.TakeProfit, TriggerPrice: d("65000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc.Sleep = r.engine(house, func(domain.Order) bool { return true })
	res, err := r.svc.Flatten(ctx, alice, "ops@example.com", "purging a test account")
	if err != nil || res.CanceledOrders != 1 || res.CanceledConditionals != 1 || len(res.Remaining) != 0 {
		t.Fatalf("flatten %+v %v", res, err)
	}
	want := map[string][2]string{
		perp.Symbol + "|LONG": {"0.5", "59700"}, ethPerp.Symbol + "|LONG": {"2", "2985"},
		ethPerp.Symbol + "|SHORT": {"1", "3015"}, coinPerp.Symbol + "|SHORT": {"10", "60300"},
	}
	if len(res.Closed) != len(want) {
		t.Fatalf("closed %+v", res.Closed)
	}
	for _, c := range res.Closed {
		w, ok := want[c.Symbol+"|"+c.Side]
		if !ok || !c.Quantity.Equal(d(w[0])) || !c.Price.Equal(d(w[1])) {
			t.Fatalf("closed %+v, want %v", c, w)
		}
	}
	for _, symbol := range []string{perp.Symbol, ethPerp.Symbol, coinPerp.Symbol} {
		views, err := r.svc.Positions(ctx, alice, symbol)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range views {
			if !v.Flat() {
				t.Fatalf("alice's %s %+v", symbol, v.Position)
			}
		}
	}
	if o, err := r.svc.Get(ctx, alice, bid.ID); err != nil || o.Status != domain.StatusCanceled {
		t.Fatalf("the bid %+v %v", o, err)
	}
	if c := r.conditional(t, alice, tp.ID); c.Status != domain.ConditionalCanceled || c.Reason != application.ReasonAdmin {
		t.Fatalf("the take-profit %+v", c)
	}
	orders, _, err := r.svc.List(ctx, alice, "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	closes := 0
	for _, o := range orders {
		if o.Kind != domain.KindAdmin {
			continue
		}
		closes++
		if o.Type != domain.Limit || o.TimeInForce != domain.IOC || o.ReduceOnly != (o.PositionSide == domain.SideBoth) || o.Status != domain.StatusFilled {
			t.Fatalf("a close %+v", o)
		}
	}
	if closes != 4 {
		t.Fatalf("%d closes", closes)
	}
	if events := r.events(); events != nil {
		canceled, flattened := 0, 0
		for _, e := range events {
			a, ok := e.(*auditv1.AdminActionPerformed)
			if !ok {
				continue
			}
			if a.GetActor() != "ops@example.com" || a.GetReason() != "purging a test account" || a.GetTarget() != "user:"+alice {
				t.Fatalf("audit %+v", a)
			}
			switch a.GetAction() {
			case "admin.orders.canceled":
				canceled++
			case "derivatives.user_flattened":
				flattened++
				var details struct {
					Orders int               `json:"canceled_orders"`
					Closed []json.RawMessage `json:"closed"`
					Left   []json.RawMessage `json:"remaining"`
				}
				if err := json.Unmarshal([]byte(a.GetDetails()), &details); err != nil || details.Orders != 1 || len(details.Closed) != 4 ||
					details.Left == nil || len(details.Left) != 0 {
					t.Fatalf("the audit's details %s %v", a.GetDetails(), err)
				}
			}
		}
		if canceled != 2 || flattened != 1 {
			t.Fatalf("%d cancels and %d flattens audited", canceled, flattened)
		}
	}
	again, err := r.svc.Flatten(ctx, alice, "ops@example.com", "purging a test account")
	if err != nil || again.CanceledOrders != 0 || again.CanceledConditionals != 0 || len(again.Closed) != 0 || len(again.Remaining) != 0 {
		t.Fatalf("again %+v %v", again, err)
	}
	r.reconcile(t, "BTC")
}

// What Flatten cannot close comes back with the reason: nothing to fill
// at the price after three rounds, no fresh mark price, a close still
// working when the engine does not answer (the later rounds wait for it
// rather than place another).
func TestFlattenLeavesWhatItCannotClose(t *testing.T) {
	r := setup(t)
	r.svc.Instruments = instruments{eth: true}
	ctx := context.Background()
	alice, bob, house := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.svc.HouseUser = house
	r.fund(alice, "100000")
	r.fund(bob, "100000")
	r.fund(house, "10000000")
	r.book.Set(ethPerp.Symbol, d("3000"), time.Now())
	r.trade(t, r.place(t, bob, domain.Sell, "60000", "0.5", false), r.place(t, alice, domain.Buy, "60000", "0.5", false), "60000")
	r.trade(t, r.placeOn(t, ethPerp.Symbol, bob, domain.Sell, "3000", "2"), r.placeOn(t, ethPerp.Symbol, alice, domain.Buy, "3000", "2"), "3000")
	r.book.Set(ethPerp.Symbol, d("3000"), time.Now().Add(-time.Hour))
	closes := func() (n, active int) {
		t.Helper()
		orders, _, err := r.svc.List(ctx, alice, perp.Symbol, "", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range orders {
			if o.Kind == domain.KindAdmin {
				n++
				if o.Status.Active() {
					active++
				}
			}
		}
		return n, active
	}
	left := func(res application.FlattenResult, want map[string]string) {
		t.Helper()
		if len(res.Remaining) != len(want) {
			t.Fatalf("remaining %+v", res.Remaining)
		}
		for _, l := range res.Remaining {
			if want[l.Symbol+"|"+l.Side+"|"+l.Quantity.String()] != l.Reason {
				t.Fatalf("remaining %+v, want %v", l, want)
			}
		}
	}

	// HOUSE takes nothing: three rounds, a second apart.
	pauses := 0
	r.svc.FlattenPause = 7 * time.Millisecond
	none := r.engine(house, func(domain.Order) bool { return false })
	r.svc.Sleep = func(ctx context.Context, d time.Duration) error {
		if d == r.svc.FlattenPause {
			pauses++
		}
		return none(ctx, d)
	}
	res, err := r.svc.Flatten(ctx, alice, "ops@example.com", "purging a test account")
	if err != nil || len(res.Closed) != 0 || pauses != 2 {
		t.Fatalf("flatten %+v %v (%d pauses)", res, err, pauses)
	}
	left(res, map[string]string{perp.Symbol + "|LONG|0.5": application.LeftNotFilled, ethPerp.Symbol + "|LONG|2": "DERIV_MARK_PRICE_UNAVAILABLE"})
	if n, active := closes(); n != 3 || active != 0 {
		t.Fatalf("%d closes, %d active", n, active)
	}

	// The engine does not answer: the close stays working, the later
	// rounds wait for it.
	r.svc.FlattenWait, r.svc.FlattenPoll = 3*time.Millisecond, time.Millisecond
	r.svc.Sleep = func(context.Context, time.Duration) error { return nil }
	res, err = r.svc.Flatten(ctx, alice, "ops@example.com", "purging a test account")
	if err != nil || len(res.Closed) != 0 {
		t.Fatalf("flatten %+v %v", res, err)
	}
	left(res, map[string]string{perp.Symbol + "|LONG|0.5": "DERIV_CLOSE_PENDING", ethPerp.Symbol + "|LONG|2": "DERIV_MARK_PRICE_UNAVAILABLE"})
	if n, active := closes(); n != 4 || active != 1 {
		t.Fatalf("%d closes, %d active", n, active)
	}

	// It answers again and the mark is back: the working close fills as
	// the next call waits for it (not this call's close), the ETH long
	// closes.
	r.book.Set(ethPerp.Symbol, d("3000"), time.Now())
	r.svc.Sleep = r.engine(house, func(domain.Order) bool { return true })
	res, err = r.svc.Flatten(ctx, alice, "ops@example.com", "purging a test account")
	if err != nil || len(res.Remaining) != 0 || len(res.Closed) != 1 || res.Closed[0].Symbol != ethPerp.Symbol ||
		!res.Closed[0].Quantity.Equal(d("2")) || !res.Closed[0].Price.Equal(d("2985")) {
		t.Fatalf("flatten %+v %v", res, err)
	}
	if p := r.position(t, alice); !p.Flat() {
		t.Fatalf("alice's BTC %+v", p)
	}
	r.reconcile(t)
}

// A close the engine reports FILLED before its trade is applied is not
// done yet: the next round would see the position as it was and close it
// again (the other way).
func TestFlattenWaitsForTheFillsToBeApplied(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob, house := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.svc.HouseUser = house
	r.fund(alice, "100000")
	r.fund(bob, "100000")
	r.fund(house, "10000000")
	r.trade(t, r.place(t, bob, domain.Sell, "60000", "0.5", false), r.place(t, alice, domain.Buy, "60000", "0.5", false), "60000")
	var later []application.Trade
	r.svc.FlattenPause = 7 * time.Millisecond
	r.svc.Sleep = func(ctx context.Context, d time.Duration) error {
		if d == r.svc.FlattenPause { // the engine answers only as Flatten waits for it
			return nil
		}
		for _, tr := range later {
			if err := r.svc.OnTrade(ctx, tr); err != nil {
				return err
			}
		}
		later = nil
		active, err := r.store.Read().Orders().ActiveOn(ctx, []string{perp.Symbol})
		if err != nil {
			return err
		}
		for _, o := range active {
			r.seq++
			later = append(later, application.Trade{
				ID: uuid.Must(uuid.NewV7()).String(), Symbol: o.Symbol, Seq: r.seq, Price: o.Price, Qty: o.Qty, At: time.Now(),
				SellerOrderID: o.ID, SellerUserID: alice, BuyerUserID: house, HouseSide: domain.Buy, BuyerIsMaker: true,
			})
			r.seq++
			if err := r.svc.OnUpdate(ctx, domain.Update{
				OrderID: o.ID, Seq: r.seq, Status: domain.StatusFilled, Filled: o.Qty, FilledQuote: o.Price.Mul(o.Qty),
			}); err != nil {
				return err
			}
		}
		return nil
	}
	res, err := r.svc.Flatten(ctx, alice, "ops@example.com", "purging a test account")
	if err != nil || len(res.Remaining) != 0 || len(res.Closed) != 1 || !res.Closed[0].Quantity.Equal(d("0.5")) {
		t.Fatalf("flatten %+v %v", res, err)
	}
	orders, _, err := r.svc.List(ctx, alice, perp.Symbol, "", "", 100)
	if err != nil || len(orders) != 2 || orders[0].Kind != domain.KindAdmin {
		t.Fatalf("alice's orders %+v %v", orders, err)
	}
	if p := r.position(t, alice); !p.Flat() {
		t.Fatalf("alice %+v", p)
	}
	r.reconcile(t)
}

// A flatten that fails midway - here the caller gone while it waits for
// the engine - still audits what it did: the cancels went out (review C79
// ④).
func TestFlattenAuditsWhatItDidWhenItFails(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice := uuid.NewString()
	r.fund(alice, "10000")
	bid := r.place(t, alice, domain.Buy, "59000", "0.1", false)
	r.svc.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	if _, err := r.svc.Flatten(ctx, alice, "ops@example.com", "purging a test account"); !errors.Is(err, context.Canceled) {
		t.Fatalf("a flatten whose caller went: %v", err)
	}
	if o, err := r.svc.Get(ctx, alice, bid.ID); err != nil || !o.CancelRequested {
		t.Fatalf("the bid %+v %v", o, err)
	}
	if events := r.events(); events != nil {
		var flattened []string
		for _, e := range events {
			if a, ok := e.(*auditv1.AdminActionPerformed); ok && a.GetAction() == "derivatives.user_flattened" {
				flattened = append(flattened, a.GetDetails())
			}
		}
		if len(flattened) != 1 || !strings.Contains(flattened[0], `"canceled_orders":1`) || !strings.Contains(flattened[0], `"complete":false`) ||
			!strings.Contains(flattened[0], `"error":"COMMON_INTERNAL"`) {
			t.Fatalf("the audits of the failed flatten %v", flattened)
		}
	}
}

// Flatten refuses a cross account being liquidated (the liquidation
// engine closes it), HOUSE, and a call without an actor and a reason; it
// changes nothing then. A user who never traded contracts - whether or
// not user-service knows them, it is not asked (the coordinator's 06:14
// rule) - has nothing to flatten: complete.
func TestFlattenRefusals(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob, ghost, house := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.svc.HouseUser = house
	r.fund(alice, "700")
	r.fund(bob, "10000")
	crossTakenOver(t, r, alice, bob)
	tp, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: bob, Symbol: perp.Symbol, Kind: domain.TakeProfit, TriggerPrice: d("65000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc.Sleep = func(context.Context, time.Duration) error {
		t.Fatal("no wait")
		return nil
	}
	_, err = r.svc.Flatten(ctx, alice, "ops@example.com", "purging a test account")
	if e := apperr.From(err); e.Code != "DERIV_POSITION_LIQUIDATING" || e.Kind != apperr.KindConflict || e.Details["settle_asset"] != "USDT" {
		t.Fatalf("an account being liquidated: %v", err)
	}
	for _, c := range []struct {
		user, actor, reason, code string
	}{
		{house, "ops@example.com", "purging a test account", "DERIV_HOUSE_NOT_CLOSED"},
		{"bob", "ops@example.com", "purging a test account", apperr.CodeInvalidArgument},
		{bob, "", "purging a test account", apperr.CodeInvalidArgument},
		{bob, "ops@example.com", "no", apperr.CodeInvalidArgument},
	} {
		if _, err := r.svc.Flatten(ctx, c.user, c.actor, c.reason); apperr.From(err).Code != c.code {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	if c := r.conditional(t, bob, tp.ID); c.Status != domain.ConditionalActive {
		t.Fatalf("bob's take-profit %+v", c)
	}
	if p := r.position(t, bob); !p.Qty.Equal(d("0.5")) {
		t.Fatalf("bob %+v", p)
	}
	res, err := r.svc.Flatten(ctx, ghost, "ops@example.com", "purging a test account")
	if err != nil || res.CanceledOrders != 0 || res.CanceledConditionals != 0 || len(res.Closed) != 0 || len(res.Remaining) != 0 || !res.Complete() {
		t.Fatalf("a user who never traded contracts %+v %v", res, err)
	}
}

// A position under liquidation (isolated here) is the liquidation
// engine's: Flatten refuses the user before it changes anything.
func TestFlattenRefusesAnIsolatedLiquidation(t *testing.T) {
	r, _, bob := liquidationSetup(t)
	ctx := context.Background()
	r.monitor(t, "59000") // bob's isolated long taken over
	if p := r.position(t, bob); !p.Liquidating {
		t.Fatalf("bob %+v", p)
	}
	tp, err := r.svc.CreateConditional(ctx, domain.ConditionalRequest{
		UserID: bob, Symbol: perp.Symbol, Kind: domain.StopLoss, TriggerPrice: d("58000"),
	})
	if apperr.From(err).Code != "DERIV_POSITION_LIQUIDATING" {
		t.Fatalf("a stop-loss on a position under liquidation %+v %v", tp, err)
	}
	_, err = r.svc.Flatten(ctx, bob, "ops@example.com", "purging a test account")
	if e := apperr.From(err); e.Code != "DERIV_POSITION_LIQUIDATING" || e.Kind != apperr.KindConflict || e.Details["symbol"] != perp.Symbol ||
		e.Details["position_side"] != "BOTH" {
		t.Fatalf("a position under liquidation: %v", err)
	}
	for _, e := range r.events() {
		if a, ok := e.(*auditv1.AdminActionPerformed); ok {
			t.Fatalf("audited %+v", a)
		}
	}
}
