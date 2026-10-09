package application

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/trading/domain"
	"github.com/skill/exchange/internal/trading/ports"
)

type memFills memRepos

func (r memFills) Insert(_ context.Context, f domain.Fill) error {
	if r.s.fills == nil {
		r.s.fills = map[string]domain.Fill{}
	}
	if _, ok := r.s.fills[f.TradeID+f.OrderID]; !ok {
		r.s.fills[f.TradeID+f.OrderID] = f
	}
	return nil
}

func (r memFills) OfOrder(_ context.Context, orderID string) ([]domain.Fill, error) {
	var out []domain.Fill
	for _, f := range r.s.fills {
		if f.OrderID == orderID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (r memFills) OfUser(_ context.Context, userID, symbol, _ string, limit int) ([]domain.Fill, error) {
	var out []domain.Fill
	for _, f := range r.s.fills {
		if f.UserID == userID && (symbol == "" || f.Symbol == symbol) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq > out[j].Seq })
	return out[:min(len(out), limit)], nil
}

func (r memFills) LastTrade(_ context.Context, symbol string) (decimal.Decimal, time.Time, error) {
	var last domain.Fill
	for _, f := range r.s.fills {
		if f.Symbol == symbol && f.Seq > last.Seq {
			last = f
		}
	}
	return last.Price, last.ExecutedAt, nil
}

func unreleased(o domain.Order) bool {
	return !o.Released && o.FreezeState == domain.FreezeDone && o.Status.Terminal()
}

// Unreleased takes the longest untried first, as the store does: since
// the order finished, or since its last failed try.
func (r memOrders) Unreleased(_ context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range r.s.orders {
		if unreleased(o) && o.UpdatedAt.Before(cutoff) {
			out = append(out, o)
		}
	}
	next := func(o domain.Order) time.Time {
		if at, ok := r.s.attempts[o.ID]; ok {
			return at
		}
		return o.UpdatedAt
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := next(out[i]), next(out[j]); !a.Equal(b) {
			return a.Before(b)
		}
		return out[i].ID < out[j].ID
	})
	return out[:min(len(out), limit)], nil
}

func (r memOrders) ReleaseAttempted(_ context.Context, orderID string, at time.Time) error {
	if o, ok := r.s.orders[orderID]; ok && !o.Released {
		if r.s.attempts == nil {
			r.s.attempts = map[string]time.Time{}
		}
		r.s.attempts[orderID] = at
	}
	return nil
}

func (r memOrders) UnreleasedStats(context.Context) (int, time.Time, error) {
	n, oldest := 0, time.Time{}
	for _, o := range r.s.orders {
		if unreleased(o) {
			n++
			if oldest.IsZero() || o.UpdatedAt.Before(oldest) {
				oldest = o.UpdatedAt
			}
		}
	}
	return n, oldest, nil
}

func update(orderID string, seq int64, status domain.Status, filled, quote, reason string) domain.Update {
	return domain.Update{OrderID: orderID, Seq: seq, Status: status, Filled: d(filled), FilledQuote: d(quote), Reason: reason}
}

