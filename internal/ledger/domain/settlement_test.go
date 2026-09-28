package domain

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// buyTrade: the buyer's limit 70100 order takes 0.002 BTC at 70000; the
// buyer (taker) pays 0.000002 BTC, the seller (maker) 0.14 USDT.
func buyTrade() Trade {
	return Trade{
		ID: "t1", Number: 7, Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		Price: dec("70000"), Quantity: dec("0.002"), Quote: dec("140"),
		BuyerOrderID: "b", BuyerUserID: "buyer", SellerOrderID: "s", SellerUserID: "seller",
		BuyerFee: dec("0.000002"), SellerFee: dec("0.14"), BuyerLimit: dec("70100"), EventID: "e1",
	}
}

func lines(p Posting) map[string]string {
	sums := map[string]decimal.Decimal{}
	for _, l := range p.Lines {
		key := l.Account.OwnerID + "/" + l.Account.Type + "/" + l.Account.Asset + "/" + l.Kind
		sums[key] = sums[key].Add(l.Amount)
	}
	out := make(map[string]string, len(sums))
	for k, v := range sums {
		out[k] = v.String()
	}
	return out
}

func TestSettlementPostings(t *testing.T) {
	ps, err := SettlementPostings(buyTrade())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || ps[0].EntryType != EntryTradeSettle || ps[1].EntryType != EntryTradeFee || ps[2].EntryType != EntryOrderUnfreeze {
		t.Fatalf("postings: %+v", ps)
	}
	want := []map[string]string{
		{ // the traded amounts only (invariant 5)
			"buyer/SPOT/USDT/FROZEN": "-140", "seller/SPOT/USDT/AVAILABLE": "140",
			"seller/SPOT/BTC/FROZEN": "-0.002", "buyer/SPOT/BTC/AVAILABLE": "0.002",
		},
		{ // fees from what each side received
			"buyer/SPOT/BTC/AVAILABLE": "-0.000002", "SYSTEM/FEE_REVENUE/BTC/AVAILABLE": "0.000002",
			"seller/SPOT/USDT/AVAILABLE": "-0.14", "SYSTEM/FEE_REVENUE/USDT/AVAILABLE": "0.14",
		},
		{ // (70100 - 70000) x 0.002 back to the buyer
			"buyer/SPOT/USDT/FROZEN": "-0.2", "buyer/SPOT/USDT/AVAILABLE": "0.2",
		},
	}
	for i, p := range ps {
		got := lines(p)
		if len(got) != len(want[i]) {
			t.Fatalf("%s lines %v, want %v", p.EntryType, got, want[i])
		}
		for k, v := range want[i] {
			if got[k] != v {
				t.Fatalf("%s line %s = %s, want %s (all %v)", p.EntryType, k, got[k], v, got)
			}
		}
	}
	if ps[0].IdemKey != "trade:t1" || ps[1].IdemKey != "trade-fee:t1" || ps[2].IdemKey != "trade-release:t1" || ps[0].SourceEventID != "e1" {
		t.Fatalf("keys %s %s %s", ps[0].IdemKey, ps[1].IdemKey, ps[2].IdemKey)
	}
}

func TestSettlementPostingsLeaveOutWhatIsZero(t *testing.T) {
	tr := buyTrade()
	tr.BuyerLimit = decimal.Zero // a market buy: nothing to release
	tr.BuyerFee = decimal.Zero
	ps, err := SettlementPostings(tr)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || len(ps[1].Lines) != 2 || ps[1].Lines[1].Account.Asset != "USDT" {
		t.Fatalf("postings: %+v", ps)
	}
	tr.SellerFee = decimal.Zero
	tr.BuyerLimit = tr.Price // a limit buy filled at its limit
	if ps, err := SettlementPostings(tr); err != nil || len(ps) != 1 {
		t.Fatalf("postings %+v, %v", ps, err)
	}
}

func TestInvalidTradesAreRefused(t *testing.T) {
	for name, change := range map[string]func(*Trade){
		"self trade":        func(t *Trade) { t.SellerUserID = t.BuyerUserID },
		"same assets":       func(t *Trade) { t.QuoteAsset = t.BaseAsset },
		"quote off":         func(t *Trade) { t.Quote = dec("140.01") },
		"zero price":        func(t *Trade) { t.Price, t.Quote = decimal.Zero, decimal.Zero },
		"buyer fee too big": func(t *Trade) { t.BuyerFee = t.Quantity },
		"negative fee":      func(t *Trade) { t.SellerFee = dec("-1") },
		"limit below price": func(t *Trade) { t.BuyerLimit = dec("69999") },
		"no id":             func(t *Trade) { t.ID = "" },
	} {
		tr := buyTrade()
		change(&tr)
		_, err := SettlementPostings(tr)
		if !apperr.Is(err, "LEDGER_INVALID_TRADE") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSimulateSettlesWholeOrNothing(t *testing.T) {
	ps, err := SettlementPostings(buyTrade())
	if err != nil {
		t.Fatal(err)
	}
	accounts := func(buyerFrozen string) []Account {
		var out []Account
		for _, k := range PostingAccounts(ps) {
			a := Account{Key: k}
			switch {
			case k.OwnerID == "buyer" && k.Asset == "USDT":
				a.Frozen = dec(buyerFrozen)
			case k.OwnerID == "seller" && k.Asset == "BTC":
				a.Frozen = dec("0.002")
			}
			out = append(out, a)
		}
		return out
	}
	// The order froze 70100 x 0.002 = 140.2.
	if err := Simulate(accounts("140.2"), ps); err != nil {
		t.Fatal(err)
	}
	// 140 would pay for the trade but not the release: refused as a whole.
	if err := Simulate(accounts("140"), ps); !errors.Is(err, ErrInsufficientBalance) && !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("simulate: %v", err)
	}
	if err := Simulate(accounts("140.2")[:2], ps); err == nil {
		t.Fatal("an account that was not loaded must fail the simulation")
	}
}
