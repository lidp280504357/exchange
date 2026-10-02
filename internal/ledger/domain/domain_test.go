package domain

import (
	"math/rand/v2"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

const alice = "0199b000-0000-7000-8000-000000000001"

func TestPostingValidate(t *testing.T) {
	spot := UserAccount(alice, AccountSpot, "USDT")
	adj := SystemAccount(AccountAdjustment, "USDT")
	ok := Posting{IdemKey: "k", EntryType: EntryManualAdjustment, Lines: []Line{
		{Account: spot, Amount: d("5"), Kind: Available}, {Account: adj, Amount: d("-5"), Kind: Available},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	for name, mutate := range map[string]func(*Posting){
		"unbalanced":      func(p *Posting) { p.Lines[1].Amount = d("-4") },
		"zero amount":     func(p *Posting) { p.Lines[0].Amount, p.Lines[1].Amount = decimal.Zero, decimal.Zero },
		"one line":        func(p *Posting) { p.Lines = p.Lines[:1] },
		"no key":          func(p *Posting) { p.IdemKey = "" },
		"unknown type":    func(p *Posting) { p.EntryType = "GIFT" },
		"bad kind":        func(p *Posting) { p.Lines[0].Kind = "LOCKED" },
		"user fee acct":   func(p *Posting) { p.Lines[0].Account.Type = AccountFeeRevenue },
		"system spot":     func(p *Posting) { p.Lines[1].Account = SystemAccount(AccountSpot, "USDT") },
		"mixed per asset": func(p *Posting) { p.Lines[1].Account.Asset = "BTC" },
	} {
		p := ok
		p.Lines = append([]Line(nil), ok.Lines...)
		mutate(&p)
		if err := p.Validate(); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Two assets, each balanced on its own, are fine (a trade).
	trade := Posting{IdemKey: "t", EntryType: EntryTradeSettle, Lines: []Line{
		{Account: UserAccount(alice, AccountSpot, "USDT"), Amount: d("-100"), Kind: Frozen},
		{Account: SystemAccount(AccountFeeRevenue, "USDT"), Amount: d("100"), Kind: Available},
		{Account: UserAccount(alice, AccountSpot, "BTC"), Amount: d("0.001"), Kind: Available},
		{Account: SystemAccount(AccountFeeRevenue, "BTC"), Amount: d("-0.001"), Kind: Available},
	}}
	if err := trade.Validate(); err != nil {
		t.Fatalf("two balanced assets: %v", err)
	}
	if string(ok.Hash()) == string(trade.Hash()) {
		t.Fatal("hashes must differ")
	}
	same := ok
	same.Lines = []Line{{Account: spot, Amount: d("5.00"), Kind: Available}, {Account: adj, Amount: d("-5"), Kind: Available}}
	if string(ok.Hash()) != string(same.Hash()) {
		t.Fatal("equal amounts hash equally")
	}
}

func TestApplyRefusesNegativeBalances(t *testing.T) {
	user := Account{Key: UserAccount(alice, AccountSpot, "USDT"), Available: d("10")}
	if err := user.Apply(Line{Amount: d("-10.01"), Kind: Available}); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("overdraft: %v", err)
	}
	if !user.Available.Equal(d("10")) || user.Version != 0 {
		t.Fatalf("a refused line changes nothing: %+v", user)
	}
	if err := user.Apply(Line{Amount: d("-10"), Kind: Available}); err != nil || !user.Available.IsZero() || user.Version != 1 {
		t.Fatalf("exact balance: %v %+v", err, user)
	}
	adj := Account{Key: SystemAccount(AccountAdjustment, "USDT")}
	if err := adj.Apply(Line{Amount: d("-1000"), Kind: Available}); err != nil {
		t.Fatalf("the counterparty may go negative: %v", err)
	}
	fee := Account{Key: SystemAccount(AccountFeeRevenue, "USDT")}
	if err := fee.Apply(Line{Amount: d("-1"), Kind: Available}); err == nil {
		t.Fatal("fee revenue may not go negative")
	}
	// HOUSE below zero in a backed asset (a rule that changed, a hand
	// adjustment): credits bring it back, debits stay refused.
	house := Account{Key: SystemAccount(AccountMarketMaker, "USDT"), Available: d("-100")}
	if err := house.Apply(Line{Amount: d("40"), Kind: Available}); err != nil || !house.Available.Equal(d("-60")) {
		t.Fatalf("a credit to an account below zero: %v %+v", err, house)
	}
	if err := house.Apply(Line{Amount: d("-1"), Kind: Available}); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("a debit below zero: %v", err)
	}
	if err := house.Apply(Line{Amount: d("70"), Kind: Available}); err != nil || !house.Available.Equal(d("10")) {
		t.Fatalf("made whole: %v %+v", err, house)
	}
	// Only the balance the line changes counts: a frozen line goes through
	// while the available one is below zero.
	odd := Account{Key: SystemAccount(AccountMarketMaker, "BTC"), Available: d("-1"), Frozen: d("2")}
	if err := odd.Apply(Line{Amount: d("-1"), Kind: Frozen}); err != nil || !odd.Frozen.Equal(d("1")) {
		t.Fatalf("a frozen line: %v %+v", err, odd)
	}
}

func TestBuilders(t *testing.T) {
	p, err := FreezePosting("k", EntryOrderFreeze, alice, AccountSpot, "USDT", d("100"), 6, "order 1")
	if err != nil || p.Validate() != nil || p.Lines[0].Kind != Available || !p.Lines[1].Amount.Equal(d("100")) {
		t.Fatalf("freeze: %+v %v", p, err)
	}
	if _, err := FreezePosting("k", EntryOrderFreeze, alice, AccountSpot, "USDT", d("0.0000001"), 6, ""); !apperr.Is(err, "LEDGER_AMOUNT_PRECISION") {
		t.Fatalf("precision: %v", err)
	}
	if _, err := FreezePosting("k", EntryTradeFee, alice, AccountSpot, "USDT", d("1"), 6, ""); err == nil {
		t.Fatal("wrong entry type")
	}
	if _, err := UnfreezePosting("k", EntryOrderUnfreeze, alice, AccountFeeRevenue, "USDT", d("1"), 6, ""); err == nil {
		t.Fatal("users hold SPOT or FUTURES only")
	}
	p, err = TransferPosting("k", alice, "USDT", d("5"), 6, AccountSpot, AccountFutures)
	if err != nil || p.Validate() != nil || p.Lines[1].Account.Type != AccountFutures {
		t.Fatalf("transfer: %+v %v", p, err)
	}
	if _, err := TransferPosting("k", alice, "USDT", d("5"), 6, AccountSpot, AccountSpot); err == nil {
		t.Fatal("same account")
	}
	if _, err := TransferPosting("k", alice, "USDT", d("-5"), 6, AccountSpot, AccountFutures); err == nil {
		t.Fatal("negative amount")
	}
	p, err = AdjustmentPosting("k", alice, []Credit{{Asset: "USDT", Amount: d("10000"), Decimals: 6}, {Asset: "BTC", Amount: d("0.1"), Decimals: 8}}, "welcome")
	if err != nil || p.Validate() != nil || len(p.Lines) != 4 || len(p.Accounts()) != 4 || len(p.Assets()) != 2 {
		t.Fatalf("adjustment: %+v %v", p, err)
	}
}

// TestRandomPostingsKeepInvariants posts random transfers, freezes and
// adjustments between a few accounts and checks after every step that
// each asset still sums to zero and that no restricted account is negative
// (§11.4 invariants 1 and 3).
func TestRandomPostingsKeepInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // a seeded walk keeps the property test reproducible
	users := []string{alice, "0199b000-0000-7000-8000-000000000002", "0199b000-0000-7000-8000-000000000003"}
	accounts := map[AccountKey]*Account{}
	get := func(k AccountKey) *Account {
		if a, ok := accounts[k]; ok {
			return a
		}
		a := &Account{Key: k}
		accounts[k] = a
		return a
	}
	amount := func() decimal.Decimal { return decimal.New(rng.Int64N(1_000_000)+1, -2) }
	refused := 0
	for i := range 2000 {
		u := users[rng.IntN(len(users))]
		var p Posting
		var err error
		switch rng.IntN(4) {
		case 0:
			p, err = AdjustmentPosting("a", u, []Credit{{Asset: "USDT", Amount: amount(), Decimals: 6}}, "")
		case 1:
			p, err = TransferPosting("t", u, "USDT", amount(), 6, AccountSpot, AccountFutures)
		case 2:
			p, err = FreezePosting("f", EntryOrderFreeze, u, AccountSpot, "USDT", amount(), 6, "")
		default:
			p, err = UnfreezePosting("u", EntryOrderUnfreeze, u, AccountSpot, "USDT", amount(), 6, "")
		}
		if err != nil || p.Validate() != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		// Apply all lines to copies first, like the service does.
		staged := map[AccountKey]Account{}
		ok := true
		for _, l := range p.Lines {
			a, seen := staged[l.Account]
			if !seen {
				a = *get(l.Account)
			}
			if a.Apply(l) != nil {
				ok = false
				break
			}
			staged[l.Account] = a
		}
		if !ok {
			refused++
			continue
		}
		for k, a := range staged {
			*accounts[k] = a
		}
		total := decimal.Zero
		for _, a := range accounts {
			total = total.Add(a.Available).Add(a.Frozen)
			if !a.Key.MayGoNegative() && (a.Available.IsNegative() || a.Frozen.IsNegative()) {
				t.Fatalf("step %d: %s went negative", i, a.Key)
			}
		}
		if !total.IsZero() {
			t.Fatalf("step %d: USDT sums to %s", i, total)
		}
	}
	if refused == 0 || refused == 2000 {
		t.Fatalf("the walk should mix accepted and refused postings, refused %d", refused)
	}
}
