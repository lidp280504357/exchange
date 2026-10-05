package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

func TestMarginRepayments(t *testing.T) {
	cross := MarginRef{UserID: uuid.NewString(), AccountType: AccountMarginCross}
	assets, debt, interest := cross.Assets("USDT"), cross.DebtRow("USDT"), cross.InterestRow("USDT")
	// 100 held, 200 principal and 3 interest owed.
	owing := map[AccountKey]decimal.Decimal{assets: d("100"), debt: d("-200"), interest: d("-3")}
	clean := map[AccountKey]decimal.Decimal{assets: d("100"), debt: d("-200")}
	for _, c := range []struct {
		name               string
		state              map[AccountKey]decimal.Decimal
		amount, toInterest string
		err                string
		debt, interest     string
	}{
		{name: "interest only", state: owing, amount: "2", toInterest: "2", debt: "-200", interest: "-1"},
		{name: "interest then principal", state: owing, amount: "10", toInterest: "3", debt: "-193", interest: "0"},
		{name: "principal without interest owed", state: clean, amount: "10", toInterest: "0", debt: "-190", interest: "0"},
		{name: "principal while interest is owed", state: owing, amount: "10", toInterest: "2", err: "LEDGER_INTEREST_FIRST"},
		{name: "principal only while interest is owed", state: owing, amount: "1", toInterest: "0", err: "LEDGER_INTEREST_FIRST"},
		{name: "more interest than owed", state: owing, amount: "10", toInterest: "4", err: "LEDGER_DEBT_OVERPAID"},
		{
			name: "more principal than owed", state: map[AccountKey]decimal.Decimal{assets: d("300"), debt: d("-200")}, amount: "201",
			toInterest: "0", err: "LEDGER_DEBT_OVERPAID",
		},
		{name: "more than held", state: clean, amount: "100.01", toInterest: "0", err: "LEDGER_INSUFFICIENT_BALANCE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := MarginRequest{IdemKey: "k", Account: cross, Moves: []MarginMove{
				{Type: MarginRepay, Asset: "USDT", Amount: d(c.amount), Interest: d(c.toInterest)},
			}}
			if err := req.Validate(); err != nil {
				t.Fatal(err)
			}
			postings, err := MarginPostings(req, accountsOf(req.MarginAccounts(), c.state))
			if c.err != "" {
				if !apperr.Is(err, c.err) {
					t.Fatalf("want %s, got %v", c.err, err)
				}
				return
			}
			if err != nil || len(postings) != 1 || postings[0].Validate() != nil {
				t.Fatalf("%v %+v", err, postings)
			}
			got := map[AccountKey]decimal.Decimal{}
			for k, v := range c.state {
				got[k] = v
			}
			for _, l := range postings[0].Lines {
				got[l.Account] = got[l.Account].Add(l.Amount)
			}
			if !got[debt].Equal(d(c.debt)) || !got[interest].Equal(d(c.interest)) || !got[assets].Equal(c.state[assets].Sub(d(c.amount))) {
				t.Fatalf("debt %s interest %s assets %s", got[debt], got[interest], got[assets])
			}
		})
	}
}

func TestMarginRequestHash(t *testing.T) {
	cross := MarginRef{UserID: uuid.NewString(), AccountType: AccountMarginCross}
	req := func(amount string) MarginRequest {
		return MarginRequest{IdemKey: "margin-borrow:1", Account: cross, Reference: "borrow 1", Moves: []MarginMove{
			{Type: MarginBorrow, Asset: "USDT", Amount: d(amount)}, {Type: MarginInterest, Asset: "USDT", Amount: d("0.001")},
		}}
	}
	if string(req("150").Hash()) != string(req("150.000").Hash()) {
		t.Fatal("a replay hashes differently")
	}
	if string(req("150").Hash()) == string(req("150.01").Hash()) {
		t.Fatal("another amount hashes the same")
	}
	other := req("150")
	other.Moves[0], other.Moves[1] = other.Moves[1], other.Moves[0]
	if string(req("150").Hash()) == string(other.Hash()) {
		t.Fatal("the moves in another order hash the same")
	}
	iso := req("150")
	iso.Account = MarginRef{UserID: cross.UserID, AccountType: AccountMarginIsolated, Scope: "BTC-USDT"}
	if string(req("150").Hash()) == string(iso.Hash()) {
		t.Fatal("another account hashes the same")
	}
}
