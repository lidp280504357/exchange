package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/ledger/domain"
)

// TestMarginTradeSettlement settles orders on margin accounts against
// HOUSE (margin design §5.1): each side on its account's asset rows
// (MARGIN_TRADE_SETTLE), and AUTO_REPAY's debt repaid from what it got in
// the same transaction; the trade record keeps the accounts.
func TestMarginTradeSettlement(t *testing.T) {
	svc, store, _ := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), user, "SG"); err != nil {
		t.Fatal(err)
	}
	house, err := domain.HouseAdjustmentPosting("house:fund", []domain.Credit{
		{Asset: "BTC", Amount: d("10"), Decimals: 8}, {Asset: "USDT", Amount: d("1000000"), Decimals: 6},
	}, "test funds")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Post(ctx, house); err != nil {
		t.Fatal(err)
	}
	cross := domain.MarginRef{UserID: user, AccountType: domain.AccountMarginCross}
	iso := domain.MarginRef{UserID: user, AccountType: domain.AccountMarginIsolated, Scope: "BTC-USDT"}
	post := func(key string, a domain.MarginRef, moves ...domain.MarginMove) {
		t.Helper()
		if _, err := svc.PostMargin(ctx, domain.MarginRequest{IdemKey: key, Account: a, Reference: key, Moves: moves}); err != nil {
			t.Fatal(err)
		}
	}
	// Cross: 1000 USDT in, 0.005 BTC borrowed; a buy of 0.01 BTC at 30000
	// with AUTO_REPAY froze 300 USDT.
	post("c-in", cross, domain.MarginMove{Type: domain.MarginTransferIn, Asset: "USDT", Amount: d("1000")})
	post("c-b", cross, domain.MarginMove{Type: domain.MarginBorrow, Asset: "BTC", Amount: d("0.005")})
	if _, err := svc.FreezeScoped(ctx, "order:c", domain.EntryOrderFreeze, user, domain.AccountMarginCross, "", "USDT", d("300"), "c"); err != nil {
		t.Fatal(err)
	}
	// Isolated BTC-USDT: 0.05 BTC in, 500 USDT borrowed; a sell of 0.02 BTC
	// at 30000 with AUTO_REPAY froze 0.02 BTC (the welcome funds hold 0.1).
	post("i-in", iso, domain.MarginMove{Type: domain.MarginTransferIn, Asset: "BTC", Amount: d("0.05")})
	post("i-b", iso, domain.MarginMove{Type: domain.MarginBorrow, Asset: "USDT", Amount: d("500")})
	if _, err := svc.FreezeScoped(ctx, "order:i", domain.EntryOrderFreeze, user, domain.AccountMarginIsolated, "BTC-USDT", "BTC", d("0.02"), "i"); err != nil {
		t.Fatal(err)
	}
	trade := func(id string, qty, quote string) domain.Trade {
		return domain.Trade{
			ID: id, Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", Price: d("30000"), Quantity: d(qty), Quote: d(quote),
			EventID: uuid.NewString(), ExecutedAt: time.Now().UTC(), BuyerFee: d("0"), SellerFee: d("0"),
		}
	}
	buy := trade(uuid.NewString(), "0.01", "300")
	buy.HouseSide, buy.BuyerOrderID, buy.BuyerUserID, buy.SellerUserID = domain.HouseSell, "c", user, "HOUSE"
	buy.BuyerFee, buy.BuyerAccount, buy.BuyerAutoRepay = d("0.00001"), domain.AccountMarginCross, true
	sell := trade(uuid.NewString(), "0.02", "600")
	sell.HouseSide, sell.SellerOrderID, sell.SellerUserID, sell.BuyerUserID = domain.HouseBuy, "i", user, "HOUSE"
	sell.SellerFee, sell.SellerAccount, sell.SellerAutoRepay = d("0.6"), domain.AccountMarginIsolated, true
	res, err := svc.Settle(ctx, []domain.Trade{buy, sell})
	if err != nil || res.Settled != 2 {
		t.Fatalf("settle %+v %v", res, err)
	}
	list, err := svc.MarginBalances(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[domain.AccountKey][2]string{
		// 0.005 borrowed + 0.01 bought - 0.00001 fee - 0.005 repaid.
		cross.Assets("BTC"): {"0.00999", "0"}, cross.DebtRow("BTC"): {"0", "0"},
		cross.Assets("USDT"): {"700", "0"},
		// 500 borrowed + 600 - 0.6 fee - 500 repaid; 0.03 BTC left.
		iso.Assets("USDT"): {"599.4", "0"}, iso.DebtRow("USDT"): {"0", "0"},
		iso.Assets("BTC"): {"0.03", "0"},
	} {
		a := marginRow(t, list, k)
		if !a.Available.Equal(d(want[0])) || !a.Frozen.Equal(d(want[1])) {
			t.Errorf("%s: %s/%s, want %s/%s", k, a.Available, a.Frozen, want[0], want[1])
		}
	}
	recorded, err := svc.Trades(ctx, domain.TradeSettled, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recorded {
		if r.ID == buy.ID && (r.BuyerAccount != domain.AccountMarginCross || !r.BuyerAutoRepay) {
			t.Errorf("the buy's record %+v", r)
		}
	}
	if m := mismatches(t, store); len(m) > 0 {
		t.Fatalf("reconciliation: %v", m)
	}
}
