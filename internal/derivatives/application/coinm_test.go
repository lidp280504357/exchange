package application_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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

// Funding on a coin-margined contract (coin-M design §2.2) is paid in the
// coin: contracts x size x |rate| / the settlement mark, the payer's
// rounded up and the receiver's down to 8 decimals; the difference stays
// in the coin's FUNDING_CLEARING.
func TestCoinMarginedFundingIsPaidInTheCoin(t *testing.T) {
	r := setup(t)
	r.svc.Instruments = instruments{coin: true}
	ctx := context.Background()
	r.book.Set(coinPerp.Symbol, d("60000"), time.Now())
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fundIn(alice, "BTC", "0.1")
	r.fundIn(bob, "BTC", "0.1")
	twenty, isolated := int32(20), domain.Isolated
	if _, err := r.svc.UpdateSettings(ctx, bob, coinPerp.Symbol, application.SettingsChange{MarginMode: &isolated, Leverage: &twenty}); err != nil {
		t.Fatal(err)
	}
	long := r.placeOn(t, coinPerp.Symbol, alice, domain.Buy, "60000", "6")
	short := r.placeOn(t, coinPerp.Symbol, bob, domain.Sell, "60000", "6")
	r.trade(t, long, short, "60000")
	margin := r.positionOn(t, coinPerp.Symbol, bob).Margin

	eight := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	r.svc.Now = func() time.Time { return eight.Add(3 * time.Second) }
	if n, err := r.svc.SnapshotFunding(ctx); err != nil || n != 2 {
		t.Fatalf("snapshot: %d %v", n, err)
	}
	r.rates[fmt.Sprintf("%s|%d", coinPerp.Symbol, eight.Unix())] = [2]decimal.Decimal{d("0.0001"), d("60010")}
	if n, err := r.svc.SettleFunding(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	// 6 x 100 x 0.0001 / 60010 = 0.00000099983: the long pays 0.000001
	// from available, the isolated short receives 0.00000099 into its
	// margin.
	if p := r.positionOn(t, coinPerp.Symbol, alice); !p.Funding.Equal(d("-0.000001")) {
		t.Fatalf("alice %+v", p)
	}
	if p := r.positionOn(t, coinPerp.Symbol, bob); !p.Funding.Equal(d("0.00000099")) || !p.Margin.Equal(margin.Add(d("0.00000099"))) {
		t.Fatalf("bob %+v (margin before %s)", p, margin)
	}
	if b := r.ledger.coins["BTC"]; b == nil || !b.funding.Equal(d("0.00000001")) || !r.ledger.funding.IsZero() {
		t.Fatalf("FUNDING_CLEARING in BTC %+v, in USDT %s", b, r.ledger.funding)
	}
	list, _, err := r.svc.FundingPayments(ctx, alice, coinPerp.Symbol, "", 10)
	if err != nil || len(list) != 1 || !list[0].Amount.Equal(d("-0.000001")) || !list[0].Rate.Equal(d("0.0001")) || !list[0].Mark.Equal(d("60010")) {
		t.Fatalf("alice's funding %+v %v", list, err)
	}
	r.svc.Now = time.Now
	r.reconcile(t, "BTC")
}

// coinLiquidationSetup opens Bob's isolated 50x long of 250 contracts
// (25,000 USD) against Alice's cross short, both at 50000: his cost is 0.5
// BTC, his margin 0.01 and his bankruptcy price 25000 / 0.51 = 49019.6078;
// at the first tier (mmr 0.004) he is warned at or below about 49254.9
// (25120 / 0.51) and liquidated at or below about 49215.7 (25100 / 0.51).
// The BTC insurance fund holds 1 BTC.
func coinLiquidationSetup(t *testing.T) (r *rig, alice, bob string) {
	t.Helper()
	r = setup(t)
	r.svc.Instruments = instruments{coin: true}
	ctx := context.Background()
	r.book.Set(coinPerp.Symbol, d("50000"), time.Now())
	r.ledger.coins["BTC"] = &books{insurance: d("1")}
	alice, bob = uuid.NewString(), uuid.NewString()
	r.fundIn(alice, "BTC", "1")
	r.fundIn(bob, "BTC", "0.02")
	fifty, isolated := int32(50), domain.Isolated
	if _, err := r.svc.UpdateSettings(ctx, bob, coinPerp.Symbol, application.SettingsChange{MarginMode: &isolated, Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	long := r.placeOn(t, coinPerp.Symbol, bob, domain.Buy, "50000", "250")
	short := r.placeOn(t, coinPerp.Symbol, alice, domain.Sell, "50000", "250")
	r.trade(t, long, short, "50000")
	if p := r.positionOn(t, coinPerp.Symbol, bob); !p.Margin.Equal(d("0.01")) || !p.EntryCost.Equal(d("0.5")) {
		t.Fatalf("bob %+v", p)
	}
	return r, alice, bob
}

func TestACoinMarginedLiquidationIsCoveredInTheCoin(t *testing.T) {
	r, alice, bob := coinLiquidationSetup(t)
	ctx := context.Background()
	r.monitorOn(t, coinPerp.Symbol, "49240")
	if p := r.positionOn(t, coinPerp.Symbol, bob); p.WarnedAt.IsZero() || p.Liquidating {
		t.Fatalf("warned: %+v", p)
	}
	r.monitorOn(t, coinPerp.Symbol, "49200")
	if p := r.positionOn(t, coinPerp.Symbol, bob); !p.Liquidating {
		t.Fatalf("taken over: %+v", p)
	}
	r.monitorOn(t, coinPerp.Symbol, "49200")
	// A sell 0.5% under the bankruptcy price, on the tick grid.
	liq, ok := r.liquidationOrderOn(t, coinPerp.Symbol, bob)
	if !ok || liq.Side != domain.Sell || !liq.Price.Equal(d("48774.5")) || !liq.Qty.Equal(d("250")) || liq.TimeInForce != domain.IOC {
		t.Fatalf("liquidation order %+v %v", liq, ok)
	}
	// Alice's reduce-only bid at 48800 takes it: the contracts are worth
	// 25000 / 48800 = 0.51229508 BTC there, Bob loses 0.01229508 on his
	// 0.01 of margin; the BTC insurance fund pays the 0.00229508 and the
	// fee is waived.
	bid, err := r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: coinPerp.Symbol, Side: domain.Buy, Type: domain.Limit, Price: d("48800"), Qty: d("250"), ReduceOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.trade(t, bid, liq, "48800")
	if p := r.positionOn(t, coinPerp.Symbol, bob); !p.Flat() || p.Liquidating {
		t.Fatalf("bob after the liquidation %+v", p)
	}
	fills, _, err := r.svc.Fills(ctx, bob, "", "", 10)
	if err != nil || !fills[0].Liquidation || !fills[0].RealizedPnL.Equal(d("-0.01229508")) || !fills[0].Insurance.Equal(d("0.00229508")) ||
		!fills[0].Fee.IsZero() {
		t.Fatalf("bob's liquidation fill %+v %v", fills[0], err)
	}
	// Bob keeps what was not his margin: 0.02 − 0.01 − the 0.0001 maker
	// fee. The USDT fund is untouched.
	b := account(bob, "BTC")
	if !r.ledger.coins["BTC"].insurance.Equal(d("0.99770492")) || !r.ledger.insurance.Equal(d("1000000")) ||
		!r.ledger.frozen[b].IsZero() || !r.ledger.available[b].Equal(d("0.0099")) {
		t.Fatalf("insurance %s/%s, bob %s/%s", r.ledger.coins["BTC"].insurance, r.ledger.insurance, r.ledger.available[b], r.ledger.frozen[b])
	}
	r.reconcile(t, "BTC")
}

func TestAnUnfillableCoinMarginedLiquidationIsDeleveraged(t *testing.T) {
	r, alice, bob := coinLiquidationSetup(t)
	ctx := context.Background()
	r.monitorOn(t, coinPerp.Symbol, "49200") // taken over
	for i := range domain.MaxLiquidationAttempts {
		r.monitorOn(t, coinPerp.Symbol, "49200")
		liq, ok := r.liquidationOrderOn(t, coinPerp.Symbol, bob)
		if !ok {
			t.Fatalf("attempt %d: no liquidation order", i+1)
		}
		r.seq++
		if err := r.svc.OnUpdate(ctx, domain.Update{
			OrderID: liq.ID, Seq: r.seq, Status: domain.StatusCanceled, Filled: decimal.Zero, FilledQuote: decimal.Zero, Reason: "IOC",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing filled three times: Alice's short, the only counterparty, is
	// closed against Bob's long at his bankruptcy price on the tick grid,
	// 49019.6, where the contracts are worth 0.51000008 BTC: Bob's loss
	// passes his margin by the rounding, 0.00000008, which the fund pays.
	r.monitorOn(t, coinPerp.Symbol, "49200")
	if p := r.positionOn(t, coinPerp.Symbol, bob); !p.Flat() {
		t.Fatalf("bob %+v", p)
	}
	if p := r.positionOn(t, coinPerp.Symbol, alice); !p.Flat() {
		t.Fatalf("alice %+v", p)
	}
	theirs, _, err := r.svc.Fills(ctx, alice, "", "", 10)
	if err != nil || !theirs[0].RealizedPnL.Equal(d("0.01000008")) || !theirs[0].Fee.IsZero() || !theirs[0].Price.Equal(d("49019.6")) {
		t.Fatalf("alice's deleveraged fill %+v %v", theirs[0], err)
	}
	fills, _, err := r.svc.Fills(ctx, bob, "", "", 10)
	if err != nil || !fills[0].RealizedPnL.Equal(d("-0.01000008")) || !fills[0].Insurance.Equal(d("0.00000008")) || !fills[0].Price.Equal(d("49019.6")) {
		t.Fatalf("bob's deleveraged fill %+v %v", fills[0], err)
	}
	r.reconcile(t, "BTC")
}

// A coin-margined cross account is liquidated on its own (coin-M design
// §2.3, C38): Alice's BTC cross short is taken over and her BTC cross
// order canceled; her USDT cross long and USDT order stay.
func TestACoinMarginedCrossAccountIsLiquidatedAlone(t *testing.T) {
	r := setup(t)
	r.svc.Instruments = instruments{coin: true}
	ctx := context.Background()
	r.book.Set(coinPerp.Symbol, d("50000"), time.Now())
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	r.fundIn(alice, "BTC", "0.0137")
	r.fundIn(bob, "BTC", "1")
	// Her USDT account: a cross long of 0.1 and a bid.
	ul := r.place(t, alice, domain.Buy, "60000", "0.1", false)
	us := r.place(t, bob, domain.Sell, "60000", "0.1", false)
	r.trade(t, ul, us, "60000")
	usdtBid := r.place(t, alice, domain.Buy, "59000", "0.01", false)
	// Her BTC account: a cross 50x short of 250 contracts (25,000 USD) at
	// 50000, 0.01 BTC of margin and a 0.00025 taker fee, and an ask of 10
	// more at 52000.
	fifty := int32(50)
	if _, err := r.svc.UpdateSettings(ctx, alice, coinPerp.Symbol, application.SettingsChange{Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	cl := r.placeOn(t, coinPerp.Symbol, bob, domain.Buy, "50000", "250")
	cs := r.placeOn(t, coinPerp.Symbol, alice, domain.Sell, "50000", "250")
	r.trade(t, cl, cs, "50000")
	coinAsk := r.placeOn(t, coinPerp.Symbol, alice, domain.Sell, "52000", "10")

	// Her BTC equity, 0.01345 with the short's result 25000/x − 0.5, meets
	// the maintenance margin 100/x at x = 24900 / 0.48655 = 51176.7; she is
	// warned from 24880 / 0.48655 = 51135.5.
	r.monitorOn(t, coinPerp.Symbol, "51150")
	if at, err := r.store.Read().Cross().WarnedAt(ctx, alice, "BTC"); err != nil || at.IsZero() {
		t.Fatalf("the BTC cross warning %v %v", at, err)
	}
	if at, err := r.store.Read().Cross().WarnedAt(ctx, alice, "USDT"); err != nil || !at.IsZero() {
		t.Fatalf("the USDT cross account warned %v %v", at, err)
	}
	r.monitorOn(t, coinPerp.Symbol, "51200")
	if p := r.positionOn(t, coinPerp.Symbol, alice); !p.Liquidating {
		t.Fatalf("taken over %+v", p)
	}
	if p := r.position(t, alice); p.Liquidating || !p.Qty.Equal(d("0.1")) {
		t.Fatalf("her USDT long %+v", p)
	}
	if o, err := r.svc.Get(ctx, alice, coinAsk.ID); err != nil || !o.CancelRequested {
		t.Fatalf("her BTC cross ask %+v %v", o, err)
	}
	if o, err := r.svc.Get(ctx, alice, usdtBid.ID); err != nil || o.CancelRequested || o.Status != domain.StatusNew {
		t.Fatalf("her USDT bid %+v %v", o, err)
	}
	r.seq++
	if err := r.svc.OnUpdate(ctx, domain.Update{
		OrderID: coinAsk.ID, Seq: r.seq, Status: domain.StatusCanceled, Filled: decimal.Zero, FilledQuote: decimal.Zero, Reason: "USER",
	}); err != nil {
		t.Fatal(err)
	}
	r.monitorOn(t, coinPerp.Symbol, "51200")
	liq, ok := r.liquidationOrderOn(t, coinPerp.Symbol, alice)
	if !ok || liq.Side != domain.Buy || !liq.Price.Equal(d("51456")) {
		t.Fatalf("liquidation order %+v %v", liq, ok)
	}
	ask, err := r.svc.Place(ctx, domain.Request{
		UserID: bob, Symbol: coinPerp.Symbol, Side: domain.Sell, Type: domain.Limit, Price: d("51300"), Qty: d("250"), ReduceOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.trade(t, ask, liq, "51300")
	if p := r.positionOn(t, coinPerp.Symbol, alice); !p.Flat() {
		t.Fatalf("alice %+v", p)
	}
	// At 51300 the contracts are worth 0.48732943 BTC: 0.01345 − the
	// 0.01267057 loss − the 0.00024367 taker fee.
	a := account(alice, "BTC")
	if !r.ledger.available[a].Equal(d("0.00053576")) || !r.ledger.frozen[a].IsZero() {
		t.Fatalf("alice %s/%s", r.ledger.available[a], r.ledger.frozen[a])
	}
	r.reconcile(t, "BTC")
}
