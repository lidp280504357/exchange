package application

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/trading/domain"
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

func (r memOrders) Unreleased(_ context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range r.s.orders {
		if !o.Released && o.FreezeState == domain.FreezeDone && o.Status.Terminal() && o.UpdatedAt.Before(cutoff) {
			out = append(out, o)
		}
	}
	return out[:min(len(out), limit)], nil
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