func TestEngineUpdatesApplyInSequenceOrder(t *testing.T) {
	svc, _, _, _ := newService()
	ctx := context.Background()
	o, _ := svc.Place(ctx, buy("c1")) // 0.001 at 60000: 60 USDT frozen
	for _, u := range []domain.Update{
		update(o.ID, 5, domain.StatusPartiallyFilled, "0.0004", "23.99", ""),
		update(o.ID, 3, domain.StatusOpen, "0", "0", ""), // older: ignored
	} {
		if err := svc.OnUpdate(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := svc.Get(ctx, "u1", o.ID)
	if got.Status != domain.StatusPartiallyFilled || !got.FilledQuantity.Equal(d("0.0004")) || got.Sequence != 5 {
		t.Fatalf("order: %+v", got)
	}
	if err := svc.OnUpdate(ctx, update(uuid.NewString(), 9, domain.StatusFilled, "1", "1", "")); err != nil {
		t.Fatalf("an unknown order is skipped: %v", err)
	}
}

func TestFinishedOrdersReleaseWhatTheyNoLongerNeed(t *testing.T) {
	svc, store, led, _ := newService()
	ctx := context.Background()
	o, _ := svc.Place(ctx, buy("c1")) // limit buy 0.001 at 60000: 60 frozen
	// 0.0004 filled (at a better price), the rest canceled: the fills
	// consumed 0.0004 x 60000 = 24 of the frozen 60.
	if err := svc.OnUpdate(ctx, update(o.ID, 7, domain.StatusCanceled, "0.0004", "23.99", "USER")); err != nil {
		t.Fatal(err)
	}
	if len(led.releases) != 1 || led.releases[0] != "release:"+o.ID+" 36 USDT" {
		t.Fatalf("releases: %v", led.releases)
	}
	got, _ := svc.Get(ctx, "u1", o.ID)
	if !got.Released || got.CancelReason != "USER" || got.Status != domain.StatusCanceled {
		t.Fatalf("order: %+v", got)
	}
	// A redelivered update releases nothing more.
	if err := svc.OnUpdate(ctx, update(o.ID, 7, domain.StatusCanceled, "0.0004", "23.99", "USER")); err != nil || len(led.releases) != 1 {
		t.Fatalf("redelivery: %v %v", err, led.releases)
	}

	// A fully filled sell has nothing left; a market buy keeps its unspent quote.
	sell, _ := svc.Place(ctx, domain.Request{UserID: "u1", Symbol: "BTC-USDT", Side: domain.SideSell, Type: domain.TypeLimit, Price: d("60000"), Quantity: d("0.001")})
	if err := svc.OnUpdate(ctx, update(sell.ID, 8, domain.StatusFilled, "0.001", "60", "")); err != nil || len(led.releases) != 1 {
		t.Fatalf("filled sell: %v %v", err, led.releases)
	}
	mkt, _ := svc.Place(ctx, domain.Request{UserID: "u1", Symbol: "BTC-USDT", Side: domain.SideBuy, Type: domain.TypeMarket, QuoteAmount: d("100")})
	if err := svc.OnUpdate(ctx, update(mkt.ID, 9, domain.StatusFilled, "0.0016", "96.006", "")); err != nil {
		t.Fatal(err)
	}
	if last := led.releases[len(led.releases)-1]; last != "release:"+mkt.ID+" 3.994 USDT" {
		t.Fatalf("market buy release: %v", led.releases)
	}
	// An engine rejection releases everything.
	post, _ := svc.Place(ctx, buy("c9"))
	if err := svc.OnUpdate(ctx, update(post.ID, 10, domain.StatusRejected, "0", "0", "ORDER_WOULD_TAKE")); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, "u1", post.ID); got.RejectReason != "ORDER_WOULD_TAKE" || led.releases[len(led.releases)-1] != "release:"+post.ID+" 60 USDT" {
		t.Fatalf("rejected: %+v %v", got, led.releases)
	}
	_ = store
}

func TestReleasesAreRetriedAfterALedgerOutage(t *testing.T) {
	svc, _, led, c := newService()
	ctx := context.Background()
	o, _ := svc.Place(ctx, buy("c1"))
	led.unfreezeErr = apperr.Unavailable(errors.New("down"))
	if err := svc.OnUpdate(ctx, update(o.ID, 4, domain.StatusCanceled, "0", "0", "USER")); err == nil {
		t.Fatal("the failed release must surface, so the event is retried")
	}
	if got, _ := svc.Get(ctx, "u1", o.ID); got.Status != domain.StatusCanceled || got.Released {
		t.Fatalf("the update stays; the release is pending: %+v", got)
	}
	led.unfreezeErr = nil
	c.t = c.t.Add(11 * time.Second)
	if n, err := svc.RecoverReleases(ctx); err != nil || n != 1 || len(led.releases) != 1 {
		t.Fatalf("recover: %d %v %v", n, err, led.releases)
	}
	if n, _ := svc.RecoverReleases(ctx); n != 0 {
		t.Fatal("released once")
	}
}

func TestFills(t *testing.T) {
	svc, _, _, c := newService()
	ctx := context.Background()
	o, _ := svc.Place(ctx, buy("c1"))
	trade := uuid.NewString()
	f := domain.Fill{
		TradeID: trade, OrderID: o.ID, UserID: "u1", Symbol: "BTC-USDT", Side: domain.SideBuy, Price: d("59990"),
		Quantity: d("0.0004"), Quote: d("23.996"), FeeAsset: "BTC", Fee: d("0.0000008"), Seq: 6, ExecutedAt: c.t,
	}
	for range 2 { // a redelivery is ignored
		if err := svc.OnFill(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	list, err := svc.Fills(ctx, "u1", o.ID)
	if err != nil || len(list) != 1 || list[0].TradeID != trade {
		t.Fatalf("order fills: %v %v", list, err)
	}
	if _, err := svc.Fills(ctx, "u2", o.ID); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Fatalf("someone else's order: %v", err)
	}
	page, next, err := svc.UserFills(ctx, "u1", "", "", 10)
	if err != nil || len(page) != 1 || next != "" {
		t.Fatalf("user fills: %v %q %v", page, next, err)
	}
	if _, _, err := svc.UserFills(ctx, "u1", "", "not-a-trade", 10); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("bad cursor: %v", err)
	}
}

// An order on a margin account that borrowed for its freeze repays, once
// its trades had repayGrace to settle, what came back of the freeze, up to
// what it borrowed (B160): a market buy by quantity also what its
// protection price held beyond the fills' prices. RecoverReleases does it
// and marks the order released, once. A limit buy filled whole keeps its
// borrow (its price improvement freed at settlement stays, as the margin
// e2e has it); a canceled one repays its unused freeze, up to its borrow.
func TestABorrowingOrderRepaysWhatCameBackAsItEnds(t *testing.T) {
	svc, _, led, c := newService()
	ctx := context.Background()
	svc.Margin = &fakeMargin{borrowed: "50"}
	svc.Features = switches{flags.KeyMarginEnabled: true, flags.KeyMarginAutoBorrow: true}
	svc.Prices = fixedAnchor{d("60000")}
	place := func(id string, market bool) domain.Order {
		r := marginBuy(id, domain.AccountMarginCross, domain.SideEffectAutoBorrow)
		if market {
			r.Type, r.Price = domain.TypeMarket, decimal.Zero
		}
		o, err := svc.Place(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	byQty := place("by-qty", true) // 0.001 at the protection 66000: 66 frozen, 50 of it borrowed
	// Filled at 60000: 60 spent, 6 back at settlement, nothing to unfreeze.
	if err := svc.OnUpdate(ctx, update(byQty.ID, 3, domain.StatusFilled, "0.001", "60", "")); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, "u1", byQty.ID); len(led.repays) != 0 || got.Released {
		t.Fatalf("repaid or released before its trades had time to settle: %v %v", led.repays, got.Released)
	}
	whole := place("whole", false) // a limit buy at 60000: 60 frozen
	if err := svc.OnUpdate(ctx, update(whole.ID, 4, domain.StatusFilled, "0.001", "59", "")); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, "u1", whole.ID); !got.Released || len(led.repays) != 0 {
		t.Fatalf("a limit buy filled whole: released %v, repays %v", got.Released, led.repays)
	}
	canceled := place("canceled", false)
	if err := svc.OnUpdate(ctx, update(canceled.ID, 5, domain.StatusCanceled, "0", "0", "USER")); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(repayGrace + time.Second)
	if n, err := svc.RecoverReleases(ctx); err != nil || n != 2 {
		t.Fatalf("recover: %d %v", n, err)
	}
	sort.Strings(led.repays)
	want := []string{"MARGIN_CROSS " + byQty.ID + " 6 USDT", "MARGIN_CROSS " + canceled.ID + " 50 USDT"}
	sort.Strings(want)
	if len(led.repays) != 2 || led.repays[0] != want[0] || led.repays[1] != want[1] {
		t.Fatalf("repays %v, want %v", led.repays, want)
	}
	for _, o := range []domain.Order{byQty, canceled} {
		if got, _ := svc.Get(ctx, "u1", o.ID); !got.Released {
			t.Fatalf("%s not released", o.ClientOrderID)
		}
	}
	if n, err := svc.RecoverReleases(ctx); err != nil || n != 0 || len(led.repays) != 2 {
		t.Fatalf("again: %d %v %v", n, err, led.repays)
	}
}

// A repayment waits for its trades' settlement (B163): while the ledger
// has settled less than the order filled it refuses, the order stays
// unreleased and the next pass tries again, the passes going on past it
// to the other orders; no failure within repayQuiet of the order's end
// (B164). An hour after the order ended the trades the ledger parked as
// FAILED no longer hold it; the ones it has not recorded still do.
func TestARepaymentWaitsForItsTradesSettlement(t *testing.T) {
	svc, _, led, c := newService()
	ctx := context.Background()
	svc.Margin = &fakeMargin{borrowed: "50"}
	svc.Features = switches{flags.KeyMarginEnabled: true, flags.KeyMarginAutoBorrow: true}
	svc.Prices = fixedAnchor{d("60000")}
	nothing := decimal.Zero
	led.settled = &nothing
	place := func(id string, market bool) domain.Order {
		r := marginBuy(id, domain.AccountMarginCross, domain.SideEffectAutoBorrow)
		if market {
			r.Type, r.Price = domain.TypeMarket, decimal.Zero
		}
		o, err := svc.Place(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	byQty := place("by-qty", true)
	if err := svc.OnUpdate(ctx, update(byQty.ID, 3, domain.StatusFilled, "0.001", "60", "")); err != nil {
		t.Fatal(err)
	}
	canceled := place("canceled", false) // nothing filled: nothing to wait for
	if err := svc.OnUpdate(ctx, update(canceled.ID, 4, domain.StatusCanceled, "0", "0", "USER")); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(repayGrace + time.Second)
	if n, err := svc.RecoverReleases(ctx); n != 1 || err != nil {
		t.Fatalf("first pass: %d %v", n, err)
	}
	if got, _ := svc.Get(ctx, "u1", byQty.ID); got.Released || len(led.repays) != 1 || led.repays[0] != "MARGIN_CROSS "+canceled.ID+" 50 USDT" {
		t.Fatalf("first pass: released %v, repays %v", got.Released, led.repays)
	}
	// Settled: the next pass repays it.
	all := d("0.001")
	led.settled = &all
	if n, err := svc.RecoverReleases(ctx); err != nil || n != 1 || len(led.repays) != 2 || led.repays[1] != "MARGIN_CROSS "+byQty.ID+" 6 USDT" {
		t.Fatalf("settled: %d %v %v", n, err, led.repays)
	}

	// One whose trades do not settle: waiting is a failure past repayQuiet,
	// and goes on an hour on while the ledger has not recorded them.
	stuck := place("stuck", true)
	if err := svc.OnUpdate(ctx, update(stuck.ID, 5, domain.StatusFilled, "0.001", "60", "")); err != nil {
		t.Fatal(err)
	}
	led.settled = &nothing
	c.t = c.t.Add(repayGrace + time.Second)
	if n, err := svc.RecoverReleases(ctx); n != 0 || err != nil {
		t.Fatalf("still waiting: %d %v", n, err)
	}
	c.t = c.t.Add(repayQuiet)
	if n, err := svc.RecoverReleases(ctx); n != 0 || !apperr.Is(errors.Unwrap(err), "LEDGER_TRADES_UNSETTLED") {
		t.Fatalf("waiting past repayQuiet: %d %v", n, err)
	}
	c.t = c.t.Add(repayWait)
	if n, _ := svc.RecoverReleases(ctx); n != 0 {
		t.Fatalf("not recorded, an hour on: %d", n)
	}
	if last := led.repayCalls[len(led.repayCalls)-1]; last != stuck.ID+" filled 0.001 skip-failed" {
		t.Fatalf("calls %v", led.repayCalls)
	}
	// Parked as FAILED: they no longer hold it.
	led.parked = d("0.001")
	if n, err := svc.RecoverReleases(ctx); err != nil || n != 1 || led.repays[len(led.repays)-1] != "MARGIN_CROSS "+stuck.ID+" 6 USDT" {
		t.Fatalf("parked as FAILED, an hour on: %d %v %v", n, err, led.repays)
	}
	for _, call := range led.repayCalls[:len(led.repayCalls)-2] {
		if strings.HasSuffix(call, "skip-failed") {
			t.Fatalf("skipped the failed trades within the hour: %v", led.repayCalls)
		}
	}
}

// A release that does not complete goes to the back (B164): the next
// pass takes the orders that waited longest since their end or their last
// try first, so ones that keep failing do not starve the rest.
func TestAFailedReleaseGoesToTheBack(t *testing.T) {
	svc, store, led, c := newService()
	ctx := context.Background()
	first, _ := svc.Place(ctx, buy("c1"))
	c.t = c.t.Add(time.Second)
	second, _ := svc.Place(ctx, buy("c2"))
	led.unfreezeErr = apperr.Unavailable(errors.New("ledger down"))
	for i, o := range []domain.Order{first, second} {
		if err := svc.OnUpdate(ctx, update(o.ID, int64(10+i), domain.StatusCanceled, "0", "0", "USER")); err == nil {
			t.Fatal("released with the ledger down")
		}
		c.t = c.t.Add(time.Second)
	}
	c.t = c.t.Add(time.Minute)
	queue := func() []string {
		t.Helper()
		list, err := store.Read().Orders().Unreleased(ctx, c.t, 100)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, o := range list {
			ids = append(ids, o.ClientOrderID)
		}
		return ids
	}
	if got := queue(); !slices.Equal(got, []string{"c1", "c2"}) {
		t.Fatalf("before: %v", got)
	}
	if n, err := svc.RecoverReleases(ctx); n != 0 || err == nil {
		t.Fatalf("ledger down: %d %v", n, err)
	}
	if !store.attempts[first.ID].Equal(c.t) || !store.attempts[second.ID].Equal(c.t) {
		t.Fatalf("attempts %v", store.attempts)
	}
	// Tried in this order, they keep it; the one that fails next goes behind.
	c.t = c.t.Add(time.Second)
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Orders().ReleaseAttempted(ctx, first.ID, c.t) }); err != nil {
		t.Fatal(err)
	}
	if got := queue(); !slices.Equal(got, []string{"c2", "c1"}) {
		t.Fatalf("after: %v", got)
	}
	ended, _ := svc.Get(ctx, "u1", first.ID)
	if n, age, err := svc.Unreleased(ctx); n != 2 || err != nil || age != c.t.Sub(ended.UpdatedAt) {
		t.Fatalf("stats: %d %s %v", n, age, err)
	}
}
