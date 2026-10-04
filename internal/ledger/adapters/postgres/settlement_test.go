package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/ledger/adapters/postgres"
	"github.com/skill/exchange/internal/ledger/application"
	"github.com/skill/exchange/internal/ledger/domain"
)

// balance returns a user's SPOT balances in asset.
func balance(t *testing.T, svc *application.Service, user, asset string) string {
	t.Helper()
	list, err := svc.Balances(context.Background(), user, domain.AccountSpot)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.Key.Asset == asset {
			return a.Available.String() + " " + a.Frozen.String()
		}
	}
	return "0 0"
}

func fund(t *testing.T, svc *application.Service, user string, credits ...domain.Credit) {
	t.Helper()
	p, err := domain.AdjustmentPosting("fund:"+user, user, credits, "test funds")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Post(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func freeze(t *testing.T, svc *application.Service, key, user, asset, amount string) {
	t.Helper()
	if _, err := svc.Freeze(context.Background(), key, domain.EntryOrderFreeze, user, domain.AccountSpot, asset, d(amount), "order"); err != nil {
		t.Fatal(err)
	}
}

func mismatches(t *testing.T, store *postgres.Store) map[string]int {
	t.Helper()
	results, err := store.Reconcile(context.Background(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range results {
		if len(r.Mismatches) > 0 {
			out[r.Check] = len(r.Mismatches)
		}
	}
	return out
}

func TestSettlement(t *testing.T) {
	svc, store, _ := setup(t)
	ctx := context.Background()
	buyer, seller := uuid.NewString(), uuid.NewString()
	fund(t, svc, buyer, domain.Credit{Asset: "USDT", Amount: d("1000"), Decimals: 6})
	fund(t, svc, seller, domain.Credit{Asset: "BTC", Amount: d("1"), Decimals: 8})
	// The buy froze its limit, 70100 x 0.003; the sell its quantity.
	freeze(t, svc, "order:b", buyer, "USDT", "210.3")
	freeze(t, svc, "order:s", seller, "BTC", "0.002")

	trade := func(n uint64, qty, quote, buyerFee, sellerFee string) domain.Trade {
		return domain.Trade{
			ID: uuid.NewString(), Number: n, Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
			Price: d("70000"), Quantity: d(qty), Quote: d(quote),
			BuyerOrderID: "b", BuyerUserID: buyer, SellerOrderID: "s", SellerUserID: seller,
			BuyerFee: d(buyerFee), SellerFee: d(sellerFee), BuyerLimit: d("70100"),
			EventID: uuid.NewString(), ExecutedAt: time.Now().UTC(),
		}
	}
	first := trade(1, "0.002", "140", "0.000002", "0.14")
	// The seller froze only 0.002: this one cannot be paid for yet.
	second := trade(2, "0.001", "70", "0.000001", "0.07")
	res, err := svc.Settle(ctx, []domain.Trade{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Settled != 1 || res.Failed != 1 || len(res.Refused) != 1 || res.Refused[0].ErrorCode != "LEDGER_INSUFFICIENT_BALANCE" {
		t.Fatalf("settle: %+v", res)
	}
	// Buyer: 140 paid and 0.2 released of the 210.3 frozen (1000 - 210.3 +
	// 0.2 available); 0.002 BTC less
	// the 0.000002 fee. Seller: 140 less the 0.14 fee.
	for _, c := range []struct{ user, asset, want string }{
		{buyer, "USDT", "789.9 70.1"}, {buyer, "BTC", "0.001998 0"}, {seller, "USDT", "139.86 0"}, {seller, "BTC", "0.998 0"},
	} {
		if got := balance(t, svc, c.user, c.asset); got != c.want {
			t.Fatalf("%s %s = %s, want %s", c.user, c.asset, got, c.want)
		}
	}
	if got := mismatches(t, store); len(got) != 1 || got[postgres.CheckTradesSettled] != 1 {
		t.Fatalf("reconciliation: %v, want only the parked trade", got)
	}

	// A redelivery changes nothing, not even the parked trade.
	res, err = svc.Settle(ctx, []domain.Trade{first, second})
	if err != nil || res.Skipped != 2 || res.Settled+res.Failed != 0 {
		t.Fatalf("redelivery: %+v, %v", res, err)
	}
	if got := balance(t, svc, buyer, "USDT"); got != "789.9 70.1" {
		t.Fatalf("after redelivery buyer USDT %s", got)
	}

	// Once the funds are there, a retry settles it.
	freeze(t, svc, "order:s2", seller, "BTC", "0.001")
	res, err = svc.RetryFailed(ctx, 10)
	if err != nil || res.Settled != 1 {
		t.Fatalf("retry: %+v, %v", res, err)
	}
	if got := balance(t, svc, buyer, "USDT"); got != "790 0" {
		t.Fatalf("after retry buyer USDT %s, want the rest of the freeze used", got)
	}
	list, err := svc.Trades(ctx, domain.TradeSettled, 10)
	if err != nil || len(list) != 2 || list[0].Attempts+list[1].Attempts != 3 {
		t.Fatalf("trades: %+v, %v", list, err)
	}
	if got := mismatches(t, store); len(got) != 0 {
		t.Fatalf("reconciliation after the retry: %v", got)
	}

	// A trade the ledger never saw leaves a gap in the numbers.
	freeze(t, svc, "order:b2", buyer, "USDT", "70.1")
	freeze(t, svc, "order:s3", seller, "BTC", "0.001")
	if _, err := svc.Settle(ctx, []domain.Trade{trade(4, "0.001", "70", "0.000001", "0.07")}); err != nil {
		t.Fatal(err)
	}
	if got := mismatches(t, store); len(got) != 1 || got[postgres.CheckTradesNumbered] != 1 {
		t.Fatalf("reconciliation with a gap: %v", got)
	}
	// Journals without trades show up in invariant 5.
	p, err := domain.SettlementPostings(trade(0, "0.001", "70", "0", "0"))
	if err != nil {
		t.Fatal(err)
	}
	freeze(t, svc, "order:b3", buyer, "USDT", "70")
	freeze(t, svc, "order:s4", seller, "BTC", "0.001")
	if _, err := svc.Post(ctx, p[0]); err != nil {
		t.Fatal(err)
	}
	if got := mismatches(t, store); got[postgres.CheckTradeSettleMatches] != 2 {
		t.Fatalf("reconciliation with a stray TRADE_SETTLE: %v", got)
	}
}
