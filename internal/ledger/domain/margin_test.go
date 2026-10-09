package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

func TestMarginAccountKeys(t *testing.T) {
	user := uuid.NewString()
	cross := MarginRef{UserID: user, AccountType: AccountMarginCross}
	iso := MarginRef{UserID: user, AccountType: AccountMarginIsolated, Scope: "BTC-USDT"}
	for _, k := range []AccountKey{
		cross.Assets("USDT"), cross.DebtRow("USDT"), cross.InterestRow("USDT"), iso.Assets("BTC"),
		iso.DebtRow("BTC"), SystemAccount(AccountMarginInterestIncome, "USDT"),
	} {
		if err := k.Validate(); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	for _, k := range []AccountKey{
		{OwnerType: OwnerUser, OwnerID: user, Type: AccountMarginIsolated, Asset: "BTC"},                            // no pair
		{OwnerType: OwnerUser, OwnerID: user, Type: AccountMarginCross, Scope: "BTC-USDT", Asset: "BTC"},            // a pair on cross
		{OwnerType: OwnerUser, OwnerID: user, Type: AccountSpot, Scope: "BTC-USDT", Asset: "BTC"},                   // a pair on SPOT
		{OwnerType: OwnerSystem, OwnerID: OwnerSystem, Type: AccountMarginCross, Asset: "BTC"},                      // a system margin row
		{OwnerType: OwnerUser, OwnerID: user, Type: AccountMarginInterestIncome, Asset: "BTC"},                      // a user's income
		{OwnerType: OwnerSystem, OwnerID: OwnerSystem, Type: AccountMarginInterestIncome, Scope: "X", Asset: "BTC"}, // scoped income
	} {
		if k.Validate() == nil {
			t.Errorf("%+v passed", k)
		}
	}
	if s := iso.DebtRow("BTC").String(); s != "USER/"+user+"/MARGIN_ISOLATED_DEBT:BTC-USDT/BTC" {
		t.Errorf("name %s", s)
	}
	if s := UserAccount(user, AccountSpot, "BTC").String(); s != "USER/"+user+"/SPOT/BTC" {
		t.Errorf("an unscoped name changed: %s", s)
	}
	if !cross.DebtRow("USDT").Debt() || !iso.InterestRow("BTC").Debt() || cross.Assets("USDT").Debt() ||
		SystemAccount(AccountMarginInterestIncome, "USDT").Debt() {
		t.Error("debt rows")
	}
}

func TestDebtRowsNeverGoAboveZero(t *testing.T) {
	debt := Account{Key: MarginRef{UserID: uuid.NewString(), AccountType: AccountMarginCross}.DebtRow("USDT"), Available: d("-100")}
	if err := debt.Apply(Line{Account: debt.Key, Amount: d("-50"), Kind: Available}); err != nil || !debt.Available.Equal(d("-150")) {
		t.Fatalf("borrow more: %v %s", err, debt.Available)
	}
	if err := debt.Apply(Line{Account: debt.Key, Amount: d("150.000001"), Kind: Available}); !apperr.Is(err, "LEDGER_DEBT_OVERPAID") {
		t.Fatalf("overpaid: %v", err)
	}
	if err := debt.Apply(Line{Account: debt.Key, Amount: d("1"), Kind: Frozen}); err == nil {
		t.Fatal("froze a debt")
	}
	if err := debt.Apply(Line{Account: debt.Key, Amount: d("150"), Kind: Available}); err != nil || !debt.Available.IsZero() {
		t.Fatalf("repaid in full: %v %s", err, debt.Available)
	}
}

func accountsOf(keys []AccountKey, set map[AccountKey]decimal.Decimal) []Account {
	out := make([]Account, 0, len(keys))
	for _, k := range keys {
		out = append(out, Account{Key: k, Available: set[k]})
	}
	return out
}

func TestMarginPostings(t *testing.T) {
	user := uuid.NewString()
	cross := MarginRef{UserID: user, AccountType: AccountMarginCross}
	spot := UserAccount(user, AccountSpot, "USDT")
	req := MarginRequest{IdemKey: "k", Account: cross, Reference: "r", Moves: []MarginMove{
		{Type: MarginTransferIn, Asset: "USDT", Amount: d("100")},
		{Type: MarginBorrow, Asset: "USDT", Amount: d("200")},
		{Type: MarginInterest, Asset: "USDT", Amount: d("0.002")},
		{Type: MarginRepay, Asset: "USDT", Amount: d("50"), Interest: d("0.002")},
		{Type: MarginTransferOut, Asset: "USDT", Amount: d("99.998")},
	}}
	state := map[AccountKey]decimal.Decimal{spot: d("100")}
	postings, err := MarginPostings(req, accountsOf(req.MarginAccounts(), state))
	if err != nil || len(postings) != 5 {
		t.Fatalf("%d postings, %v", len(postings), err)
	}
	for i, want := range []string{EntryMarginTransferIn, EntryMarginBorrow, EntryMarginInterest, EntryMarginRepay, EntryMarginTransferOut} {
		if postings[i].EntryType != want || postings[i].Validate() != nil {
			t.Errorf("posting %d: %s %v", i, postings[i].EntryType, postings[i].Validate())
		}
	}
	// 100 in, 200 borrowed, 50 repaid: 250 held against 150 owed; out
	// 99.998 leaves 150.002, one 0.002 short of nothing: what the debt holds.
	req.Moves[4].Amount = d("100.002")
	if _, err := MarginPostings(req, accountsOf(req.MarginAccounts(), state)); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("out of what the debt holds: %v", err)
	}
	req.Moves = req.Moves[:2]
	req.Moves[0].Amount = d("100.01")
	if _, err := MarginPostings(req, accountsOf(req.MarginAccounts(), state)); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("more in than SPOT holds: %v", err)
	}
	for name, bad := range map[string]MarginMove{
		"unknown":          {Type: "GIFT", Asset: "USDT", Amount: d("1")},
		"no amount":        {Type: MarginBorrow, Asset: "USDT", Amount: d("0")},
		"interest part":    {Type: MarginBorrow, Asset: "USDT", Amount: d("1"), Interest: d("0.1")},
		"interest too big": {Type: MarginRepay, Asset: "USDT", Amount: d("1"), Interest: d("2")},
	} {
		r := MarginRequest{IdemKey: "k", Account: cross, Moves: []MarginMove{bad}}
		if r.Validate() == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func TestInterestPosting(t *testing.T) {
	a := MarginRef{UserID: uuid.NewString(), AccountType: AccountMarginCross}
	b := MarginRef{UserID: uuid.NewString(), AccountType: AccountMarginIsolated, Scope: "BTC-USDT"}
	p, err := InterestRequest{IdemKey: "USDT:1", Asset: "USDT", Lines: []InterestLine{{Account: a, Amount: d("0.01")}, {Account: b, Amount: d("0.02")}}}.Posting()
	if err != nil || p.Validate() != nil || len(p.Lines) != 3 || !p.Lines[2].Amount.Equal(d("0.03")) || p.IdemKey != "margin-interest:USDT:1" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := (InterestRequest{IdemKey: "k", Asset: "USDT", Lines: []InterestLine{{Account: a, Amount: d("1")}, {Account: a, Amount: d("1")}}}).Posting(); err == nil {
		t.Fatal("an account twice")
	}
}

func TestMarginTradeSettlement(t *testing.T) {
	user := uuid.NewString()
	iso := MarginRef{UserID: user, AccountType: AccountMarginIsolated, Scope: "BTC-USDT"}
	// A margin buy against HOUSE that repays its BTC debt with what it gets.
	trade := Trade{
		ID: uuid.NewString(), Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", Price: d("30000"), Quantity: d("0.1"),
		Quote: d("3000"), BuyerOrderID: "o", BuyerUserID: user, SellerOrderID: "", SellerUserID: "HOUSE", BuyerFee: d("0.0001"),
		SellerFee: decimal.Zero, HouseSide: HouseSell, BuyerAccount: AccountMarginIsolated, BuyerAutoRepay: true,
	}
	postings, err := SettlementPostings(trade)
	if err != nil {
		t.Fatal(err)
	}
	if postings[0].EntryType != EntryMarginTradeSettle || postings[0].Lines[0].Account != iso.Assets("USDT") ||
		postings[0].Lines[3].Account != iso.Assets("BTC") {
		t.Fatalf("settlement %+v", postings[0])
	}
	if fee := postings[1]; fee.EntryType != EntryTradeFee || fee.Lines[0].Account != iso.Assets("BTC") {
		t.Fatalf("fee %+v", fee)
	}
	// It owes 0.05 BTC and 0.0002 of interest: 0.0999 received pays both.
	rows := map[AccountKey]Account{
		iso.DebtRow("BTC"):     {Key: iso.DebtRow("BTC"), Available: d("-0.05")},
		iso.InterestRow("BTC"): {Key: iso.InterestRow("BTC"), Available: d("-0.0002")},
	}
	repay := AutoRepayPostings(trade, rows)
	if len(repay) != 1 || repay[0].EntryType != EntryMarginRepay || repay[0].IdemKey != "trade-repay:"+trade.ID+":buyer" ||
		!repay[0].Lines[0].Amount.Equal(d("-0.0502")) || !repay[0].Lines[1].Amount.Equal(d("0.0002")) || !repay[0].Lines[2].Amount.Equal(d("0.05")) ||
		repay[0].Validate() != nil {
		t.Fatalf("auto-repay %+v", repay)
	}
	// Owing more than it got: it repays all it got, interest first.
	rows[iso.DebtRow("BTC")] = Account{Key: iso.DebtRow("BTC"), Available: d("-1")}
	if repay = AutoRepayPostings(trade, rows); !repay[0].Lines[0].Amount.Equal(d("-0.0999")) || !repay[0].Lines[2].Amount.Equal(d("0.0997")) {
		t.Fatalf("partial %+v", repay)
	}
	// Owing nothing: no journal.
	if repay = AutoRepayPostings(trade, map[AccountKey]Account{}); len(repay) != 0 {
		t.Fatalf("nothing owed %+v", repay)
	}
	if keys := trade.RepayAccounts(); len(keys) != 2 || keys[0] != iso.DebtRow("BTC") {
		t.Fatalf("repay accounts %v", keys)
	}
	// A SPOT order cannot repay, a pair's isolated account is its own.
	spot := trade
	spot.BuyerAccount = AccountSpot
	if _, err := SettlementPostings(spot); err == nil {
		t.Fatal("a SPOT order repaid")
	}
	spot.BuyerAutoRepay = false
	if p, err := SettlementPostings(spot); err != nil || p[0].EntryType != EntryHouseTradeSettle {
		t.Fatalf("a SPOT buy against HOUSE: %v", err)
	}
	odd := trade
	odd.BuyerAccount = "FUTURES"
	if _, err := SettlementPostings(odd); err == nil {
		t.Fatal("a FUTURES order settled on spot")
	}
}

func TestScopedFreeze(t *testing.T) {
	user := uuid.NewString()
	p, err := FreezeScopedPosting("k", EntryOrderFreeze, user, AccountMarginIsolated, "BTC-USDT", "USDT", d("10"), 6, "order")
	if err != nil || p.Lines[0].Account.Scope != "BTC-USDT" || p.Validate() != nil {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := FreezeScopedPosting("k", EntryOrderFreeze, user, AccountMarginIsolated, "", "USDT", d("10"), 6, ""); err == nil {
		t.Fatal("an isolated freeze without its pair")
	}
	if _, err := FreezeScopedPosting("k", EntryOrderFreeze, user, AccountMarginCrossDebt, "", "USDT", d("10"), 6, ""); err == nil {
		t.Fatal("froze a debt row")
	}
	old, _ := FreezePosting("k", EntryOrderFreeze, user, AccountSpot, "USDT", d("10"), 6, "order")
	scoped, _ := FreezeScopedPosting("k", EntryOrderFreeze, user, AccountSpot, "", "USDT", d("10"), 6, "order")
	if string(old.Hash()) != string(scoped.Hash()) {
		t.Fatal("a SPOT freeze hashes differently through the scoped path")
	}
}

// RepayReleasedPosting (B160): up to the cap, interest first, at most what
// is owed and what the account holds available (B163 ④); nothing without
// either.
func TestRepayReleasedPosting(t *testing.T) {
	user, order := uuid.NewString(), uuid.NewString()
	cross := MarginRef{UserID: user, AccountType: AccountMarginCross}
	rows := func(available, debt, interest string) map[AccountKey]Account {
		return map[AccountKey]Account{
			cross.Assets("USDT"):      {Key: cross.Assets("USDT"), Available: d(available)},
			cross.DebtRow("USDT"):     {Key: cross.DebtRow("USDT"), Available: d(debt)},
			cross.InterestRow("USDT"): {Key: cross.InterestRow("USDT"), Available: d(interest)},
		}
	}
	for _, c := range []struct {
		name, upTo, available, debt, interest string
		paid, toInterest, toDebt              string // "" when nothing is posted
	}{
		{"the cap", "6", "100", "-50", "-0.05", "6", "0.05", "5.95"},
		{"what is owed", "100", "100", "-50", "-0.05", "50.05", "0.05", "50"},
		{"what is available", "6", "4", "-50", "-0.05", "4", "0.05", "3.95"},
		{"interest only", "0.01", "100", "-50", "-0.05", "0.01", "0.01", ""},
		{"nothing available", "6", "0", "-50", "-0.05", "", "", ""},
		{"nothing owed", "6", "100", "0", "0", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, ok := RepayReleasedPosting(cross, "USDT", order, d(c.upTo), rows(c.available, c.debt, c.interest))
			if c.paid == "" {
				if ok {
					t.Fatalf("posted %+v", p)
				}
				return
			}
			if !ok || p.EntryType != EntryMarginRepay || p.IdemKey != ReleaseRepayKey(order) || p.Validate() != nil ||
				p.Memo != "auto-repay order "+order+" release" || !p.Lines[0].Amount.Equal(d(c.paid).Neg()) {
				t.Fatalf("posting %+v", p)
			}
			got := map[AccountKey]decimal.Decimal{}
			for _, l := range p.Lines[1:] {
				got[l.Account] = l.Amount
			}
			if c.toInterest != "" && !got[cross.InterestRow("USDT")].Equal(d(c.toInterest)) {
				t.Fatalf("to interest %v", got)
			}
			if c.toDebt != "" && !got[cross.DebtRow("USDT")].Equal(d(c.toDebt)) {
				t.Fatalf("to debt %v", got)
			}
		})
	}
}
