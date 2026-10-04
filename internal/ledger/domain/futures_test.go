package domain

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

const trader = "0192a0c4-0000-7000-8000-000000000001"

func futuresAccount(available, frozen string) Account {
	return Account{Key: UserAccount(trader, AccountFutures, "USDT"), Available: d(available), Frozen: d(frozen)}
}

func limit(s string) *decimal.Decimal {
	v := d(s)
	return &v
}

// net sums what the plan does to each account and balance kind.
func net(plan FuturesPlan) map[string]decimal.Decimal {
	out := map[string]decimal.Decimal{}
	for _, p := range plan.Postings {
		if err := p.Validate(); err != nil {
			panic(err)
		}
		for _, l := range p.Lines {
			k := l.Account.Type + "/" + l.Kind
			out[k] = out[k].Add(l.Amount)
		}
	}
	return out
}

func TestClosingAProfitableCrossPosition(t *testing.T) {
	// A long closes: its 100 of margin is released, 30 of profit and a
	// 0.6 fee.
	plan, err := FuturesPostings(FuturesRequest{
		IdemKey: "fill:t1:B", UserID: trader, Asset: "USDT", Reference: "BTC-USDT-PERP trade t1",
		Moves: []FuturesMove{
			{Type: MoveUnfreeze, Amount: d("100")},
			{Type: MoveProfit, Amount: d("30")},
			{Type: MoveFee, Amount: d("0.6")},
		},
	}, futuresAccount("0", "150"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Postings) != 3 || plan.Postings[0].EntryType != EntryOrderUnfreeze || plan.Postings[1].EntryType != EntryRealizedPnL ||
		plan.Postings[2].EntryType != EntryTradeFee || plan.Postings[2].IdemKey != "futures:fill:t1:B:2" {
		t.Fatalf("postings %+v", plan.Postings)
	}
	n := net(plan)
	if !n["FUTURES/AVAILABLE"].Equal(d("129.4")) || !n["FUTURES/FROZEN"].Equal(d("-100")) || !n["PNL_CLEARING/AVAILABLE"].Equal(d("-30")) ||
		!n["FEE_REVENUE/AVAILABLE"].Equal(d("0.6")) {
		t.Fatalf("net %v", n)
	}
	if o := plan.Outcomes[2]; !o.User.Equal(d("-0.6")) || !o.Waived.IsZero() || plan.Posted[2] != 2 {
		t.Fatalf("fee outcome %+v", o)
	}
}

func TestALossBeyondTheBalanceFallsOnTheInsuranceFund(t *testing.T) {
	// Cross: the loss takes what is available after the release; the fee
	// is waived.
	plan, err := FuturesPostings(FuturesRequest{
		IdemKey: "fill:t2:S", UserID: trader, Asset: "USDT", Reference: "trade t2",
		Moves: []FuturesMove{
			{Type: MoveUnfreeze, Amount: d("100")},
			{Type: MoveLoss, Amount: d("130")},
			{Type: MoveFee, Amount: d("0.6")},
		},
	}, futuresAccount("20", "100"))
	if err != nil {
		t.Fatal(err)
	}
	if o := plan.Outcomes[1]; !o.User.Equal(d("-120")) || !o.Insurance.Equal(d("10")) {
		t.Fatalf("loss outcome %+v", o)
	}
	if o := plan.Outcomes[2]; !o.User.IsZero() || !o.Waived.Equal(d("0.6")) || plan.Posted[2] != -1 {
		t.Fatalf("fee outcome %+v, posted %v", o, plan.Posted)
	}
	n := net(plan)
	if !n["FUTURES/AVAILABLE"].Equal(d("-20")) || !n["INSURANCE_FUND/AVAILABLE"].Equal(d("-10")) || !n["PNL_CLEARING/AVAILABLE"].Equal(d("130")) {
		t.Fatalf("net %v", n)
	}

	// Isolated: the loss stops at the position's margin although more is
	// available, and the insurance fund pays the rest.
	plan, err = FuturesPostings(FuturesRequest{
		IdemKey: "fill:t3:S", UserID: trader, Asset: "USDT",
		Moves: []FuturesMove{
			{Type: MoveLoss, Amount: d("130"), Kind: Frozen, Limit: limit("100")},
			{Type: MoveFee, Amount: d("0.6"), Kind: Frozen, Limit: limit("0")},
		},
	}, futuresAccount("500", "100"))
	if err != nil {
		t.Fatal(err)
	}
	if o := plan.Outcomes[0]; !o.User.Equal(d("-100")) || !o.Insurance.Equal(d("30")) {
		t.Fatalf("isolated loss %+v", o)
	}
	if n := net(plan); !n["FUTURES/AVAILABLE"].IsZero() || !n["FUTURES/FROZEN"].Equal(d("-100")) {
		t.Fatalf("isolated net %v", n)
	}
}

func TestLiquidationLeavesTheRestToTheInsuranceFund(t *testing.T) {
	plan, err := FuturesPostings(FuturesRequest{
		IdemKey: "liq:t4", UserID: trader, Asset: "USDT",
		Moves: []FuturesMove{
			{Type: MoveLoss, Amount: d("92"), Kind: Frozen, Limit: limit("100"), EntryType: EntryLiquidationSettle},
			{Type: MoveInsurance, Amount: d("8"), Kind: Frozen},
		},
	}, futuresAccount("0", "100"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Postings[0].EntryType != EntryLiquidationSettle || plan.Postings[1].EntryType != EntryInsuranceContribution {
		t.Fatalf("entry types %s %s", plan.Postings[0].EntryType, plan.Postings[1].EntryType)
	}
	if n := net(plan); !n["FUTURES/FROZEN"].Equal(d("-100")) || !n["INSURANCE_FUND/AVAILABLE"].Equal(d("8")) {
		t.Fatalf("net %v", n)
	}
}

func TestFundingAndMarginMoves(t *testing.T) {
	plan, err := FuturesPostings(FuturesRequest{
		IdemKey: "funding:BTC-USDT-PERP:1759219200:p1", UserID: trader, Asset: "USDT",
		Moves: []FuturesMove{
			{Type: MoveFundingPay, Amount: d("5"), Kind: Frozen},
			{Type: MoveFundingReceive, Amount: d("1"), Kind: Frozen},
			{Type: MoveFreeze, Amount: d("50"), Partial: true},
		},
	}, futuresAccount("20", "3"))
	if err != nil {
		t.Fatal(err)
	}
	if o := plan.Outcomes[0]; !o.User.Equal(d("-3")) || !o.Insurance.Equal(d("2")) || plan.Postings[0].EntryType != EntryFundingPayment {
		t.Fatalf("funding paid %+v", o)
	}
	if o := plan.Outcomes[2]; !o.Waived.Equal(d("30")) {
		t.Fatalf("partial freeze %+v", o)
	}
	n := net(plan)
	if !n["FUNDING_CLEARING/AVAILABLE"].Equal(d("4")) || !n["FUTURES/FROZEN"].Equal(d("18")) || !n["FUTURES/AVAILABLE"].Equal(d("-20")) {
		t.Fatalf("net %v", n)
	}
}

func TestExactMovesRefuseToOverdraw(t *testing.T) {
	for _, moves := range [][]FuturesMove{
		{{Type: MoveUnfreeze, Amount: d("101")}},
		{{Type: MoveFreeze, Amount: d("51")}},
		{{Type: MoveInsurance, Amount: d("1"), Kind: Frozen}, {Type: MoveUnfreeze, Amount: d("100")}},
	} {
		_, err := FuturesPostings(FuturesRequest{IdemKey: "k", UserID: trader, Asset: "USDT", Moves: moves}, futuresAccount("50", "100"))
		if apperr.From(err).Code != ErrInsufficientBalance.Code {
			t.Fatalf("%+v: %v", moves, err)
		}
	}
}

func TestFuturesRequestsAreValidated(t *testing.T) {
	ok := FuturesRequest{IdemKey: "k", UserID: trader, Asset: "USDT", Moves: []FuturesMove{{Type: MoveFee, Amount: d("1")}}}
	for name, mutate := range map[string]func(*FuturesRequest){
		"no moves":               func(r *FuturesRequest) { r.Moves = nil },
		"a bad user":             func(r *FuturesRequest) { r.UserID = "someone" },
		"an unknown move":        func(r *FuturesRequest) { r.Moves[0].Type = "GIFT" },
		"a zero amount":          func(r *FuturesRequest) { r.Moves[0].Amount = decimal.Zero },
		"a bad kind":             func(r *FuturesRequest) { r.Moves[0].Kind = "PENDING" },
		"a limit on PROFIT":      func(r *FuturesRequest) { r.Moves[0].Type, r.Moves[0].Limit = MoveProfit, limit("1") },
		"a partial fee":          func(r *FuturesRequest) { r.Moves[0].Partial = true },
		"an entry type on a fee": func(r *FuturesRequest) { r.Moves[0].EntryType = EntryLiquidationSettle },
		"a transfer entry type":  func(r *FuturesRequest) { r.Moves[0].Type, r.Moves[0].EntryType = MoveLoss, EntryAccountTransfer },
	} {
		r := ok
		r.Moves = append([]FuturesMove(nil), ok.Moves...)
		mutate(&r)
		var e *apperr.Error
		if err := r.Validate(); !errors.As(err, &e) || e.Kind != apperr.KindInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	// The hash tells a replay from another request.
	other := ok
	other.Moves = []FuturesMove{{Type: MoveFee, Amount: d("1"), Limit: limit("1")}}
	same := ok
	if string(ok.Hash()) == string(other.Hash()) || string(ok.Hash()) != string(same.Hash()) {
		t.Fatal("hash")
	}
}

func TestPnLClearingMayGoNegative(t *testing.T) {
	a := Account{Key: SystemAccount(AccountPnLClearing, "USDT")}
	if err := a.Apply(Line{Account: a.Key, Amount: d("-5"), Kind: Available}); err != nil || !a.Available.Equal(d("-5")) {
		t.Fatalf("%v %s", err, a.Available)
	}
	if err := a.Key.Validate(); err != nil {
		t.Fatal(err)
	}
}
