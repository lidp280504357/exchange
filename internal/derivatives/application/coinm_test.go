package application_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// events returns the events emitted so far in memory; nil on PostgreSQL,
// whose go to the outbox.
func (r *rig) events() []proto.Message {
	m, ok := r.store.(*memStore)
	if !ok {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.events)
}

// placeOn places a limit order on a contract.
func (r *rig) placeOn(t *testing.T, symbol, user string, side domain.Side, price, qty string) domain.Order {
	t.Helper()
	o, err := r.svc.Place(context.Background(), domain.Request{
		UserID: user, Symbol: symbol, Side: side, Type: domain.Limit, Price: d(price), Qty: d(qty),
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// A coin-margined contract (coin-M design §2, batch G1): whole contracts,
// derivatives.coin_m on top of contract trading, a FUTURES account per
// settlement asset (BTC here, the USDT one untouched), the inverse
// formulas end to end through a fill and a close, the books exact in BTC
// (invariant 6 per asset).
func TestCoinMarginedContracts(t *testing.T) {
	r := setup(t)
	r.svc.Instruments = instruments{coin: true}
	ctx := context.Background()
	r.book.Set(coinPerp.Symbol, d("60000"), time.Now())
	alice, bob := uuid.NewString(), uuid.NewString()
	r.ledger.available[alice+"|BTC"], r.ledger.available[bob+"|BTC"] = d("0.1"), d("0.1")
	r.fund(alice, "1000")

	// Whole contracts only.
	_, err := r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: coinPerp.Symbol, Side: domain.Buy, Type: domain.Limit, Price: d("60000"), Qty: d("1.5"),
	})
	if apperr.From(err).Code != "DERIV_CONTRACTS_NOT_INTEGER" {
		t.Fatalf("1.5 contracts: %v", err)
	}
	// derivatives.coin_m closed to the account: not eligible.
	r.eligible.deny[application.FeatureCoinM] = true
	_, err = r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: coinPerp.Symbol, Side: domain.Buy, Type: domain.Limit, Price: d("60000"), Qty: d("1"),
	})
	if apperr.From(err).Code != "USER_NOT_ELIGIBLE" {
		t.Fatalf("coin_m closed: %v", err)
	}
	r.eligible.deny[application.FeatureCoinM] = false

	// A buy reserves at min(price, mark) in BTC, per contract rounded up:
	// 100 / 60000 / 20 = 0.00008334 of margin and 0.00000084 of taker fee,
	// 0.00050508 for 6.
	buy := r.placeOn(t, coinPerp.Symbol, alice, domain.Buy, "60000", "6")
	sell := r.placeOn(t, coinPerp.Symbol, bob, domain.Sell, "60000", "6")
	btc, err := r.svc.Account(ctx, alice, "BTC")
	if err != nil || !btc.OrderMargin.Equal(d("0.00050508")) || !btc.Frozen.Equal(d("0.00050508")) {
		t.Fatalf("alice's BTC account %+v %v", btc, err)
	}
	usdt, err := r.svc.Account(ctx, alice, "USDT")
	if err != nil || !usdt.OrderMargin.IsZero() || !usdt.Available.Equal(d("1000")) {
		t.Fatalf("alice's USDT account %+v %v", usdt, err)
	}
	if _, err := r.svc.Account(ctx, alice, "DOGE"); apperr.From(err).Code != "DERIV_SETTLE_ASSET_MISMATCH" {
		t.Fatalf("an asset no contract settles in: %v", err)
	}

	r.trade(t, sell, buy, "60000")
	views, err := r.svc.Positions(ctx, alice, coinPerp.Symbol)
	if err != nil || len(views) != 1 {
		t.Fatalf("positions %+v %v", views, err)
	}
	v := views[0]
	// 6 contracts of 100 USD at 60000 cost 0.01 BTC.
	if v.Settle != "BTC" || !v.ContractSize.Equal(d("100")) || !v.Qty.Equal(d("6")) || !v.EntryCost.Equal(d("0.01")) ||
		!v.Entry.Equal(d("60000")) || !v.Value.Equal(d("0.01")) {
		t.Fatalf("alice's position %+v", v)
	}
	r.reconcile(t, "BTC")

	// The mark falls to 50000: the long loses 600/50000 - 0.01 = 0.002 BTC.
	r.book.Set(coinPerp.Symbol, d("50000"), time.Now())
	views, _ = r.svc.Positions(ctx, alice, coinPerp.Symbol)
	if u := views[0].UnrealizedPnL; !u.Equal(d("-0.002")) {
		t.Fatalf("unrealized at 50000: %s", u)
	}

	// A buy below the mark reserves at its own price, where the contracts
	// are worth more coin: 2 at 49000, per contract 100 / 49000 / 20 and
	// its taker fee, rounded up: 0.00010205 + 0.00000103.
	before, _ := r.svc.Account(ctx, alice, "BTC")
	r.placeOn(t, coinPerp.Symbol, alice, domain.Buy, "49000", "2")
	after, _ := r.svc.Account(ctx, alice, "BTC")
	if got := after.OrderMargin.Sub(before.OrderMargin); !got.Equal(d("0.00020616")) {
		t.Fatalf("2 contracts at 49000 reserve %s", got)
	}

	// Closed at 50000: alice realizes the loss in BTC, bob the gain.
	r.book.Set(coinPerp.Symbol, d("50000"), time.Now())
	closeA := r.placeOn(t, coinPerp.Symbol, alice, domain.Sell, "50000", "6")
	closeB := r.placeOn(t, coinPerp.Symbol, bob, domain.Buy, "50000", "6")
	r.trade(t, closeA, closeB, "50000")
	if p, _ := r.svc.Positions(ctx, alice, coinPerp.Symbol); len(p) != 0 {
		t.Fatalf("alice's position left %+v", p)
	}
	a, _ := r.svc.Account(ctx, alice, "BTC")
	b, _ := r.svc.Account(ctx, bob, "BTC")
	// Alice: 0.1 - 0.002 lost - her fees (taker 0.0005 x 0.01 at the open,
	// maker 0.0002 x 0.012 at the close); bob: 0.1 + 0.002 - his (maker
	// 0.0002 x 0.01, taker 0.0005 x 0.012), each rounded up to 8 decimals.
	if !a.WalletBalance().Equal(d("0.0979926")) || !b.WalletBalance().Equal(d("0.101992")) {
		t.Fatalf("after the close: alice %s, bob %s", a.WalletBalance(), b.WalletBalance())
	}
	if pnl, _ := r.ledger.PnLClearing(ctx, "BTC"); !pnl.IsZero() {
		t.Fatalf("PNL_CLEARING in BTC %s", pnl)
	}
	r.reconcile(t, "BTC")

	// The events carry the settlement asset and the contract size.
	events := r.events()
	if events == nil {
		return // on PostgreSQL they are in the outbox
	}
	var fill *derivativesv1.FillSettled
	var pos *derivativesv1.Position
	for _, e := range events {
		switch m := e.(type) {
		case *derivativesv1.FillSettled:
			fill = m
		case *derivativesv1.PositionOpened:
			pos = m.GetPosition()
		}
	}
	if fill == nil || fill.GetSettleAsset() != "BTC" || fill.GetContractSize() != "100" || pos == nil || pos.GetSettleAsset() != "BTC" {
		t.Fatalf("events: fill %v, position %v", fill, pos)
	}
}
