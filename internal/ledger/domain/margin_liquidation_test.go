package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

func TestLiquidationMoves(t *testing.T) {
	iso := MarginRef{UserID: uuid.NewString(), AccountType: AccountMarginIsolated, Scope: "BTC-USDT"}
	assets, debt, interest := iso.Assets("USDT"), iso.DebtRow("USDT"), iso.InterestRow("USDT")
	fund := SystemAccount(AccountInsuranceFund, "USDT")
	// 100 left, 150 principal and 2 interest owed, 1000 in the fund.
	state := map[AccountKey]decimal.Decimal{assets: d("100"), debt: d("-150"), interest: d("-2"), fund: d("1000")}
	req := MarginRequest{IdemKey: "liquidation:1", Account: iso, Reference: "liquidation 1", Moves: []MarginMove{
		{Type: MarginLiquidationRepay, Asset: "USDT", Amount: d("100"), Interest: d("2")},
		{Type: MarginInsuranceCover, Asset: "USDT", Amount: d("52")},
	}}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	postings, err := MarginPostings(req, accountsOf(req.MarginAccounts(), state))
	if err != nil || len(postings) != 2 {
		t.Fatalf("%d postings, %v", len(postings), err)
	}
	got := map[AccountKey]decimal.Decimal{}
	for k, v := range state {
		got[k] = v
	}
	for _, p := range postings {
		if p.EntryType != EntryMarginLiquidate || p.Validate() != nil {
			t.Fatalf("posting %s %v", p.EntryType, p.Validate())
		}
		for _, l := range p.Lines {
			got[l.Account] = got[l.Account].Add(l.Amount)
		}
	}
	if !got[assets].IsZero() || !got[debt].IsZero() || !got[interest].IsZero() || !got[fund].Equal(d("948")) {
		t.Fatalf("after %v", got)
	}
	// More than the debt left, or than the fund holds, is refused.
	req.Moves[1].Amount = d("52.01")
	if _, err := MarginPostings(req, accountsOf(req.MarginAccounts(), state)); !apperr.Is(err, "LEDGER_DEBT_OVERPAID") {
		t.Fatalf("cover beyond the debt: %v", err)
	}
	req.Moves[1].Amount = d("52")
	short := map[AccountKey]decimal.Decimal{assets: d("100"), debt: d("-150"), interest: d("-2"), fund: d("10")}
	if _, err := MarginPostings(req, accountsOf(req.MarginAccounts(), short)); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("cover beyond the fund: %v", err)
	}
	// The fee goes from what is left to the fund.
	fee := MarginRequest{IdemKey: "liquidation:1:fee", Account: iso, Moves: []MarginMove{{Type: MarginLiquidationFee, Asset: "USDT", Amount: d("3")}}}
	postings, err = MarginPostings(fee, accountsOf(fee.MarginAccounts(), state))
	if err != nil || len(postings) != 1 || postings[0].EntryType != EntryMarginLiquidate || !postings[0].Lines[1].Amount.Equal(d("3")) ||
		postings[0].Lines[1].Account != fund {
		t.Fatalf("fee %+v %v", postings, err)
	}
	fee.Moves[0].Interest = d("1")
	if fee.Validate() == nil {
		t.Fatal("a fee with an interest part")
	}
}
